package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"llamamanager/config"
	"llamamanager/models"
)

func modelsPayload(items ...models.ModelItem) models.APIResponse {
	return models.APIResponse{Success: true, Data: models.ModelListResponse{Models: items}}
}

func writeModels(t *testing.T, w http.ResponseWriter, items ...models.ModelItem) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(modelsPayload(items...)); err != nil {
		t.Fatalf("fake write: %v", err)
	}
}

func TestNewWorkerParsesURLAndDefaultsNameToHost(t *testing.T) {
	w, err := NewWorker(config.WorkerConfig{URL: "http://gpu1.local:8080/"})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	if w.Name != "gpu1.local" {
		t.Fatalf("Name = %q, want the URL hostname gpu1.local", w.Name)
	}
	if w.URL != "http://gpu1.local:8080" {
		t.Fatalf("URL = %q, want trailing slash trimmed", w.URL)
	}
}

func TestNewWorkerRejectsInvalidEntries(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"missing scheme", "gpu1.local:8080"},
		{"unsupported scheme", "ftp://gpu1.local"},
		{"missing host", "http://"},
		{"garbage", "not a url"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewWorker(config.WorkerConfig{URL: tc.url}); err == nil {
				t.Fatalf("NewWorker(%q) should fail", tc.url)
			}
		})
	}
}

func TestClientSendsAPIKeyAndParsesModels(t *testing.T) {
	var gotKey string
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("api-key")
		gotPath = r.URL.Path
		writeModels(t, w, models.ModelItem{
			ModelConfig: models.ModelConfig{Name: "big", Alias: "gpt-x", Port: intPtr(8099)},
			Active:      true,
		})
	}))
	defer ts.Close()

	w, err := NewWorker(config.WorkerConfig{URL: ts.URL, APIKey: "secretkey123456"})
	if err != nil {
		t.Fatal(err)
	}

	items, err := w.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if gotKey != "secretkey123456" {
		t.Fatalf("api-key header = %q", gotKey)
	}
	if gotPath != "/api/v1/models" {
		t.Fatalf("path = %q", gotPath)
	}
	if len(items) != 1 || items[0].Name != "big" || items[0].Alias != "gpt-x" {
		t.Fatalf("items = %+v", items)
	}

	running, err := w.GetRunning(context.Background())
	if err != nil {
		t.Fatalf("GetRunning: %v", err)
	}
	if running == nil || running.Name != "big" || running.Alias != "gpt-x" || running.Port != 8099 {
		t.Fatalf("running = %+v", running)
	}
}

func TestGetRunningNone(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeModels(t, w, models.ModelItem{ModelConfig: models.ModelConfig{Name: "idle"}})
	}))
	defer ts.Close()

	w, err := NewWorker(config.WorkerConfig{URL: ts.URL})
	if err != nil {
		t.Fatal(err)
	}
	running, err := w.GetRunning(context.Background())
	if err != nil {
		t.Fatalf("GetRunning: %v", err)
	}
	if running != nil {
		t.Fatalf("running = %+v, want nil when nothing is active", running)
	}
}

func TestStartStopModelPaths(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(models.APIResponse{Success: true})
	}))
	defer ts.Close()

	w, err := NewWorker(config.WorkerConfig{URL: ts.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := w.StartModel(ctx, "alpha"); err != nil {
		t.Fatalf("StartModel: %v", err)
	}
	if err := w.StopModel(ctx, "alpha"); err != nil {
		t.Fatalf("StopModel: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"POST /api/v1/models/alpha/start", "DELETE /api/v1/models/alpha/stop"}
	if len(seen) != 2 || seen[0] != want[0] || seen[1] != want[1] {
		t.Fatalf("worker saw %v, want %v", seen, want)
	}
}

func TestWorkerErrorCarriesStatusAndMessage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.APIResponse{Success: false, Message: "model file not found: /m.gguf"})
	}))
	defer ts.Close()

	w, err := NewWorker(config.WorkerConfig{URL: ts.URL})
	if err != nil {
		t.Fatal(err)
	}
	err = w.StartModel(context.Background(), "alpha")
	if err == nil {
		t.Fatal("StartModel should fail when the worker answers 400")
	}
	we, ok := err.(*WorkerError)
	if !ok {
		t.Fatalf("error type = %T, want *WorkerError", err)
	}
	if we.StatusCode != http.StatusBadRequest || !strings.Contains(we.Message, "model file not found") {
		t.Fatalf("WorkerError = %+v", we)
	}
}

func TestWorkerTransportErrorIsNotWorkerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := ts.URL
	ts.Close() // nothing listens there anymore → connection refused

	w, err := NewWorker(config.WorkerConfig{URL: url})
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.ListModels(context.Background())
	if err == nil {
		t.Fatal("expected a transport error")
	}
	if _, ok := err.(*WorkerError); ok {
		t.Fatalf("transport failures must not be *WorkerError, got %T", err)
	}
}

func TestRoutablePort(t *testing.T) {
	cases := []struct {
		name string
		cfg  models.ModelConfig
		port int
		ok   bool
	}{
		{"configured port", models.ModelConfig{Port: intPtr(9000)}, 9000, true},
		{"default port", models.ModelConfig{}, DefaultLlamaPort, true},
		{"launch with --port", models.ModelConfig{LaunchCmd: strPtr("llama-server -m x --port 8082")}, 8082, true},
		{"launch with --port=", models.ModelConfig{LaunchCmd: strPtr("llama-server --port=8083")}, 8083, true},
		{"launch without port", models.ModelConfig{LaunchCmd: strPtr("llama-server -m x")}, 0, false},
		{"launch out of range", models.ModelConfig{LaunchCmd: strPtr("--port 70000")}, 0, false},
		{"launch first match wins", models.ModelConfig{LaunchCmd: strPtr("--port 1 --port 2")}, 1, true},
		{"launch similar flag ignored", models.ModelConfig{LaunchCmd: strPtr("--ports 5 --port 6")}, 6, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			port, ok := RoutablePort(tc.cfg)
			if port != tc.port || ok != tc.ok {
				t.Fatalf("RoutablePort = (%d, %v), want (%d, %v)", port, ok, tc.port, tc.ok)
			}
		})
	}
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }

func TestRegistrySkipsInvalidEntriesAndWarns(t *testing.T) {
	var warnings []string
	reg := NewRegistry([]config.WorkerConfig{
		{URL: "not a url"},
		{URL: "http://w1.local:8080"},
	}, func(format string, args ...interface{}) {
		warnings = append(warnings, format)
	})

	if len(reg.Workers()) != 1 || reg.Workers()[0].Name != "w1.local" {
		t.Fatalf("workers = %+v, want exactly w1.local", reg.Workers())
	}
	if len(warnings) == 0 {
		t.Fatal("a warning should be logged for the skipped entry")
	}
}

func TestRegistryDuplicateNamesWarn(t *testing.T) {
	var warnings int
	NewRegistry([]config.WorkerConfig{
		{URL: "http://a.local:8080", Name: "same"},
		{URL: "http://b.local:8080", Name: "same"},
	}, func(format string, args ...interface{}) {
		if strings.Contains(format, "duplicated") {
			warnings++
		}
	})
	if warnings == 0 {
		t.Fatal("duplicate worker names should warn")
	}
}

func TestResolveByNameAndHost(t *testing.T) {
	reg := NewRegistry([]config.WorkerConfig{
		{URL: "http://gpu9.local:8080", Name: "nine"},
	}, nil)

	for _, key := range []string{"nine", "gpu9.local", "gpu9.local:8080"} {
		if _, err := reg.Resolve(key); err != nil {
			t.Fatalf("Resolve(%q): %v", key, err)
		}
	}
	if _, err := reg.Resolve("nope"); err == nil {
		t.Fatal("Resolve of an unknown worker should fail")
	}
}

