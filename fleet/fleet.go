// Package fleet implements the coordinator side of the coordinator/worker
// architecture: a registry of remote llm_server_manager instances ("workers")
// plus an HTTP client for their existing REST API. Workers run today's binary
// unmodified; the coordinator authenticates with each worker's own api-key.
package fleet

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"syscall"
	"time"

	"llamamanager/config"
	"llamamanager/models"
)

// Machine status values used in worker and fleet views.
const (
	StatusOnline  = "online"
	StatusOffline = "offline"
)

// DefaultCacheTTL is how long a worker probe result is reused. It keeps
// UI/CLI polling cheap; the proxy (Phase 2) keeps its own, shorter-lived
// routing cache.
const DefaultCacheTTL = 2 * time.Second

// WorkerStatus is the lean per-worker view served by GET /api/v1/workers.
type WorkerStatus struct {
	Name         string `json:"name"`
	URL          string `json:"url"`
	APIKeySet    bool   `json:"api_key_set"`
	Status       string `json:"status"`
	RunningModel string `json:"running_model,omitempty"`
	ModelCount   int    `json:"model_count"`
	Error        string `json:"error,omitempty"`
}

// FleetMachine is one machine in the GET /api/v1/fleet view: the coordinator
// itself (machine "local") first, then the workers in config order.
type FleetMachine struct {
	Machine string             `json:"machine"`
	URL     string             `json:"url,omitempty"`
	Status  string             `json:"status"`
	Running *RunningModel      `json:"running,omitempty"`
	Models  []models.ModelItem `json:"models"`
	Error   string             `json:"error,omitempty"`
}

// WorkerSnapshot is the probed state of one worker: everything the /workers
// and /fleet views derive from.
type WorkerSnapshot struct {
	Worker *Worker
	Status string             // StatusOnline | StatusOffline
	Models []models.ModelItem // nil when offline
	Error  string             // transport/auth failure detail when offline
}

// WorkerListResponse and FleetResponse wrap the views for the APIResponse
// envelope.
type WorkerListResponse struct {
	Workers []WorkerStatus `json:"workers"`
}

type FleetResponse struct {
	Machines []FleetMachine `json:"machines"`
}

// Registry holds the configured workers. It is safe for concurrent use and
// swapped atomically on config reload.
type Registry struct {
	logger func(format string, args ...interface{})

	mu      sync.RWMutex
	workers []*Worker

	cacheMu  sync.Mutex
	cache    map[string]cacheEntry // keyed by worker URL
	cacheTTL time.Duration
}

type cacheEntry struct {
	snapshot WorkerSnapshot
	at       time.Time
}

// NewRegistry builds the worker set from the coordinator config. Entries
// without a parseable URL are skipped with a warning, mirroring the
// nameless-model rule.
func NewRegistry(specs []config.WorkerConfig, logger func(format string, args ...interface{})) *Registry {
	if logger == nil {
		logger = func(string, ...interface{}) {}
	}
	r := &Registry{
		logger:   logger,
		cache:    make(map[string]cacheEntry),
		cacheTTL: DefaultCacheTTL,
	}
	r.load(specs)
	return r
}

// load replaces the worker set. Callers hold the appropriate lock.
func (r *Registry) load(specs []config.WorkerConfig) {
	workers := make([]*Worker, 0, len(specs))
	seen := make(map[string]bool, len(specs))
	for i, spec := range specs {
		w, err := NewWorker(spec)
		if err != nil {
			r.logger("Warning: skipping worker entry %d: %v", i, err)
			continue
		}
		if seen[w.Name] {
			r.logger("Warning: worker name %q is duplicated; the first entry wins for name lookup", w.Name)
		}
		if w.Name == LocalMachine {
			// "local" is the coordinator's own machine name in fleet views
			// and routing; a worker claiming it makes views ambiguous.
			r.logger("Warning: worker name %q is reserved for the coordinator itself; choose a different name", w.Name)
		}
		seen[w.Name] = true
		workers = append(workers, w)
	}
	r.workers = workers
}

// Reload swaps the worker set (config reload) and drops cached probe results.
func (r *Registry) Reload(specs []config.WorkerConfig) {
	r.mu.Lock()
	r.load(specs)
	r.mu.Unlock()

	r.cacheMu.Lock()
	r.cache = make(map[string]cacheEntry)
	r.cacheMu.Unlock()
}

// Workers returns a snapshot of the configured workers, in config order.
func (r *Registry) Workers() []*Worker {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Worker, len(r.workers))
	copy(out, r.workers)
	return out
}

