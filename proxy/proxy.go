// Package proxy implements the OpenAI-compatible /v1 router: it maps each
// request's "model" field to the machine (local or worker) currently running
// that model and reverse-proxies the exchange untouched, with full SSE
// streaming support. The coordinator is a thin router — llama-server already
// serves the OpenAI API.
package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"llamamanager/fleet"
	"llamamanager/models"
)

// DefaultRouteTTL is how long the routing table is trusted before the next
// request triggers a rebuild. Worker probes underneath are cached by the
// fleet registry with the same TTL, so rebuilds stay cheap.
const DefaultRouteTTL = 2 * time.Second

// LocalSource is the coordinator's view of its own machine.
// *manager.ServerManager satisfies it directly.
type LocalSource interface {
	ListModels() map[string]*models.ModelConfig
	GetCurrentServer() *models.RunningServer
}

// target is one routable machine for a published model name.
type target struct {
	machine string
	baseURL string // http://<host>:<port>
	warming bool   // machine reports the model "starting" (observable on the local machine only)
}

// publishedModel is one GET /v1/models entry: the canonical published name of
// a running model (alias when set, else real name).
type publishedModel struct {
	id      string
	created int64
}

type refreshGroup struct{ done chan struct{} }

// nameClaim records which machine first claimed a published name and whether
// it did so via an alias — the input for collision warnings.
type nameClaim struct {
	machine string
	isAlias bool
}

type Router struct {
	local       LocalSource
	fleet       *fleet.Registry
	logger      func(format string, args ...interface{})
	apiKey      string
	authEnabled bool
	ttl         time.Duration
	upstream    http.RoundTripper

	mu      sync.RWMutex
	table   map[string][]target
	entries []publishedModel
	builtAt time.Time
	warned  map[string]bool

	refMu    sync.Mutex
	inflight map[string]*refreshGroup
}

// New builds the /v1 router. The local machine participates as "local"; the
// fleet registry supplies the workers.
func New(local LocalSource, flt *fleet.Registry, apiKey string, authEnabled bool, logger func(format string, args ...interface{})) *Router {
	if logger == nil {
		logger = func(string, ...interface{}) {}
	}
	return &Router{
		local:       local,
		fleet:       flt,
		logger:      logger,
		apiKey:      apiKey,
		authEnabled: authEnabled,
		ttl:         DefaultRouteTTL,
		upstream: &http.Transport{
			Proxy:           http.ProxyFromEnvironment,
			DialContext:     (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			MaxIdleConns:    100,
			IdleConnTimeout: 90 * time.Second,
		},
		table:    make(map[string][]target),
		warned:   make(map[string]bool),
		inflight: make(map[string]*refreshGroup),
	}
}

// lookup returns the targets for name, with ok=false when the name is absent
// or the table is stale.
func (p *Router) lookup(name string) ([]target, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	tgts, ok := p.table[name]
	return tgts, ok && time.Since(p.builtAt) < p.ttl
}

// resolve returns the first-match targets for a published name. A missing or
// stale entry triggers one synchronous table rebuild (single-flight per name)
// so the answer reflects the current fleet state.
func (p *Router) resolve(name string) ([]target, bool) {
	if tgts, ok := p.lookup(name); ok {
		return tgts, true
	}
	p.refresh(name)
	return p.lookup(name)
}

// refresh rebuilds the table at most once per name concurrently: parallel
// requests for the same unknown model share one rebuild.
func (p *Router) refresh(name string) {
	p.refMu.Lock()
	if g, ok := p.inflight[name]; ok {
		p.refMu.Unlock()
		<-g.done
		return
	}
	g := &refreshGroup{done: make(chan struct{})}
	p.inflight[name] = g
	p.refMu.Unlock()

	defer func() {
		close(g.done)
		p.refMu.Lock()
		delete(p.inflight, name)
		p.refMu.Unlock()
	}()

	p.rebuild()
}

// invalidate drops a name and ages out the whole table so the next resolve
// rebuilds from live worker state.
func (p *Router) invalidate(name string) {
	p.mu.Lock()
	delete(p.table, name)
	p.builtAt = time.Time{}
	p.mu.Unlock()
}

// rebuild recomputes the routing table and the /v1/models entry list from the
// local manager and every online worker. First-match order: local, then
// workers in config order.
func (p *Router) rebuild() {
	table := make(map[string][]target)
	entries := make([]publishedModel, 0, 8)
	claims := make(map[string]nameClaim)
	seen := make(map[string]bool)

	addTarget := func(machine, host string, warming bool, cfg models.ModelConfig, created int64) {
		port, routable := fleet.RoutablePort(cfg)
		if !routable {
			// launch_cmd without a parseable --port: controllable via the
			// control plane, but never published or routed on /v1.
			return
		}
		t := target{machine: machine, baseURL: fmt.Sprintf("http://%s:%d", host, port), warming: warming}

		name := cfg.Name
		keys := []struct {
			key     string
			isAlias bool
		}{{key: name}}
		if alias := cfg.Alias; alias != "" && alias != name {
			keys = append(keys, struct {
				key     string
				isAlias bool
			}{key: alias, isAlias: true})
		}

		for _, k := range keys {
			if claim, ok := claims[k.key]; ok && (claim.isAlias || k.isAlias) && claim.machine != machine {
				p.warnOnce("proxy: published name %q is claimed as %s on machine %q and %s on machine %q; first-match wins",
					k.key, claimLabel(claim), claim.machine, claimLabel(nameClaim{machine: machine, isAlias: k.isAlias}), machine)
			} else if _, ok := claims[k.key]; !ok {
				claims[k.key] = nameClaim{machine: machine, isAlias: k.isAlias}
			}
			table[k.key] = append(table[k.key], t)
		}

		id := publishName(cfg)
		if id != "" && !seen[id] {
			seen[id] = true
			entries = append(entries, publishedModel{id: id, created: created})
		}
	}

	if cur := p.local.GetCurrentServer(); cur != nil {
		addTarget(fleet.LocalMachine, "127.0.0.1", cur.Status == models.StatusStarting, cur.ModelConfig, cur.StartTime.Unix())
	}
	for _, snap := range p.fleet.Snapshots(context.Background()) {
		if snap.Status != fleet.StatusOnline {
			continue
		}
		host := hostname(snap.Worker.URL)
		for _, item := range snap.Models {
			if item.Active {
				addTarget(snap.Worker.Name, host, false, item.ModelConfig, 0)
			}
		}
	}

	p.mu.Lock()
	p.table = table
	p.entries = entries
	p.builtAt = time.Now()
	p.mu.Unlock()
}

func claimLabel(c nameClaim) string {
	if c.isAlias {
		return "an alias"
	}
	return "a real model name"
}

// publishName is the canonical /v1/models name for a model: alias when set,
// else the real name.
func publishName(cfg models.ModelConfig) string {
	if cfg.Alias != "" {
		return cfg.Alias
	}
	return cfg.Name
}

// hostname extracts the host (no port) a worker's llama ports live on.
func hostname(workerURL string) string {
	if u, err := url.Parse(workerURL); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return "127.0.0.1"
}

// warnOnce logs a warning only the first time a given message is produced —
// rebuilds run on demand and would otherwise repeat themselves.
func (p *Router) warnOnce(format string, args ...interface{}) {
	key := fmt.Sprintf(format, args...)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.warned[key] {
		return
	}
	p.warned[key] = true
	p.logger("Warning: %s", key)
}
