package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"llamamanager/models"
)

// TestConfigEndpointBodyLimit checks that the config endpoint accepts bodies
// well past the general 1KB API limit (a real config file exceeds 1KB), while
// other endpoints still enforce it.
func TestConfigEndpointBodyLimit(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "llama-server")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLAMA_SERVER_PATH", fakeBin)

	configPath := filepath.Join(dir, "llm_config.json")
	initial := `{"models":[{"name":"alpha","launch_cmd":"true","threads":1,"temperature":0.7}]}`
	if err := os.WriteFile(configPath, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	app, err := New(configPath, false, 6)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// A valid config padded past 1KB must be accepted on PUT /api/v1/config.
	big := strings.Repeat("x", 8192)
	bigCfg := fmt.Sprintf(`{"models":[{"name":"alpha","launch_cmd":"true %s","threads":1}]}`, big)
	req := httptest.NewRequest("PUT", "/api/v1/config", strings.NewReader(bigCfg))
	rr := httptest.NewRecorder()
	app.router.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("PUT /config with %d-byte body: status %d, body %s", len(bigCfg), rr.Code, rr.Body.String())
	}
	onDisk, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != bigCfg {
		t.Fatal("config file was not updated with the large body")
	}

	// Any other endpoint still enforces the 1KB limit.
	oversized := strings.Repeat("A", 4096)
	req = httptest.NewRequest("POST", "/api/v1/models/alpha/start", strings.NewReader(oversized))
	rr = httptest.NewRecorder()
	app.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized non-config body: status %d, want %d", rr.Code, http.StatusRequestEntityTooLarge)
	}
}

// TestWatchConfigReloads checks that the config file is reloaded for every
// common save style. Watching the file itself fails for the "replace" style
// (used by vim by default), because the original file is renamed to a backup
// and a new file is created in its place, which replaces the inode and
// invalidates a watch placed on the file.
func TestWatchConfigReloads(t *testing.T) {
	saveStyles := map[string]func(t *testing.T, path, content string){
		// vim's default: rename the original to a backup, then create a new file
		"replace": func(t *testing.T, path, content string) {
			if err := os.Rename(path, path+"~"); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			os.Remove(path + "~")
		},
		// write a temp file, then rename it over the config
		"atomic": func(t *testing.T, path, content string) {
			tmp := path + ".tmp"
			if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(tmp, path); err != nil {
				t.Fatal(err)
			}
		},
		// truncate and rewrite in place
		"inplace": func(t *testing.T, path, content string) {
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	}

	for name, save := range saveStyles {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			fakeBin := filepath.Join(dir, "llama-server")
			if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("LLAMA_SERVER_PATH", fakeBin)

			configPath := filepath.Join(dir, "llm_config.json")
			configJSON := func(model string) string {
				return fmt.Sprintf(`{"models":[{"name":%q,"launch_cmd":"true","threads":1,"temperature":0.7}]}`, model)
			}
			if err := os.WriteFile(configPath, []byte(configJSON("alpha")), 0o644); err != nil {
				t.Fatal(err)
			}

			app, err := New(configPath, false, 6)
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			app.WatchConfig()
			t.Cleanup(func() { app.Shutdown() })

			// Read the model count concurrently with the reload so that
			// `go test -race` verifies the access is synchronised.
			stopReading := make(chan struct{})
			defer close(stopReading)
			go func() {
				for {
					select {
					case <-stopReading:
						return
					default:
						_ = app.GetModelCount()
					}
				}
			}()

			save(t, configPath, configJSON("beta"))

			deadline := time.Now().Add(6 * time.Second)
			for time.Now().Before(deadline) {
				if _, ok := app.mgr.ListModels()["beta"]; ok {
					return
				}
				time.Sleep(50 * time.Millisecond)
			}
			t.Fatalf("config was not reloaded after %q save", name)
		})
	}
}