func TestReloadSwapsWorkersAndClearsCache(t *testing.T) {
	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		writeModels(t, w)
	}))
	defer ts.Close()

	reg := NewRegistry([]config.WorkerConfig{{URL: ts.URL}}, nil)
	reg.cacheTTL = time.Minute // keep entries cached so the reload is what invalidates

	ctx := context.Background()
	reg.Snapshot(ctx, reg.Workers()[0])
	reg.Snapshot(ctx, reg.Workers()[0])
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Fatalf("probe hits = %d, want 1 (cache must serve the second read)", n)
	}

	// Reload with a different worker set must drop the cache and swap workers.
	reg.Reload([]config.WorkerConfig{{URL: ts.URL, Name: "renamed"}})
	workers := reg.Workers()
	if len(workers) != 1 || workers[0].Name != "renamed" {
		t.Fatalf("workers after reload = %+v", workers)
	}
	reg.Snapshot(ctx, workers[0])
	if n := atomic.LoadInt32(&hits); n != 2 {
		t.Fatalf("probe hits after reload = %d, want 2 (cache must be cleared)", n)
	}
}

func TestSnapshotsMarkOfflineWorkers(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeModels(t, w, models.ModelItem{ModelConfig: models.ModelConfig{Name: "busy", Port: intPtr(8081)}, Active: true})
	}))
	defer ts.Close()

	reg := NewRegistry([]config.WorkerConfig{
		{URL: deadURL, Name: "dead"},
		{URL: ts.URL, Name: "alive", APIKey: "k"},
	}, nil)

	statuses := reg.WorkerStatuses(context.Background())
	if len(statuses) != 2 {
		t.Fatalf("statuses = %d, want 2", len(statuses))
	}
	if statuses[0].Status != StatusOffline || statuses[0].Error == "" {
		t.Fatalf("offline status = %+v", statuses[0])
	}
	if statuses[1].Status != StatusOnline || statuses[1].RunningModel != "busy" || !statuses[1].APIKeySet {
		t.Fatalf("online status = %+v", statuses[1])
	}

	snaps := reg.Snapshots(context.Background())
	if snaps[0].FleetMachine().Models == nil {
		t.Fatal("offline fleet machine should expose an empty (non-nil) models list")
	}
	if snaps[1].FleetMachine().Running == nil || snaps[1].FleetMachine().Running.Name != "busy" {
		t.Fatalf("online fleet machine running = %+v", snaps[1].FleetMachine().Running)
	}
}

func TestSnapshotCacheDisabledWithZeroTTL(t *testing.T) {
	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		writeModels(t, w)
	}))
	defer ts.Close()

	reg := NewRegistry([]config.WorkerConfig{{URL: ts.URL}}, nil)
	reg.cacheTTL = 0

	w := reg.Workers()[0]
	reg.Snapshot(context.Background(), w)
	reg.Snapshot(context.Background(), w)
	if n := atomic.LoadInt32(&hits); n != 2 {
		t.Fatalf("probe hits = %d, want 2 with the cache disabled", n)
	}
}

// TestCanceledProbeNotCached checks that a probe aborted by the caller's own
// request context is not cached: the next caller must see a fresh probe, not
// a poisoned "offline" entry.
func TestCanceledProbeNotCached(t *testing.T) {
	var hits int32
	var once sync.Once
	started := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		once.Do(func() { close(started) })
		time.Sleep(150 * time.Millisecond)
		writeModels(t, w)
	}))
	defer ts.Close()

	reg := NewRegistry([]config.WorkerConfig{{URL: ts.URL}}, nil)
	reg.cacheTTL = time.Minute

	w := reg.Workers()[0]

	slowCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	snap := reg.Snapshot(slowCtx, w)
	if snap.Status != StatusOffline {
		t.Fatalf("slow probe should read offline, got %s", snap.Status)
	}

	<-started
	// The canceled probe must not be cached: this fresh probe hits the server.
	fresh := reg.Snapshot(context.Background(), w)
	if fresh.Status != StatusOnline {
		t.Fatalf("fresh probe status = %s, want online", fresh.Status)
	}
	if n := atomic.LoadInt32(&hits); n != 2 {
		t.Fatalf("probe hits = %d, want 2 (canceled probe must not be cached)", n)
	}
}

func TestRegistryConcurrentAccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeModels(t, w)
	}))
	defer ts.Close()

	specs := []config.WorkerConfig{{URL: ts.URL, Name: "fake"}}
	reg := NewRegistry(specs, nil)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_, _ = reg.Resolve("fake")
				_ = reg.Workers()
				_ = reg.WorkerStatuses(context.Background())
				if i%2 == 0 {
					reg.Reload(specs)
				}
			}
		}(i)
	}
	wg.Wait()
}
