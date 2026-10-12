package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
