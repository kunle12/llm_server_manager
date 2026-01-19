package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"

	"llamamanager/manager"
	"llamamanager/models"

	"github.com/gorilla/mux"
)

const (
	apiKeyHeader = "api-key"
	envAPIKey    = "LLM_MANAGER_API_KEY"
)

type Handler struct {
	manager  *manager.ServerManager
	logger   func(format string, args ...interface{})
	apiKey   string
	enabled  bool
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

	return &Handler{
		manager: mgr,
		logger:  logger,
		apiKey:  apiKey,
		enabled: enabled,
	}
}

func (h *Handler) ListModels(w http.ResponseWriter, r *http.Request) {
	modelMap := h.manager.ListModels()
	current := h.manager.GetCurrentServer()

	var modelList []models.ModelItem
	for _, config := range modelMap {
		active := current != nil && current.ModelConfig.Name == config.Name && current.Status == models.StatusRunning
		modelList = append(modelList, models.ModelItem{
			ModelConfig: *config,
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

		if providedKey != h.apiKey {
			h.sendJSON(w, http.StatusUnauthorized, models.APIResponse{
				Success: false,
				Message: "invalid api key",
			})
			return
		}

		next.ServeHTTP(w, r)
	})
}