// TestReloadConfigUpdatesWorkers checks that a config reload swaps the
// worker registry atomically: the coordinator sees the new worker set without
// a process restart.
func TestReloadConfigUpdatesWorkers(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "llama-server")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLAMA_SERVER_PATH", fakeBin)

	configPath := filepath.Join(dir, "llm_config.json")
	modelsJSON := `{"models":[{"name":"alpha","launch_cmd":"true","threads":1,"temperature":0.7}]`
	if err := os.WriteFile(configPath, []byte(modelsJSON+`,"workers":["http://w1.local:8080"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	app, err := New(configPath, false, 6)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	workers := app.fleet.Workers()
	if len(workers) != 1 || workers[0].Name != "w1.local" {
		t.Fatalf("initial workers = %+v", workers)
	}

	updated := modelsJSON + `,"workers":[{"url":"http://w2.local:8080","name":"second"}]}`
	if err := os.WriteFile(configPath, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := app.reloadConfig(); err != nil {
		t.Fatalf("reloadConfig: %v", err)
	}

	workers = app.fleet.Workers()
	if len(workers) != 1 || workers[0].Name != "second" || workers[0].URL != "http://w2.local:8080" {
		t.Fatalf("workers after reload = %+v", workers)
	}
}

// TestFleetEndpointsStandalone checks that the fleet endpoints are registered
// even without workers configured: /fleet answers with just the local
// machine, /workers with an empty list.
func TestFleetEndpointsStandalone(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "llama-server")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLAMA_SERVER_PATH", fakeBin)

	configPath := filepath.Join(dir, "llm_config.json")
	if err := os.WriteFile(configPath, []byte(`{"models":[{"name":"alpha","launch_cmd":"true","threads":1,"temperature":0.7}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	app, err := New(configPath, false, 6)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rr := httptest.NewRecorder()
	app.router.ServeHTTP(rr, httptest.NewRequest("GET", "/api/v1/workers", nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"workers":[]`) {
		t.Fatalf("GET /workers = %d, body %s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	app.router.ServeHTTP(rr, httptest.NewRequest("GET", "/api/v1/fleet", nil))
	if rr.Code != 200 {
		t.Fatalf("GET /fleet status = %d, body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"machine":"local"`) {
		t.Fatalf("GET /fleet body missing the local machine: %s", rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), `"machine":"`) && strings.Count(rr.Body.String(), `"machine":"`) != 1 {
		t.Fatalf("standalone /fleet should contain exactly one machine: %s", rr.Body.String())
	}
}

// TestProxyWiredWhenEnabled checks the full middleware chain with the proxy
// on: /v1 routes answer, large prompt bodies are accepted past the 1KB API
// limit, and unknown /v1 endpoints get OpenAI-format errors.
func TestProxyWiredWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "llama-server")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLAMA_SERVER_PATH", fakeBin)

	configPath := filepath.Join(dir, "llm_config.json")
	if err := os.WriteFile(configPath, []byte(`{"models":[{"name":"alpha","launch_cmd":"true","threads":1}],"proxy":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	app, err := New(configPath, false, 6)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rr := httptest.NewRecorder()
	app.router.ServeHTTP(rr, httptest.NewRequest("GET", "/v1/models", nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"object":"list"`) {
		t.Fatalf("GET /v1/models = %d, body %s", rr.Code, rr.Body.String())
	}

	// A 2KB prompt body must pass the general 1KB limit; "alpha" is a
	// launch_cmd model without --port, so it is configured but not routable.
	big := strings.Repeat("x", 2048)
	body := fmt.Sprintf(`{"model":"alpha","messages":[{"role":"user","content":%q}]}`, big)
	rr = httptest.NewRecorder()
	app.router.ServeHTTP(rr, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body)))
	if rr.Code != 404 || !strings.Contains(rr.Body.String(), "model_not_available") {
		t.Fatalf("POST /v1/chat/completions = %d, body %s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	app.router.ServeHTTP(rr, httptest.NewRequest("GET", "/v1/nope", nil))
	if rr.Code != 404 || !strings.Contains(rr.Body.String(), "not_found") {
		t.Fatalf("GET /v1/nope = %d, body %s", rr.Code, rr.Body.String())
	}
}

// TestProxyDisabledByDefault checks the standalone invariant: without the
// proxy flag, /v1 paths are plain 404s as before.
func TestProxyDisabledByDefault(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "llama-server")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLAMA_SERVER_PATH", fakeBin)

	configPath := filepath.Join(dir, "llm_config.json")
	if err := os.WriteFile(configPath, []byte(`{"models":[{"name":"alpha","launch_cmd":"true","threads":1}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	app, err := New(configPath, false, 6)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rr := httptest.NewRecorder()
	app.router.ServeHTTP(rr, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"alpha"}`)))
	if rr.Code != 404 {
		t.Fatalf("POST /v1/chat/completions with proxy off = %d, want 404", rr.Code)
	}
}

