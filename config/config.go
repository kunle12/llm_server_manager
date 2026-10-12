package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/mitchellh/mapstructure"
	"github.com/spf13/viper"
	"llamamanager/models"
)

// WorkerConfig is one entry of the coordinator's "workers" list: a remote
// llm_server_manager instance. Two forms are accepted — a plain URL string
// or a full object — on both the viper path (Load/Reload, via flexHook) and
// the encoding/json path (SaveConfig, via UnmarshalJSON).
type WorkerConfig struct {
	URL    string `json:"url" mapstructure:"url"`
	Name   string `json:"name,omitempty" mapstructure:"name,omitempty"`
	APIKey string `json:"api_key,omitempty" mapstructure:"api_key,omitempty"`
}

// UnmarshalJSON accepts a plain URL string ("http://host:8080") or an object
// {"url": ..., "name"?: ..., "api_key"?: ...}.
func (w *WorkerConfig) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var rawURL string
		if err := json.Unmarshal(trimmed, &rawURL); err != nil {
			return err
		}
		w.URL = strings.TrimSpace(rawURL)
		return nil
	}

	type plain WorkerConfig // avoid recursing into UnmarshalJSON
	var p plain
	if err := json.Unmarshal(trimmed, &p); err != nil {
		return err
	}
	*w = WorkerConfig(p)
	return nil
}

// ProxySetting is the "proxy" config key. The documented form is a plain
// boolean; an object form is tolerated for forward compatibility, though v1
// defines no object keys — any object means enabled, honoring an explicit
// {"enabled": bool} when present.
type ProxySetting bool

// MarshalJSON always writes the documented boolean form.
func (p ProxySetting) MarshalJSON() ([]byte, error) { return json.Marshal(bool(p)) }

// UnmarshalJSON accepts a boolean, an object (see ProxySetting), or a
// boolean-like string.
func (p *ProxySetting) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var obj map[string]interface{}
		if err := json.Unmarshal(trimmed, &obj); err != nil {
			return err
		}
		*p = proxySettingFromObject(obj)
		return nil
	}
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return err
		}
		b, err := strconv.ParseBool(strings.TrimSpace(s))
		if err != nil {
			return fmt.Errorf("proxy must be a boolean, got %q", s)
		}
		*p = ProxySetting(b)
		return nil
	}
	var b bool
	if err := json.Unmarshal(trimmed, &b); err != nil {
		return err
	}
	*p = ProxySetting(b)
	return nil
}

func proxySettingFromObject(obj map[string]interface{}) ProxySetting {
	if enabled, ok := obj["enabled"]; ok {
		if b, ok := enabled.(bool); ok {
			return ProxySetting(b)
		}
	}
	return ProxySetting(true)
}

// Note: viper drops empty map values from settings, so an empty object form
// is indistinguishable from unset on the viper path (Load/Reload) and behaves
// as false; only the encoding/json path (SaveConfig) can honor it as enabled.

type Config struct {
	Models  []models.ModelConfig `json:"models" mapstructure:"models"`
	Workers []WorkerConfig       `json:"workers,omitempty" mapstructure:"workers"`
	Proxy   ProxySetting         `json:"proxy" mapstructure:"proxy"`
}

func Load(configPath string) (*Config, error) {
	viper.Reset()
	viper.SetConfigFile(configPath)
	viper.SetConfigType("json")

	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := decodeConfig(viper.GetViper(), &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return &config, nil
}

// Reload reloads the configuration from the same config file
func Reload(configPath string) (*Config, error) {
	// Reset viper to clear previous config
	viper.Reset()

	viper.SetConfigFile(configPath)
	viper.SetConfigType("json")

	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to reload config file: %w", err)
	}

	var config Config
	if err := decodeConfig(viper.GetViper(), &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return &config, nil
}

// decodeConfig unmarshals through viper with the flex hooks added, so the
// viper path accepts the same alternate forms UnmarshalJSON handles for the
// encoding/json path.
func decodeConfig(v *viper.Viper, out *Config) error {
	return v.Unmarshal(out, func(c *mapstructure.DecoderConfig) {
		c.DecodeHook = mapstructure.ComposeDecodeHookFunc(c.DecodeHook, flexHook)
	})
}

var (
	workerEntryType = reflect.TypeOf(WorkerConfig{})
	proxyType       = reflect.TypeOf(ProxySetting(false))
)

// flexHook teaches the mapstructure decoder the alternate config forms:
// plain-string worker entries and non-boolean proxy values.
func flexHook(from reflect.Type, to reflect.Type, data interface{}) (interface{}, error) {
	if to == workerEntryType {
		if s, ok := data.(string); ok {
			return WorkerConfig{URL: strings.TrimSpace(s)}, nil
		}
		return data, nil
	}
	if to == proxyType {
		switch v := data.(type) {
		case bool:
			return ProxySetting(v), nil
		case string:
			b, err := strconv.ParseBool(strings.TrimSpace(v))
			if err != nil {
				return nil, fmt.Errorf("proxy must be a boolean, got %q", v)
			}
			return ProxySetting(b), nil
		case map[string]interface{}:
			return proxySettingFromObject(v), nil
		}
	}
	return data, nil
}
