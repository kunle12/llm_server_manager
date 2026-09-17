package server

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