// Resolve finds a worker by configured name or URL host (with or without
// port). The first match wins.
func (r *Registry) Resolve(nameOrHost string) (*Worker, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, w := range r.workers {
		if w.Name == nameOrHost {
			return w, nil
		}
	}
	for _, w := range r.workers {
		if u, err := url.Parse(w.URL); err == nil && (u.Hostname() == nameOrHost || u.Host == nameOrHost) {
			return w, nil
		}
	}
	return nil, fmt.Errorf("worker %q not found", nameOrHost)
}

// Snapshots probes every worker concurrently, in config order.
func (r *Registry) Snapshots(ctx context.Context) []WorkerSnapshot {
	workers := r.Workers()
	snaps := make([]WorkerSnapshot, len(workers))
	var wg sync.WaitGroup
	for i, w := range workers {
		wg.Add(1)
		go func(i int, w *Worker) {
			defer wg.Done()
			snaps[i] = r.Snapshot(ctx, w)
		}(i, w)
	}
	wg.Wait()
	return snaps
}

// Snapshot probes one worker and returns its state, reusing a cached result
// while the cache TTL has not expired.
func (r *Registry) Snapshot(ctx context.Context, w *Worker) WorkerSnapshot {
	if r.cacheTTL > 0 {
		r.cacheMu.Lock()
		if e, ok := r.cache[w.URL]; ok && time.Since(e.at) < r.cacheTTL {
			r.cacheMu.Unlock()
			return e.snapshot
		}
		r.cacheMu.Unlock()
	}

	snap, cacheable := r.probe(ctx, w)

	// A probe cut short by the caller's own request context says nothing
	// about worker health — caching it would report a false "offline" to the
	// next caller.
	if cacheable && r.cacheTTL > 0 {
		r.cacheMu.Lock()
		r.cache[w.URL] = cacheEntry{snapshot: snap, at: time.Now()}
		r.cacheMu.Unlock()
	}
	return snap
}

func (r *Registry) probe(ctx context.Context, w *Worker) (WorkerSnapshot, bool) {
	snap := WorkerSnapshot{Worker: w, Status: StatusOffline}
	items, err := w.ListModels(ctx)
	if err != nil {
		snap.Error = describeWorkerError(err)
		// Cacheable only when the caller's context is still healthy — i.e.
		// the failure came from the worker side (its own read timeout, a
		// real refusal). A caller deadline/cancellation says nothing about
		// worker health and must not be cached.
		return snap, ctx.Err() == nil
	}
	snap.Status = StatusOnline
	snap.Models = items
	return snap, true
}

// describeWorkerError turns transport/auth failures into short, plain-language
// fleet-view messages. Raw Go/network error strings ("dial tcp ...: connect:
// connection refused") are deliberately not surfaced to the UI.
func describeWorkerError(err error) string {
	var we *WorkerError
	if errors.As(err, &we) {
		if we.StatusCode == http.StatusUnauthorized {
			return "Unauthorized — check the api-key"
		}
		if we.Message != "" {
			return we.Message
		}
		return fmt.Sprintf("The manager returned an error (HTTP %d)", we.StatusCode)
	}

	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "Timed out"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "Connection refused — the manager isn't running"
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		return "Network unreachable"
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "Host name could not be resolved"
	}

	return "Unreachable"
}

// StatusView derives the lean /workers view from a snapshot.
func (s WorkerSnapshot) StatusView() WorkerStatus {
	st := WorkerStatus{
		Name:      s.Worker.Name,
		URL:       s.Worker.URL,
		APIKeySet: s.Worker.APIKey != "",
		Status:    s.Status,
		Error:     s.Error,
	}
	if s.Models != nil {
		st.ModelCount = len(s.Models)
		if running := RunningFromItems(s.Models); running != nil {
			st.RunningModel = running.Name
		}
	}
	return st
}

// FleetMachine derives the /fleet view for a worker from a snapshot.
func (s WorkerSnapshot) FleetMachine() FleetMachine {
	m := FleetMachine{
		Machine: s.Worker.Name,
		URL:     s.Worker.URL,
		Status:  s.Status,
		Error:   s.Error,
		Models:  []models.ModelItem{},
	}
	if s.Models != nil {
		m.Models = s.Models
		m.Running = RunningFromItems(s.Models)
	}
	return m
}

// WorkerStatuses probes every worker concurrently and returns the lean view
// for GET /api/v1/workers.
func (r *Registry) WorkerStatuses(ctx context.Context) []WorkerStatus {
	snaps := r.Snapshots(ctx)
	out := make([]WorkerStatus, 0, len(snaps))
	for _, s := range snaps {
		out = append(out, s.StatusView())
	}
	return out
}
