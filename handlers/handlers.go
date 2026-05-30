package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"

	"llamamanager/manager"
	"llamamanager/models"

	"github.com/gorilla/mux"
)

const (
	apiKeyHeader     = "api-key"
	envAPIKey        = "LLM_MANAGER_API_KEY"
	envAllowedOrigins = "LLM_ALLOWED_ORIGINS"
)

type Handler struct {
	manager        *manager.ServerManager
	logger         func(format string, args ...interface{})
	apiKey         string
	enabled        bool
	allowedOrigins map[string]bool
}

// validAPIKeyPattern matches exactly 16 alphanumeric characters
var validAPIKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9]{16}$`)

func New(mgr *manager.ServerManager, logger func(format string, args ...interface{})) *Handler {
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

	return &Handler{
		manager:        mgr,
		logger:         logger,
		apiKey:         apiKey,
		enabled:        enabled,
		allowedOrigins: allowedOrigins,
	}
}

func (h *Handler) ListModels(w http.ResponseWriter, r *http.Request) {
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

	response := models.ModelListResponse{
		Models: modelList,
	}

	h.sendJSON(w, http.StatusOK, models.APIResponse{
		Success: true,
		Data:    response,
	})
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

	current := h.manager.GetCurrentServer()
	if current == nil {
		h.sendJSON(w, http.StatusBadRequest, models.APIResponse{
			Success: false,
			Message: "no server is currently running",
		})
		return
	}

	if current.ModelConfig.Name != modelName {
		h.sendJSON(w, http.StatusBadRequest, models.APIResponse{
			Success: false,
			Message: fmt.Sprintf("server is running model '%s', not '%s'", current.ModelConfig.Name, modelName),
		})
		return
	}

	if err := h.manager.StopCurrent(); err != nil {
		h.sendJSON(w, http.StatusInternalServerError, models.APIResponse{
			Success: false,
			Message: err.Error(),
		})
		return
	}

	h.sendJSON(w, http.StatusOK, models.APIResponse{
		Success: true,
		Message: fmt.Sprintf("model '%s' stopped successfully", current.ModelConfig.Name),
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
			if len(h.allowedOrigins) > 0 {
				if !h.allowedOrigins[origin] {
					http.Error(w, "origin not allowed", http.StatusForbidden)
					return
				}
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
			} else {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
			}
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