// TestProxyBearerAuthServerLevel checks auth across both planes: /v1 accepts
// the coordinator key as Bearer, while /api/v1 keeps requiring the api-key
// header exactly as before.
func TestProxyBearerAuthServerLevel(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "llama-server")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLAMA_SERVER_PATH", fakeBin)
	t.Setenv("LLM_MANAGER_API_KEY", "coordkey12345678")

	configPath := filepath.Join(dir, "llm_config.json")
	if err := os.WriteFile(configPath, []byte(`{"models":[{"name":"alpha","launch_cmd":"true","threads":1}],"proxy":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	app, err := New(configPath, false, 6)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// /v1 without auth → 401 OpenAI-format error.
	rr := httptest.NewRecorder()
	app.router.ServeHTTP(rr, httptest.NewRequest("GET", "/v1/models", nil))
	if rr.Code != 401 || !strings.Contains(rr.Body.String(), "invalid_api_key") {
		t.Fatalf("GET /v1/models unauthenticated = %d, body %s", rr.Code, rr.Body.String())
	}

	// /v1 with Bearer → 200.
	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer coordkey12345678")
	rr = httptest.NewRecorder()
	app.router.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("GET /v1/models with Bearer = %d, body %s", rr.Code, rr.Body.String())
	}

	// The control plane keeps its own contract: Bearer alone is not enough.
	req = httptest.NewRequest("GET", "/api/v1/models", nil)
	req.Header.Set("Authorization", "Bearer coordkey12345678")
	rr = httptest.NewRecorder()
	app.router.ServeHTTP(rr, req)
	if rr.Code != 401 {
		t.Fatalf("GET /api/v1/models with Bearer only = %d, want 401 (control-plane auth unchanged)", rr.Code)
	}
}

// TestDuplicateAliasWarns checks the load-time warning for two models
// declaring the same alias (resolves by first-match; not a rejection).
func TestDuplicateAliasWarns(t *testing.T) {
	list := []models.ModelConfig{
		{Name: "a", Alias: "shared"},
		{Name: "b", Alias: "shared"},
		{Name: "c", Alias: "unique"},
	}
	var warnings []string
	indexModels(list, func(format string, args ...interface{}) {
		warnings = append(warnings, fmt.Sprintf(format, args...))
	})

	if len(warnings) != 1 || !strings.Contains(warnings[0], `"a" and "b"`) {
		t.Fatalf("warnings = %v, want exactly one duplicate-alias warning mentioning a and b", warnings)
	}
}

// TestAliasSurvivesReload checks that aliases are preserved across the reload
// path identically to the load path.
func TestAliasSurvivesReload(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "llama-server")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLAMA_SERVER_PATH", fakeBin)

	configPath := filepath.Join(dir, "llm_config.json")
	if err := os.WriteFile(configPath, []byte(`{"models":[{"name":"alpha","alias":"gpt-x","launch_cmd":"true","threads":1}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	app, err := New(configPath, false, 6)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := app.mgr.ListModels()["alpha"].Alias; got != "gpt-x" {
		t.Fatalf("alias after load = %q", got)
	}

	if err := os.WriteFile(configPath, []byte(`{"models":[{"name":"beta","alias":"gpt-y","launch_cmd":"true","threads":1}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := app.reloadConfig(); err != nil {
		t.Fatalf("reloadConfig: %v", err)
	}
	if got := app.mgr.ListModels()["beta"].Alias; got != "gpt-y" {
		t.Fatalf("alias after reload = %q", got)
	}
}

// TestEmptyNamedModelsAreSkipped checks that the load and reload paths agree: a
// model without a name cannot be addressed through the API, so it is dropped
// rather than indexed under the empty key.
func TestEmptyNamedModelsAreSkipped(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "llama-server")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLAMA_SERVER_PATH", fakeBin)

	configPath := filepath.Join(dir, "llm_config.json")
	nameless := `{"launch_cmd":"true","threads":1,"temperature":0.7}`
	writeConfig := func(named string) {
		content := fmt.Sprintf(`{"models":[{"name":%q,"launch_cmd":"true","threads":1,"temperature":0.7},%s]}`, named, nameless)
		if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeConfig("alpha")

	app, err := New(configPath, false, 6)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	check := func(want string) {
		t.Helper()
		if got := app.GetModelCount(); got != 1 {
			t.Fatalf("GetModelCount() = %d, want 1 (nameless model must be skipped)", got)
		}
		models := app.mgr.ListModels()
		if _, ok := models[""]; ok {
			t.Fatal("nameless model must not be indexed under the empty name")
		}
		if _, ok := models[want]; !ok || len(models) != 1 {
			t.Fatalf("ListModels() = %v, want exactly %q", models, want)
		}
	}

	check("alpha")

	writeConfig("beta")
	if err := app.reloadConfig(); err != nil {
		t.Fatalf("reloadConfig: %v", err)
	}
	check("beta")
}

// TestCORSPreflight checks that an OPTIONS preflight for an API route is
// answered with the CORS allow headers rather than mux's 405. Cross-origin
// clients (a UI served from another origin, e.g. localhost vs 127.0.0.1, or a
// separate dev server) depend on this to reach the API at all.
func TestCORSPreflight(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "llama-server")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLAMA_SERVER_PATH", fakeBin)

	configPath := filepath.Join(dir, "llm_config.json")
	if err := os.WriteFile(configPath, []byte(`{"models":[{"name":"alpha","launch_cmd":"true","threads":1}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	app, err := New(configPath, false, 6)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	const origin = "http://other.local:5173"
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/fleet", nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", "GET")
	req.Header.Set("Access-Control-Request-Headers", "api-key")
	rr := httptest.NewRecorder()
	app.handler.CORS(app.router).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("preflight status = %d, want 200 (body %s)", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != origin {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, origin)
	}
	if !strings.Contains(rr.Header().Get("Access-Control-Allow-Headers"), "api-key") {
		t.Fatalf("Access-Control-Allow-Headers = %q, want api-key allowed", rr.Header().Get("Access-Control-Allow-Headers"))
	}
}

// TestProxyTogglesLiveWithoutRestart checks that flipping "proxy" in the config
// takes effect on reload (the /v1 routes are gated at request time) and that
// GET /api/v1/proxy reports the runtime state the UI shows.
func TestProxyTogglesLiveWithoutRestart(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "llama-server")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLAMA_SERVER_PATH", fakeBin)

	configPath := filepath.Join(dir, "llm_config.json")
	write := func(proxy bool) {
		body := fmt.Sprintf(`{"models":[{"name":"alpha","launch_cmd":"true","threads":1}],"proxy":%t}`, proxy)
		if err := os.WriteFile(configPath, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(false)

	app, err := New(configPath, false, 6)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	status := func(path string) int {
		rr := httptest.NewRecorder()
		app.router.ServeHTTP(rr, httptest.NewRequest("GET", path, nil))
		return rr.Code
	}
	proxyEnabled := func() bool {
		rr := httptest.NewRecorder()
		app.router.ServeHTTP(rr, httptest.NewRequest("GET", "/api/v1/proxy", nil))
		if rr.Code != 200 {
			t.Fatalf("GET /api/v1/proxy = %d, body %s", rr.Code, rr.Body.String())
		}
		return strings.Contains(rr.Body.String(), `"enabled":true`)
	}

	if got := status("/v1/models"); got != 404 {
		t.Fatalf("proxy off: /v1/models = %d, want 404", got)
	}
	if proxyEnabled() {
		t.Fatal("proxy status should be false when config proxy is off")
	}

	// Enable via config reload — no restart.
	write(true)
	if err := app.reloadConfig(); err != nil {
		t.Fatalf("reloadConfig: %v", err)
	}
	if got := status("/v1/models"); got != 200 {
		t.Fatalf("proxy on: /v1/models = %d, want 200", got)
	}
	if !proxyEnabled() {
		t.Fatal("proxy status should be true after enabling via reload")
	}

	// Disable again.
	write(false)
	if err := app.reloadConfig(); err != nil {
		t.Fatalf("reloadConfig: %v", err)
	}
	if got := status("/v1/models"); got != 404 {
		t.Fatalf("proxy off again: /v1/models = %d, want 404", got)
	}
	if proxyEnabled() {
		t.Fatal("proxy status should be false after disabling via reload")
	}
}

// TestSystemEndpointWiring checks that GET /api/v1/system is registered behind
// the API router and returns the telemetry envelope with no-store caching.
func TestSystemEndpointWiring(t *testing.T) {
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "llama-server")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLAMA_SERVER_PATH", fakeBin)

	configPath := filepath.Join(dir, "llm_config.json")
	if err := os.WriteFile(configPath, []byte(`{"models":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	app, err := New(configPath, false, 6)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rr := httptest.NewRecorder()
	app.router.ServeHTTP(rr, httptest.NewRequest("GET", "/api/v1/system", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}

	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			CPU struct {
				Cores int `json:"cores"`
			} `json:"cpu"`
			Memory struct {
				TotalBytes uint64 `json:"total_bytes"`
			} `json:"memory"`
			GPU struct {
				Available bool `json:"available"`
			} `json:"gpu"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Success || payload.Data.CPU.Cores <= 0 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}
