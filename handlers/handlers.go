package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"llamamanager/manager"
	"llamamanager/models"

	"github.com/gorilla/mux"
)

type Handler struct {
	manager *manager.ServerManager
	logger  func(format string, args ...interface{})
}

func New(mgr *manager.ServerManager, logger func(format string, args ...interface{})) *Handler {
	return &Handler{
		manager: mgr,
		logger:  logger,
	}
}

func (h *Handler) ListModels(w http.ResponseWriter, r *http.Request) {
	modelMap := h.manager.ListModels()

	var modelList []models.ModelConfig
	for _, config := range modelMap {
		modelList = append(modelList, *config)
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
		Message: "server starting",
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
		Message: "server stopped successfully",
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
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
