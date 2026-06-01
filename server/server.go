package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"llamamanager/config"
	"llamamanager/handlers"
	"llamamanager/manager"
	"llamamanager/models"

	"github.com/fsnotify/fsnotify"
	"github.com/gorilla/mux"
	"golang.org/x/time/rate"
)

type App struct {
	mgr              *manager.ServerManager
	handler          *handlers.Handler
	logger           func(format string, args ...interface{})
	router           *mux.Router
	httpSrv          *http.Server
	config           *config.Config
	enableLogging    bool
	configPath       string
	watcherWg        sync.WaitGroup
}

// rateLimiter provides per-IP rate limiting
type rateLimiterEntry struct {
	limiter  *rate.Limiter
	lastUsed time.Time
}

type rateLimiter struct {
	limiters        map[string]*rateLimiterEntry
	rate            rate.Limit
	burst           int
	mu              sync.Mutex
	lastCleanup     time.Time
	cleanupInterval time.Duration
}

func newRateLimiter(r rate.Limit, b int) *rateLimiter {
	return &rateLimiter{
		limiters:        make(map[string]*rateLimiterEntry),
		rate:            r,
		burst:           b,
		lastCleanup:     time.Now(),
		cleanupInterval: 5 * time.Minute,
	}
}

func (rl *rateLimiter) getLimiter(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	entry, exists := rl.limiters[ip]
	if !exists {
		entry = &rateLimiterEntry{
			limiter:  rate.NewLimiter(rl.rate, rl.burst),
			lastUsed: time.Now(),
		}
		rl.limiters[ip] = entry
	} else {
		entry.lastUsed = time.Now()
	}

	if time.Since(rl.lastCleanup) > rl.cleanupInterval {
		rl.cleanup()
		rl.lastCleanup = time.Now()
	}

	return entry.limiter
}

func (rl *rateLimiter) cleanup() {
	for ip, entry := range rl.limiters {
		if time.Since(entry.lastUsed) > rl.cleanupInterval {
			delete(rl.limiters, ip)
		}
	}
}

func (rl *rateLimiter) limit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.RemoteAddr
		if !rl.getLimiter(ip).Allow() {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// maxBytesReader limits request body size to prevent memory exhaustion attacks
func maxBytesReader(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}

func New(configPath string, enableLogging bool, maxRetries int) (*App, error) {
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

	mgr, err := manager.New(modelMap, logger, enableLogging, maxRetries)
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

	if err := watcher.Add(a.configPath); err != nil {
		a.logger("Failed to watch config file: %v", err)
		watcher.Close()
		return
	}

	a.watcherWg.Add(1)
	go func() {
		defer a.watcherWg.Done()
		for {
			select {
			case <-a.mgr.GetStopChan():
				watcher.Close()
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
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
		config := &cfg.Models[i]
		if config.Name == "" {
			a.logger("Warning: skipping model with empty name in reloaded config")
			continue
		}
		modelMap[config.Name] = config
	}

	a.mgr.ReloadConfigs(modelMap)
	a.config = cfg
	return nil
}

func (a *App) setupRouter() {
	r := mux.NewRouter()

	r.Use(a.handler.CORS)

	// Rate limiting: 10 requests per second, burst of 20
	rl := newRateLimiter(rate.Limit(10), 20)
	r.Use(rl.limit)

	// Limit request body to 1KB to prevent memory exhaustion
	r.Use(maxBytesReader(1024))

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
	// Clean up PID file if it exists (created by daemon mode)
	os.Remove("/tmp/llm_server_manager.pid")

	if a.httpSrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := a.httpSrv.Shutdown(ctx); err != nil {
			return fmt.Errorf("failed to shutdown server: %w", err)
		}
	}

	a.mgr.CloseStopChan()
	a.watcherWg.Wait()

	if a.mgr.GetCurrentServer() != nil {
		a.logger("Stopping running server...")
		if err := a.mgr.StopCurrent(); err != nil {
			a.logger("Warning: failed to stop server: %v", err)
		}
	}

	return nil
}
