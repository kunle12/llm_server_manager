package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"llamamanager/config"
	"llamamanager/metrics"
	"llamamanager/models"
)

// LocalMachine is the name the coordinator uses for itself in fleet views.
const LocalMachine = "local"

// DefaultLlamaPort is llama-server's default port, used when a model config
// sets none.
const DefaultLlamaPort = 8081

// Timeouts for worker calls: reads must stay snappy for status probes;
// start/stop can legitimately take longer (process spawn).
const (
	defaultReadTimeout    = 5 * time.Second
	defaultControlTimeout = 30 * time.Second

	// maxWorkerResponseBytes caps how much of a worker reply is read.
	maxWorkerResponseBytes = 1 << 22 // 4MB
)

// Worker is one remote llm_server_manager instance.
type Worker struct {
	Name   string // config name, defaulting to the URL hostname
	URL    string // manager base URL, e.g. "http://gpu1.local:8080"
	APIKey string // sent as the worker's api-key header; empty = unauthenticated

	readTimeout    time.Duration
	controlTimeout time.Duration
	client         *http.Client
}

// NewWorker builds a Worker from a config entry.
func NewWorker(spec config.WorkerConfig) (*Worker, error) {
	return newWorker(spec, defaultReadTimeout, defaultControlTimeout)
}

func newWorker(spec config.WorkerConfig, readTimeout, controlTimeout time.Duration) (*Worker, error) {
	rawURL := strings.TrimSpace(spec.URL)
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid worker URL %q: %w", spec.URL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("worker URL %q must use http or https", spec.URL)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("worker URL %q is missing a host", spec.URL)
	}

	name := strings.TrimSpace(spec.Name)
	if name == "" {
		name = u.Hostname()
	}

	return &Worker{
		Name:           name,
		URL:            strings.TrimRight(u.String(), "/"),
		APIKey:         spec.APIKey,
		readTimeout:    readTimeout,
		controlTimeout: controlTimeout,
		client:         &http.Client{},
	}, nil
}

// WorkerError is a failure reported by a worker (non-2xx reply), as opposed
// to a transport failure (worker unreachable, timeout). Handlers pass the
// worker's status code and message through to the caller.
type WorkerError struct {
	StatusCode int
	Message    string
}

func (e *WorkerError) Error() string { return e.Message }

// call performs a manager API call against the worker and returns the raw
// response body on success (any 2xx). Transport failures come back as plain
// errors; non-2xx replies as *WorkerError.
func (w *Worker) call(ctx context.Context, method, path string, timeout time.Duration) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, method, w.URL+path, nil)
	if err != nil {
		return nil, err
	}
	if w.APIKey != "" {
		req.Header.Set("api-key", w.APIKey)
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxWorkerResponseBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &WorkerError{
			StatusCode: resp.StatusCode,
			Message:    workerMessage(resp.StatusCode, data),
		}
	}
	return data, nil
}

// workerMessage extracts a human-readable message from a worker error reply,
// which uses the APIResponse envelope when the worker speaks our protocol.
// Anything else (an HTML error page from a proxy, a truncated body) becomes a
// generic message rather than leaking the raw payload into the UI.
func workerMessage(statusCode int, body []byte) string {
	var envelope struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Message != "" {
		return envelope.Message
	}
	return fmt.Sprintf("The manager returned an unexpected response (HTTP %d)", statusCode)
}

// ListModels returns the worker's configured models. This single call is also
// the source for running-model detection: the item flagged active is the one
// the worker is running (and it carries the port and alias).
func (w *Worker) ListModels(ctx context.Context) ([]models.ModelItem, error) {
	data, err := w.call(ctx, http.MethodGet, "/api/v1/models", w.readTimeout)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Data struct {
			Models []models.ModelItem `json:"models"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("worker %s returned a malformed models payload: %w", w.Name, err)
	}
	return payload.Data.Models, nil
}

// RunningModel describes a model a machine is currently running.
type RunningModel struct {
	Name      string    `json:"name"`
	Alias     string    `json:"alias,omitempty"`
	Status    string    `json:"status"`
	StartTime time.Time `json:"start_time,omitempty"`
	Port      int       `json:"port"`
}

// GetRunning reports the model the worker is currently running, or
// (nil, nil) when it reports none.
func (w *Worker) GetRunning(ctx context.Context) (*RunningModel, error) {
	items, err := w.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	return RunningFromItems(items), nil
}

// System returns the worker's host telemetry. Older workers without
// GET /api/v1/system answer 404, which callers can treat as "unsupported".
func (w *Worker) System(ctx context.Context) (*metrics.Snapshot, error) {
	data, err := w.call(ctx, http.MethodGet, "/api/v1/system", w.readTimeout)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Success bool             `json:"success"`
		Message string           `json:"message"`
		Data    metrics.Snapshot `json:"data"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("worker %s returned a malformed system payload: %w", w.Name, err)
	}
	if !payload.Success {
		return nil, fmt.Errorf("worker %s: %s", w.Name, payload.Message)
	}
	return &payload.Data, nil
}

// RunningFromItems finds the active model in a models list and renders it as
// a RunningModel, or nil when no item is active. StartTime is left zero here:
// the models-list API does not expose it (the coordinator fills it in for its
// own local machine, where it is known).
func RunningFromItems(items []models.ModelItem) *RunningModel {
	for _, item := range items {
		if !item.Active {
			continue
		}
		port, _ := RoutablePort(item.ModelConfig)
		return &RunningModel{
			Name:   item.Name,
			Alias:  item.Alias,
			Status: string(models.StatusRunning),
			Port:   port,
		}
	}
	return nil
}

// StartModel asks the worker to start a model. A nil return means the start
// was accepted; a *WorkerError carries the worker's own status and message.
func (w *Worker) StartModel(ctx context.Context, name string) error {
	_, err := w.call(ctx, http.MethodPost,
		"/api/v1/models/"+url.PathEscape(name)+"/start", w.controlTimeout)
	return err
}

// StopModel asks the worker to stop a running model.
func (w *Worker) StopModel(ctx context.Context, name string) error {
	_, err := w.call(ctx, http.MethodDelete,
		"/api/v1/models/"+url.PathEscape(name)+"/stop", w.controlTimeout)
	return err
}

// launchPortPattern matches "--port 8081" and "--port=8081" (first match
// wins); the [= ] boundary keeps flags like --ports from matching.
var launchPortPattern = regexp.MustCompile(`--port[= ]([0-9]+)`)

// RoutablePort returns the port a running model can be reached on and whether
// it is routable at all. Standard models use the configured port, defaulting
// to llama-server's 8081. launch_cmd models are routable only when --port
// parses out of the command string; otherwise the model is controllable via
// the control plane but never published or routed.
func RoutablePort(cfg models.ModelConfig) (int, bool) {
	if cfg.LaunchCmd != nil && *cfg.LaunchCmd != "" {
		return parseLaunchPort(*cfg.LaunchCmd)
	}
	if cfg.Port != nil && *cfg.Port > 0 {
		return *cfg.Port, true
	}
	return DefaultLlamaPort, true
}

func parseLaunchPort(cmd string) (int, bool) {
	m := launchPortPattern.FindStringSubmatch(cmd)
	if m == nil {
		return 0, false
	}
	port, err := strconv.Atoi(m[1])
	if err != nil || port < 1 || port > 65535 {
		return 0, false
	}
	return port, true
}
