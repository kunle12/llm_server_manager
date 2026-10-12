package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "llm_config.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadParsesWorkersProxyAlias(t *testing.T) {
	path := writeTempConfig(t, `{
		"models": [
			{"name": "m1", "alias": "gpt-4o", "model_path": "/m.gguf", "threads": 2, "temperature": 0.7},
			{"name": "raw", "launch_cmd": "llama-server --port 8099"}
		],
		"workers": [
			"http://gpu1.local:8080",
			{"url": "http://gpu2.local:8080", "name": "gpu2", "api_key": "key123"}
		],
		"proxy": true
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Proxy != true {
		t.Fatalf("Proxy = %v, want true", cfg.Proxy)
	}
	if len(cfg.Workers) != 2 {
		t.Fatalf("Workers = %d, want 2", len(cfg.Workers))
	}
	if cfg.Workers[0].URL != "http://gpu1.local:8080" || cfg.Workers[0].Name != "" {
		t.Fatalf("string worker entry = %+v", cfg.Workers[0])
	}
	if cfg.Workers[1].URL != "http://gpu2.local:8080" || cfg.Workers[1].Name != "gpu2" || cfg.Workers[1].APIKey != "key123" {
		t.Fatalf("object worker entry = %+v", cfg.Workers[1])
	}
	if len(cfg.Models) != 2 {
		t.Fatalf("Models = %d, want 2", len(cfg.Models))
	}
	if cfg.Models[0].Alias != "gpt-4o" {
		t.Fatalf("alias = %q, want gpt-4o", cfg.Models[0].Alias)
	}
	if cfg.Models[1].Alias != "" {
		t.Fatalf("unset alias should stay empty, got %q", cfg.Models[1].Alias)
	}
}

func TestProxyToleratedForms(t *testing.T) {
	cases := []struct {
		name  string
		proxy string
		want  bool
	}{
		{"bool true", `true`, true},
		{"bool false", `false`, false},
		{"string true", `"true"`, true},
		{"object enabled false", `{"enabled": false}`, false},
		{"object enabled true", `{"enabled": true}`, true},
		{"object without enabled", `{"autostart": true}`, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTempConfig(t, `{
				"models": [{"name": "m", "model_path": "/m.gguf", "threads": 1}],
				"proxy": `+tc.proxy+`
			}`)
			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if bool(cfg.Proxy) != tc.want {
				t.Fatalf("Proxy = %v, want %v", cfg.Proxy, tc.want)
			}
		})
	}
}

func TestProxyInvalidValueRejected(t *testing.T) {
	path := writeTempConfig(t, `{
		"models": [{"name": "m", "model_path": "/m.gguf", "threads": 1}],
		"proxy": "banana"
	}`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load should reject a non-boolean proxy string")
	}
}

// TestJSONPathToleratesAlternateForms covers the encoding/json decode path
// used by PUT /api/v1/config, which must agree with the viper path. Unlike
// the viper path, it can also honor the empty-object form (viper drops empty
// map values, see the note in config.go).
func TestJSONPathToleratesAlternateForms(t *testing.T) {
	body := `{
		"models": [{"name": "m", "model_path": "/m.gguf", "threads": 1}],
		"workers": ["http://w1.local:8080", {"url": "http://w2.local:8080", "name": "w2"}],
		"proxy": {"enabled": false}
	}`
	var cfg Config
	if err := json.Unmarshal([]byte(body), &cfg); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if len(cfg.Workers) != 2 || cfg.Workers[1].Name != "w2" {
		t.Fatalf("workers = %+v", cfg.Workers)
	}
	if cfg.Proxy != false {
		t.Fatalf("Proxy = %v, want false from the object form", cfg.Proxy)
	}

	// Marshal writes back the documented boolean form.
	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"proxy":false`) {
		t.Fatalf("marshalled config = %s, want it to contain \"proxy\":false", out)
	}

	// The empty-object form means enabled on this path.
	var enabled Config
	if err := json.Unmarshal([]byte(`{"proxy":{}}`), &enabled); err != nil {
		t.Fatal(err)
	}
	if enabled.Proxy != true {
		t.Fatalf("empty-object proxy = %v, want true", enabled.Proxy)
	}
}
