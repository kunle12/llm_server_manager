package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"llamamanager/config"
	"llamamanager/fleet"
	"llamamanager/manager"
	"llamamanager/models"

	"github.com/gorilla/mux"
)

func newTestHandler(t *testing.T, initialConfig string) (*Handler, string, *int) {
	t.Helper()
	return newTestHandlerWith(t, initialConfig, nil, map[string]*models.ModelConfig{})
}

func newTestHandlerWith(t *testing.T, initialConfig string, reg *fleet.Registry, configs map[string]*models.ModelConfig) (*Handler, string, *int) {
	t.Helper()

	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "llama-server")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLAMA_SERVER_PATH", fakeBin)

	configPath := filepath.Join(dir, "llm_config.json")
	if err := os.WriteFile(configPath, []byte(initialConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr, err := manager.New(configs, func(string, ...interface{}) {}, false, 0)
	if err != nil {
		t.Fatalf("manager.New: %v", err)
	}

	reloads := 0
	h := New(mgr, reg, func(string, ...interface{}) {}, configPath)
	h.SetConfigSavedCallback(func() error {
		reloads++
		return nil
	})

	return h, configPath, &reloads
}

func TestGetConfigReturnsRawFile(t *testing.T) {
	initial := `{"models":[{"name":"alpha","launch_cmd":"true","threads":1,"temperature":0.7}]}`
	h, configPath, _ := newTestHandler(t, initial)

	rr := httptest.NewRecorder()
	h.GetConfig(rr, httptest.NewRequest("GET", "/api/v1/config", nil))

	if rr.Code != 200 {
		t.Fatalf("GetConfig status = %d, want 200", rr.Code)
	}
	if got := rr.Body.String(); got != initial {
		t.Fatalf("GetConfig body = %q, want the raw file content %q", got, initial)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("config file missing: %v", err)
	}
}

func TestSaveConfigValidatesAndWrites(t *testing.T) {
	initial := `{"models":[{"name":"alpha","launch_cmd":"true","threads":1,"temperature":0.7}]}`
	h, configPath, reloads := newTestHandler(t, initial)

	newCfg := `{"models":[{"name":"beta","launch_cmd":"true","threads":1,"temperature":0.5}]}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/config", strings.NewReader(newCfg))
	h.SaveConfig(rr, req)

	if rr.Code != 200 {
		t.Fatalf("SaveConfig status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if *reloads != 1 {
		t.Fatalf("reload callback ran %d times, want 1", *reloads)
	}
	onDisk, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != newCfg {
		t.Fatalf("config on disk = %q, want %q", onDisk, newCfg)
	}

	// GET now returns the new content.
	rr = httptest.NewRecorder()
	h.GetConfig(rr, httptest.NewRequest("GET", "/api/v1/config", nil))
	if rr.Body.String() != newCfg {
		t.Fatalf("GetConfig after save = %q, want %q", rr.Body.String(), newCfg)
	}
}

func TestSaveConfigRejectsInvalidInput(t *testing.T) {
	initial := `{"models":[{"name":"alpha","launch_cmd":"true","threads":1,"temperature":0.7}]}`

	cases := []struct {
		name   string
		body   string
		inBody string // substring expected in the error message
	}{
		{"invalid json", `{"models":[`, "invalid JSON"},
		{"empty models", `{"models":[]}`, "at least one model"},
		{"missing name", `{"models":[{"model_path":"/m.gguf","threads":1}]}`, "missing a name"},
		{"duplicate name", `{"models":[{"name":"a","launch_cmd":"x"},{"name":"a","launch_cmd":"y"}]}`, "duplicate"},
		{"bad temperature", `{"models":[{"name":"a","model_path":"/m.gguf","threads":1,"temperature":3}]}`, "temperature"},
		{"missing model_path", `{"models":[{"name":"a","threads":1}]}`, "model_path"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, configPath, reloads := newTestHandler(t, initial)

			rr := httptest.NewRecorder()
			req := httptest.NewRequest("PUT", "/api/v1/config", strings.NewReader(tc.body))
			h.SaveConfig(rr, req)

			if rr.Code != 400 {
				t.Fatalf("status = %d, want 400 (body: %s)", rr.Code, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), tc.inBody) {
				t.Fatalf("error message %q does not contain %q", rr.Body.String(), tc.inBody)
			}
			if *reloads != 0 {
				t.Fatal("reload callback must not run for rejected configs")
			}
			onDisk, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(onDisk) != initial {
				t.Fatalf("rejected save must leave the file untouched, got %q", onDisk)
			}
		})
	}
}

func TestSaveConfigAcceptsLargeBody(t *testing.T) {
	h, _, reloads := newTestHandler(t, `{"models":[{"name":"alpha","launch_cmd":"true","threads":1}]}`)

	// A comment field padded well past the 1KB general API limit.
	big := strings.Repeat("x", 64*1024)
	body := fmt.Sprintf(`{"models":[{"name":"alpha","launch_cmd":"true %s","threads":1}]}`, big)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/config", strings.NewReader(body))
	h.SaveConfig(rr, req)

	if rr.Code != 200 {
		t.Fatalf("large valid config rejected: status %d, body %s", rr.Code, rr.Body.String())
	}
	if *reloads != 1 {
		t.Fatalf("reload callback ran %d times, want 1", *reloads)
	}
}

// fakeWorkerServer is a stand-in worker manager API. It records the last
// request and answers from a script of path+method → status/body.
type fakeWorkerServer struct {
	mu         sync.Mutex
	apiKey     string
	url        string
	lastPath   string
	lastMethod string
	lastKey    string
	handler    func(w http.ResponseWriter, r *http.Request)
}

func (f *fakeWorkerServer) record(r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastPath = r.URL.Path
	f.lastMethod = r.Method
	f.lastKey = r.Header.Get("api-key")
}

func (f *fakeWorkerServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.record(r)
	f.mu.Lock()
	handler := f.handler
	f.mu.Unlock()
	if handler != nil {
		handler(w, r)
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

// URL returns the fake worker's base URL.
func (f *fakeWorkerServer) URL() string { return f.url }

func (f *fakeWorkerServer) request() (path, method, key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastPath, f.lastMethod, f.lastKey
}

const fakeWorkerKey = "workerkey123456"

// fleetTestRouter registers the fleet/worker routes through mux, matching the
// server wiring so mux.Vars is populated for the {worker}/{model} segments.
func fleetTestRouter(h *Handler) http.Handler {
	router := mux.NewRouter()
	router.HandleFunc("/api/v1/workers", h.ListWorkers).Methods("GET")
	router.HandleFunc("/api/v1/workers/{worker}/models", h.WorkerListModels).Methods("GET")
	router.HandleFunc("/api/v1/workers/{worker}/models/{model}/start", h.WorkerStartModel).Methods("POST")
	router.HandleFunc("/api/v1/workers/{worker}/models/{model}/stop", h.WorkerStopModel).Methods("DELETE")
	router.HandleFunc("/api/v1/fleet", h.GetFleet).Methods("GET")
	return router
}

func newFakeWorker(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) (*fakeWorkerServer, *fleet.Registry) {
	t.Helper()
	fake := &fakeWorkerServer{apiKey: fakeWorkerKey, handler: handler}
	ts := httptest.NewServer(fake)
	t.Cleanup(ts.Close)
	fake.url = ts.URL

	reg := fleet.NewRegistry([]config.WorkerConfig{
		{URL: ts.URL, Name: "fake", APIKey: fakeWorkerKey},
	}, nil)
	return fake, reg
}

func writeJSON(t *testing.T, w http.ResponseWriter, code int, v interface{}) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("fake worker write: %v", err)
	}
}

func TestListWorkersEmptyWhenStandalone(t *testing.T) {
	h, _, _ := newTestHandler(t, `{"models":[{"name":"alpha","launch_cmd":"true","threads":1}]}`)

	rr := httptest.NewRecorder()
	fleetTestRouter(h).ServeHTTP(rr, httptest.NewRequest("GET", "/api/v1/workers", nil))

	if rr.Code != 200 {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Data struct {
			Workers []fleet.WorkerStatus `json:"workers"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v (%s)", err, rr.Body.String())
	}
	if len(payload.Data.Workers) != 0 {
		t.Fatalf("workers = %v, want empty", payload.Data.Workers)
	}
}

func TestListWorkersOnlineAndOffline(t *testing.T) {
	fake, reg := newFakeWorker(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/models" {
			writeJSON(t, w, 200, models.APIResponse{Success: true, Data: models.ModelListResponse{
				Models: []models.ModelItem{
					{ModelConfig: models.ModelConfig{Name: "big", Alias: "gpt-x", Port: intPtr(8099)}, Active: true},
					{ModelConfig: models.ModelConfig{Name: "small"}, Active: false},
				},
			}})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	// Second worker: unreachable (server closed immediately). The fake stays
	// in the set under its real URL.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()
	reg.Reload([]config.WorkerConfig{
		{URL: deadURL, Name: "dead", APIKey: fakeWorkerKey},
		{URL: fake.URL(), Name: "fake", APIKey: fakeWorkerKey},
	})

	h, _, _ := newTestHandlerWith(t, `{"models":[{"name":"alpha","launch_cmd":"true","threads":1}]}`, reg, nil)

	rr := httptest.NewRecorder()
	fleetTestRouter(h).ServeHTTP(rr, httptest.NewRequest("GET", "/api/v1/workers", nil))

	var payload struct {
		Data struct {
			Workers []fleet.WorkerStatus `json:"workers"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v (%s)", err, rr.Body.String())
	}
	if len(payload.Data.Workers) != 2 {
		t.Fatalf("workers = %d, want 2", len(payload.Data.Workers))
	}

	// Order follows the config: dead first, then fake.
	deadStatus := payload.Data.Workers[0]
	if deadStatus.Name != "dead" || deadStatus.Status != fleet.StatusOffline || deadStatus.Error == "" {
		t.Fatalf("dead worker status = %+v", deadStatus)
	}

	fakeStatus := payload.Data.Workers[1]
	if fakeStatus.Name != "fake" || fakeStatus.Status != fleet.StatusOnline {
		t.Fatalf("fake worker status = %+v", fakeStatus)
	}
	if fakeStatus.RunningModel != "big" || fakeStatus.ModelCount != 2 {
		t.Fatalf("fake worker view = %+v, want running=big count=2", fakeStatus)
	}
	if !fakeStatus.APIKeySet {
		t.Fatal("api_key_set should be true when a key is configured")
	}
}

func TestGetFleetLocalFirst(t *testing.T) {
	_, reg := newFakeWorker(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 200, models.APIResponse{Success: true, Data: models.ModelListResponse{
			Models: []models.ModelItem{
				{ModelConfig: models.ModelConfig{Name: "worker-model", Port: intPtr(8099)}, Active: true},
			},
		}})
	})

	configs := map[string]*models.ModelConfig{
		"alpha": {Name: "alpha", Alias: "gpt-local", Port: intPtr(8098), Threads: 2, ModelPath: "/m.gguf"},
	}
	h, _, _ := newTestHandlerWith(t, `{"models":[{"name":"alpha","model_path":"/m.gguf","threads":2,"port":8098}]}`, reg, configs)

	rr := httptest.NewRecorder()
	fleetTestRouter(h).ServeHTTP(rr, httptest.NewRequest("GET", "/api/v1/fleet", nil))

	if rr.Code != 200 {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Data struct {
			Machines []fleet.FleetMachine `json:"machines"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	machines := payload.Data.Machines
	if len(machines) != 2 {
		t.Fatalf("machines = %d, want 2 (local + worker)", len(machines))
	}

	local := machines[0]
	if local.Machine != fleet.LocalMachine || local.Status != fleet.StatusOnline {
		t.Fatalf("local machine entry = %+v", local)
	}
	if local.Running != nil {
		t.Fatalf("local machine should report nothing running, got %+v", local.Running)
	}
	found := false
	for _, m := range local.Models {
		if m.Name == "alpha" {
			found = true
			if m.Alias != "gpt-local" || m.Port == nil || *m.Port != 8098 {
				t.Fatalf("local model entry = %+v", m)
			}
		}
	}
	if !found {
		t.Fatalf("local models missing alpha: %+v", local.Models)
	}

	worker := machines[1]
	if worker.Machine != "fake" || worker.Status != fleet.StatusOnline {
		t.Fatalf("worker machine entry = %+v", worker)
	}
	if worker.Running == nil || worker.Running.Name != "worker-model" || worker.Running.Port != 8099 {
		t.Fatalf("worker running = %+v", worker.Running)
	}
}

func TestWorkerStartStopPassesThrough(t *testing.T) {
	// The fake asserts the exact path, method, and api-key the coordinator
	// must send, then answers per scenario.
	fake, reg := newFakeWorker(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("api-key") != fakeWorkerKey {
			writeJSON(t, w, 401, models.APIResponse{Success: false, Message: "invalid api key"})
			return
		}
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/v1/models/alpha/start":
			writeJSON(t, w, 200, models.APIResponse{Success: true, Message: "model 'alpha' starting"})
		case r.Method == "DELETE" && r.URL.Path == "/api/v1/models/alpha/stop":
			writeJSON(t, w, 200, models.APIResponse{Success: true, Message: "stopped"})
		default:
			writeJSON(t, w, 400, models.APIResponse{Success: false, Message: "model file not found: /m.gguf"})
		}
	})

	h, _, _ := newTestHandlerWith(t, `{"models":[{"name":"alpha","launch_cmd":"true","threads":1}]}`, reg, nil)

	rr := httptest.NewRecorder()
	fleetTestRouter(h).ServeHTTP(rr, httptest.NewRequest("POST", "/api/v1/workers/fake/models/alpha/start", nil))
	if rr.Code != 200 {
		t.Fatalf("start status = %d, body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "starting on worker 'fake'") {
		t.Fatalf("start body = %s", rr.Body.String())
	}

	if path, method, key := fake.request(); path != "/api/v1/models/alpha/start" || method != "POST" || key != fakeWorkerKey {
		t.Fatalf("fake saw %s %s (api-key %q) for start", method, path, key)
	}

	rr = httptest.NewRecorder()
	fleetTestRouter(h).ServeHTTP(rr, httptest.NewRequest("DELETE", "/api/v1/workers/fake/models/alpha/stop", nil))
	if rr.Code != 200 {
		t.Fatalf("stop status = %d, body %s", rr.Code, rr.Body.String())
	}

	// The fake must have seen the worker-style routes with the worker's key.
	path, method, key := fake.request()
	if path != "/api/v1/models/alpha/stop" || method != "DELETE" || key != fakeWorkerKey {
		t.Fatalf("fake saw %s %s (api-key %q), want DELETE stop with the worker key", method, path, key)
	}
}

func TestWorkerStartPropagatesWorkerErrors(t *testing.T) {
	_, reg := newFakeWorker(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 400, models.APIResponse{Success: false, Message: "model file not found: /m.gguf"})
	})
	h, _, _ := newTestHandlerWith(t, `{"models":[{"name":"alpha","launch_cmd":"true","threads":1}]}`, reg, nil)

	rr := httptest.NewRecorder()
	fleetTestRouter(h).ServeHTTP(rr, httptest.NewRequest("POST", "/api/v1/workers/fake/models/alpha/start", nil))

	if rr.Code != 400 {
		t.Fatalf("status = %d, want the worker's 400 passed through (body %s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "model file not found") {
		t.Fatalf("body = %s, want the worker's message", rr.Body.String())
	}
}

func TestWorkerTransportErrorIs502(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	reg := fleet.NewRegistry([]config.WorkerConfig{{URL: deadURL, Name: "dead"}}, nil)
	h, _, _ := newTestHandlerWith(t, `{"models":[{"name":"alpha","launch_cmd":"true","threads":1}]}`, reg, nil)

	rr := httptest.NewRecorder()
	fleetTestRouter(h).ServeHTTP(rr, httptest.NewRequest("POST", "/api/v1/workers/dead/models/alpha/start", nil))

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body %s)", rr.Code, rr.Body.String())
	}
}

func TestWorkerUnknownWorkerIs404(t *testing.T) {
	h, _, _ := newTestHandler(t, `{"models":[{"name":"alpha","launch_cmd":"true","threads":1}]}`)

	rr := httptest.NewRecorder()
	fleetTestRouter(h).ServeHTTP(rr, httptest.NewRequest("POST", "/api/v1/workers/nope/models/alpha/start", nil))

	if rr.Code != 404 {
		t.Fatalf("status = %d, want 404 (body %s)", rr.Code, rr.Body.String())
	}
}

func TestWorkerResolveByHost(t *testing.T) {
	_, reg := newFakeWorker(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 200, models.APIResponse{Success: true, Data: models.ModelListResponse{
			Models: []models.ModelItem{},
		}})
	})
	// The registry names the worker after the fake server's host; resolve by
	// host must find the same worker.
	host := strings.TrimPrefix(reg.Workers()[0].URL, "http://")

	h, _, _ := newTestHandlerWith(t, `{"models":[{"name":"alpha","launch_cmd":"true","threads":1}]}`, reg, nil)

	rr := httptest.NewRecorder()
	fleetTestRouter(h).ServeHTTP(rr, httptest.NewRequest("GET", "/api/v1/workers/"+host+"/models", nil))

	if rr.Code != 200 {
		t.Fatalf("resolve by host failed: status %d, body %s", rr.Code, rr.Body.String())
	}
}

func intPtr(i int) *int { return &i }
