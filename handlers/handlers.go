package handlers

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"llamamanager/config"
	"llamamanager/fleet"
	"llamamanager/manager"
	"llamamanager/models"

	"github.com/gorilla/mux"
)

const (
	apiKeyHeader      = "api-key"
	envAPIKey         = "LLM_MANAGER_API_KEY"
	envAllowedOrigins = "LLM_ALLOWED_ORIGINS"

	// maxConfigBodyBytes caps the request body accepted by PUT /api/v1/config.
	maxConfigBodyBytes = 1 << 20 // 1MB
)

type Handler struct {
	manager        *manager.ServerManager
	fleet          *fleet.Registry
	logger         func(format string, args ...interface{})
	apiKey         string
	enabled        bool
	allowedOrigins map[string]bool
	configPath     string
	onConfigSaved  func() error
}

// validAPIKeyPattern matches exactly 16 alphanumeric characters
var validAPIKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9]{16}$`)

func New(mgr *manager.ServerManager, fleetReg *fleet.Registry, logger func(format string, args ...interface{}), configPath string) *Handler {
	apiKey := os.Getenv(envAPIKey)
	var enabled bool
	if apiKey != "" {
		if !validAPIKeyPattern.MatchString(apiKey) {
			logger("WARNING: LLM_MANAGER_API_KEY must be exactly 16 alphanumeric characters, authentication disabled")
			enabled = false
		} else {
			enabled = true
			logger("API key authentication enabled")
		}
	} else {
		logger("API key authentication disabled (environment variable not set)")
	}

	// Parse allowed origins from environment variable
	allowedOrigins := make(map[string]bool)
	originsEnv := os.Getenv(envAllowedOrigins)
	if originsEnv != "" {
		for _, origin := range strings.Split(originsEnv, ",") {
			origin = strings.TrimSpace(origin)
			if origin != "" {
				allowedOrigins[origin] = true
			}
		}
		if len(allowedOrigins) > 0 {
			logger("CORS restricted to allowed origins: %s", originsEnv)
		}
	}

	if fleetReg == nil {
		// Standalone callers (and tests) get an empty registry so the fleet
		// endpoints answer with an empty worker set instead of panicking.
		fleetReg = fleet.NewRegistry(nil, logger)
	}

	return &Handler{
		manager:        mgr,
		fleet:          fleetReg,
		logger:         logger,
		apiKey:         apiKey,
		enabled:        enabled,
		allowedOrigins: allowedOrigins,
		configPath:     configPath,
	}
}

func (h *Handler) ListModels(w http.ResponseWriter, r *http.Request) {
	response := models.ModelListResponse{
		Models: h.localModelItems(),
	}

	h.sendJSON(w, http.StatusOK, models.APIResponse{
		Success: true,
		Data:    response,
	})
}

// localModelItems renders the local machine's models in list form: the same
// shape ListModels serves, reused by the fleet view.
func (h *Handler) localModelItems() []models.ModelItem {
	modelMap := h.manager.ListModels()
	current := h.manager.GetCurrentServer()

	var modelList []models.ModelItem
	for _, config := range modelMap {
		cfg := *config
		if cfg.LaunchCmd != nil && *cfg.LaunchCmd != "" {
			cfg.ModelPath = "use launch cmd"
			cfg.Temperature = 0
			cfg.Threads = 0
			cfg.ContextSize = nil
			cfg.TopK = nil
			cfg.TopP = nil
			cfg.Port = nil
			cfg.Mmproj = nil
			cfg.ChatTemplateKwargs = nil
			cfg.Ngl = nil
			cfg.Mmap = nil
			cfg.SpecDraftNMax = nil
		}
		active := current != nil && current.ModelConfig.Name == config.Name && current.Status == models.StatusRunning
		modelList = append(modelList, models.ModelItem{
			ModelConfig: cfg,
			Active:      active,
		})
	}
	return modelList
}

func (h *Handler) StartModel(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	modelName := vars["model"]

	if modelName == "" {
		h.sendJSON(w, http.StatusBadRequest, models.APIResponse{
			Success: false,
			Message: "model name is required",
		})
		return
	}

	err := h.manager.StartModel(modelName)
	if err != nil {
		h.sendJSON(w, http.StatusBadRequest, models.APIResponse{
			Success: false,
			Message: err.Error(),
		})
		return
	}

	h.sendJSON(w, http.StatusOK, models.APIResponse{
		Success: true,
		Message: fmt.Sprintf("model '%s' starting", modelName),
		Data:    h.manager.GetCurrentServer(),
	})
}

func (h *Handler) GetRunningModel(w http.ResponseWriter, r *http.Request) {
	current := h.manager.GetCurrentServer()

	if current == nil {
		h.sendJSON(w, http.StatusOK, models.APIResponse{
			Success: false,
			Message: "no model is operating",
		})
		return
	}

	h.sendJSON(w, http.StatusOK, models.APIResponse{
		Success: true,
		Message: fmt.Sprintf("model '%s' is currently operating", current.ModelConfig.Name),
	})
}

// ListWorkers reports the health of every configured worker. Standalone
// instances (no workers configured) return an empty list.
func (h *Handler) ListWorkers(w http.ResponseWriter, r *http.Request) {
	h.sendJSON(w, http.StatusOK, models.APIResponse{
		Success: true,
		Data:    fleet.WorkerListResponse{Workers: h.fleet.WorkerStatuses(r.Context())},
	})
}

// GetFleet aggregates the local machine and all workers into one view, local
// first. The local machine is always "online" — it is the process answering
// the request.
func (h *Handler) GetFleet(w http.ResponseWriter, r *http.Request) {
	machines := make([]fleet.FleetMachine, 0, 1)
	machines = append(machines, h.localFleetMachine())

	for _, snap := range h.fleet.Snapshots(r.Context()) {
		machines = append(machines, snap.FleetMachine())
	}

	h.sendJSON(w, http.StatusOK, models.APIResponse{
		Success: true,
		Data:    fleet.FleetResponse{Machines: machines},
	})
}

// localFleetMachine renders the coordinator's own machine entry.
func (h *Handler) localFleetMachine() fleet.FleetMachine {
	m := fleet.FleetMachine{
		Machine: fleet.LocalMachine,
		Status:  fleet.StatusOnline,
		Models:  h.localModelItems(),
	}
	if current := h.manager.GetCurrentServer(); current != nil {
		port, _ := fleet.RoutablePort(current.ModelConfig)
		m.Running = &fleet.RunningModel{
			Name:      current.ModelConfig.Name,
			Alias:     current.ModelConfig.Alias,
			Status:    string(current.Status),
			StartTime: current.StartTime,
			Port:      port,
		}
	}
	return m
}

// resolveWorker maps the {worker} path segment (config name or URL host) to
// a configured worker.
func (h *Handler) resolveWorker(w http.ResponseWriter, r *http.Request) *fleet.Worker {
	vars := mux.Vars(r)
	worker, err := h.fleet.Resolve(vars["worker"])
	if err != nil {
		h.sendJSON(w, http.StatusNotFound, models.APIResponse{
			Success: false,
			Message: err.Error(),
		})
		return nil
	}
	return worker
}

// workerError maps worker client failures for control endpoints:
// worker-reported errors pass through with the worker's status code and
// message; transport failures become 502.
func (h *Handler) workerError(w http.ResponseWriter, err error) {
	var we *fleet.WorkerError
	if errors.As(err, &we) {
		h.sendJSON(w, we.StatusCode, models.APIResponse{
			Success: false,
			Message: we.Message,
		})
		return
	}
	h.sendJSON(w, http.StatusBadGateway, models.APIResponse{
		Success: false,
		Message: err.Error(),
	})
}

// WorkerListModels passes through a worker's model list.
func (h *Handler) WorkerListModels(w http.ResponseWriter, r *http.Request) {
	worker := h.resolveWorker(w, r)
	if worker == nil {
		return
	}

	items, err := worker.ListModels(r.Context())
	if err != nil {
		h.workerError(w, err)
		return
	}
	if items == nil {
		items = []models.ModelItem{}
	}

	h.sendJSON(w, http.StatusOK, models.APIResponse{
		Success: true,
		Data:    models.ModelListResponse{Models: items},
	})
}

// WorkerStartModel asks a worker to start one of its configured models.
func (h *Handler) WorkerStartModel(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	modelName := vars["model"]

	worker := h.resolveWorker(w, r)
	if worker == nil {
		return
	}

	if err := worker.StartModel(r.Context(), modelName); err != nil {
		h.workerError(w, err)
		return
	}

	h.sendJSON(w, http.StatusOK, models.APIResponse{
		Success: true,
		Message: fmt.Sprintf("model '%s' starting on worker '%s'", modelName, worker.Name),
	})
}

// WorkerStopModel asks a worker to stop its running model (or a specific one
// by name).
func (h *Handler) WorkerStopModel(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	modelName := vars["model"]

	worker := h.resolveWorker(w, r)
	if worker == nil {
		return
	}

	if err := worker.StopModel(r.Context(), modelName); err != nil {
		h.workerError(w, err)
		return
	}

	h.sendJSON(w, http.StatusOK, models.APIResponse{
		Success: true,
		Message: fmt.Sprintf("model '%s' stopped on worker '%s'", modelName, worker.Name),
	})
}

// SetConfigSavedCallback registers a function invoked after the config file
// has been rewritten via the API. A returned error is logged and surfaced as
// a warning; the config watcher retries the reload regardless.
func (h *Handler) SetConfigSavedCallback(fn func() error) {
	h.onConfigSaved = fn
}

// GetConfig returns the raw config file so clients can edit it and PUT it
// back verbatim. Unlike the other endpoints the body is the file itself, not
// an APIResponse envelope.
func (h *Handler) GetConfig(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(h.configPath)
	if err != nil {
		h.sendJSON(w, http.StatusInternalServerError, models.APIResponse{
			Success: false,
			Message: fmt.Sprintf("failed to read config: %v", err),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(data); err != nil {
		h.logger("Failed to write config response: %v", err)
	}
}

// SaveConfig validates a config uploaded as raw JSON, writes it to the config
// file atomically, and triggers a reload. The config watcher also picks the
// change up on its own, so a failed immediate reload self-heals.
func (h *Handler) SaveConfig(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxConfigBodyBytes+1))
	if err != nil {
		h.sendJSON(w, http.StatusBadRequest, models.APIResponse{
			Success: false,
			Message: fmt.Sprintf("failed to read request body: %v", err),
		})
		return
	}
	if int64(len(body)) > maxConfigBodyBytes {
		h.sendJSON(w, http.StatusRequestEntityTooLarge, models.APIResponse{
			Success: false,
			Message: fmt.Sprintf("config exceeds %d bytes", maxConfigBodyBytes),
		})
		return
	}

	var cfg config.Config
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&cfg); err != nil {
		h.sendJSON(w, http.StatusBadRequest, models.APIResponse{
			Success: false,
			Message: fmt.Sprintf("invalid JSON: %v", err),
		})
		return
	}

	if len(cfg.Models) == 0 {
		h.sendJSON(w, http.StatusBadRequest, models.APIResponse{
			Success: false,
			Message: "config must contain at least one model",
		})
		return
	}

	seen := make(map[string]bool, len(cfg.Models))
	for i := range cfg.Models {
		m := &cfg.Models[i]
		if m.Name == "" {
			h.sendJSON(w, http.StatusBadRequest, models.APIResponse{
				Success: false,
				Message: fmt.Sprintf("model at index %d is missing a name", i),
			})
			return
		}
		if seen[m.Name] {
			h.sendJSON(w, http.StatusBadRequest, models.APIResponse{
				Success: false,
				Message: fmt.Sprintf("duplicate model name %q", m.Name),
			})
			return
		}
		seen[m.Name] = true
		if err := manager.ValidateModelConfig(m); err != nil {
			h.sendJSON(w, http.StatusBadRequest, models.APIResponse{
				Success: false,
				Message: fmt.Sprintf("model %q: %v", m.Name, err),
			})
			return
		}
	}

	if err := writeConfigFileAtomic(h.configPath, body); err != nil {
		h.sendJSON(w, http.StatusInternalServerError, models.APIResponse{
			Success: false,
			Message: fmt.Sprintf("failed to write config: %v", err),
		})
		return
	}

	h.logger("Config updated via API (%d models)", len(cfg.Models))

	message := "config saved"
	if h.onConfigSaved != nil {
		if err := h.onConfigSaved(); err != nil {
			h.logger("Config reload after save failed: %v", err)
			message = "config saved; reload pending (file watcher will retry)"
		}
	}

	h.sendJSON(w, http.StatusOK, models.APIResponse{
		Success: true,
		Message: message,
	})
}

// writeConfigFileAtomic writes data to path via a temp file plus rename so a
// crash mid-write cannot leave a truncated config behind.
func writeConfigFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)

	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// No-op after a successful rename; cleans up on any earlier failure.
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func (h *Handler) StopModel(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	modelName := vars["model"]

	if modelName == "" {
		h.sendJSON(w, http.StatusBadRequest, models.APIResponse{
			Success: false,
			Message: "model name is required",
		})
		return
	}

	if err := h.manager.StopModel(modelName); err != nil {
		h.sendJSON(w, http.StatusBadRequest, models.APIResponse{
			Success: false,
			Message: err.Error(),
		})
		return
	}

	h.sendJSON(w, http.StatusOK, models.APIResponse{
		Success: true,
		Message: fmt.Sprintf("model '%s' stopped successfully", modelName),
	})
}

func (h *Handler) sendJSON(w http.ResponseWriter, statusCode int, response interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(true)
	if err := encoder.Encode(response); err != nil {
		h.logger("Failed to encode JSON response: %v", err)
	}
}

func (h *Handler) CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		if origin != "" {
			if len(h.allowedOrigins) > 0 && !h.allowedOrigins[origin] {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, api-key")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (h *Handler) RequireAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.enabled {
			next.ServeHTTP(w, r)
			return
		}

		providedKey := r.Header.Get(apiKeyHeader)
		if providedKey == "" {
			h.sendJSON(w, http.StatusUnauthorized, models.APIResponse{
				Success: false,
				Message: "api-key header is required",
			})
			return
		}

		if subtle.ConstantTimeCompare([]byte(providedKey), []byte(h.apiKey)) != 1 {
			h.sendJSON(w, http.StatusUnauthorized, models.APIResponse{
				Success: false,
				Message: "invalid api key",
			})
			return
		}

		next.ServeHTTP(w, r)
	})
}
