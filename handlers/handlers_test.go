package handlers

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"llamamanager/manager"
	"llamamanager/models"
)

func newTestHandler(t *testing.T, initialConfig string) (*Handler, string, *int) {
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

	mgr, err := manager.New(map[string]*models.ModelConfig{}, func(string, ...interface{}) {}, false, 0)
	if err != nil {
		t.Fatalf("manager.New: %v", err)
	}

	reloads := 0
	h := New(mgr, func(string, ...interface{}) {}, configPath)
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
