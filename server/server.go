package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"llamamanager/config"
	"llamamanager/handlers"
	"llamamanager/manager"
	"llamamanager/models"

	"github.com/gorilla/mux"
)

type App struct {
	mgr          *manager.ServerManager
	handler      *handlers.Handler
	logger       func(format string, args ...interface{})
	router       *mux.Router
	httpSrv      *http.Server
	config       *config.Config
	enableLogging bool
}

func New(configPath string, enableLogging bool) (*App, error) {
	logger := func(format string, args ...interface{}) {
		log.Printf("[LLM Manager] "+format, args...)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	modelMap := make(map[string]*models.ModelConfig)
	for i := range cfg.Models {
		modelMap[cfg.Models[i].Name] = &cfg.Models[i]
	}

	mgr, err := manager.New(modelMap, logger, enableLogging)
	if err != nil {
		return nil, fmt.Errorf("failed to create server manager: %w", err)
	}
	h := handlers.New(mgr, logger)

	app := &App{
		mgr:          mgr,
		handler:      h,
		logger:       logger,
		config:       cfg,
		enableLogging: enableLogging,
	}

	app.setupRouter()

	return app, nil
}

func (a *App) GetModelCount() int {
	return len(a.config.Models)
}

func (a *App) setupRouter() {
	r := mux.NewRouter()

	r.Use(a.handler.CORS)

	api := r.PathPrefix("/api/v1").Subrouter()
	api.HandleFunc("/models", a.handler.ListModels).Methods("GET")
	api.HandleFunc("/models/{model}/start", a.handler.StartModel).Methods("POST")
	api.HandleFunc("/models/{model}/stop", a.handler.StopModel).Methods("DELETE")

	a.router = r
}

func (a *App) Start(listenAddr string) error {
	a.httpSrv = &http.Server{
		Addr:         listenAddr,
		Handler:      a.router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		a.logger("Server starting on %s", listenAddr)
		if err := a.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			a.logger("Server failed to start: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	a.logger("Shutting down server...")
	return a.Shutdown()
}

func (a *App) Shutdown() error {
	if a.httpSrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := a.httpSrv.Shutdown(ctx); err != nil {
			return fmt.Errorf("failed to shutdown server: %w", err)
		}
	}

	if a.mgr.GetCurrentServer() != nil {
		a.logger("Stopping running server...")
		if err := a.mgr.StopCurrent(); err != nil {
			a.logger("Warning: failed to stop server: %v", err)
		}
	}

	return nil
}
