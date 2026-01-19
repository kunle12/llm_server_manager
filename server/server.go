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

	"github.com/fsnotify/fsnotify"
	"github.com/gorilla/mux"
)

type App struct {
	mgr           *manager.ServerManager
	handler       *handlers.Handler
	logger        func(format string, args ...interface{})
	router        *mux.Router
	httpSrv       *http.Server
	config        *config.Config
	enableLogging bool
	configPath    string
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
		mgr:           mgr,
		handler:       h,
		logger:        logger,
		config:        cfg,
		enableLogging: enableLogging,
		configPath:    configPath,
	}

	app.setupRouter()

	return app, nil
}

func (a *App) GetModelCount() int {
	return len(a.config.Models)
}

// WatchConfig starts a goroutine that watches the config file for changes
func (a *App) WatchConfig() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		a.logger("Failed to create config watcher: %v", err)
		return
	}

	// Watch the config file
	if err := watcher.Add(a.configPath); err != nil {
		a.logger("Failed to watch config file: %v", err)
		return
	}

	go func() {
		for {
			select {
			case <-a.mgr.GetStopChan():
				watcher.Close()
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				// Reload on write/remove operations
				if event.Op&(fsnotify.Write|fsnotify.Remove) != 0 {
					a.logger("Config file changed, reloading...")
					if err := a.reloadConfig(); err != nil {
						a.logger("Failed to reload config: %v", err)
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				a.logger("Config watcher error: %v", err)
			}
		}
	}()
}

// reloadConfig reloads the configuration from file and updates the manager
func (a *App) reloadConfig() error {
	cfg, err := config.Reload(a.configPath)
	if err != nil {
		return err
	}

	modelMap := make(map[string]*models.ModelConfig)
	for i := range cfg.Models {
		modelMap[cfg.Models[i].Name] = &cfg.Models[i]
	}

	a.mgr.ReloadConfigs(modelMap)
	a.config = cfg
	return nil
}

func (a *App) setupRouter() {
	r := mux.NewRouter()

	r.Use(a.handler.CORS)

	api := r.PathPrefix("/api/v1").Subrouter()
	api.Use(a.handler.RequireAPIKey)
	api.HandleFunc("/models", a.handler.ListModels).Methods("GET")
	api.HandleFunc("/models/{model}/start", a.handler.StartModel).Methods("POST")
	api.HandleFunc("/models/{model}/stop", a.handler.StopModel).Methods("DELETE")
	api.HandleFunc("/models/running", a.handler.GetRunningModel).Methods("GET")

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

	// Start config file watcher
	a.WatchConfig()

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

	// Signal the config watcher to stop
	a.mgr.CloseStopChan()

	if a.mgr.GetCurrentServer() != nil {
		a.logger("Stopping running server...")
		if err := a.mgr.StopCurrent(); err != nil {
			a.logger("Warning: failed to stop server: %v", err)
		}
	}

	return nil
}
