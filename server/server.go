package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
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
	mgr           *manager.ServerManager
	handler       *handlers.Handler
	logger        func(format string, args ...interface{})
	router        *mux.Router
	httpSrv       *http.Server
	enableLogging bool
	configPath    string
	watcherWg     sync.WaitGroup
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

// maxRequestSize enforces a maximum request body size to prevent memory
// exhaustion attacks. Content-Length is checked up front so oversized
// requests are rejected without reading the body; MaxBytesReader remains as
// a backstop for chunked/unknown-length bodies.
func maxRequestSize(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > maxBytes {
				http.Error(w, "request entity too large", http.StatusRequestEntityTooLarge)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}

// indexModels indexes models by name. Entries without a name are skipped, with
// a warning, since they can never be addressed through the API. The load and
// reload paths share this so they cannot disagree about the model set.
func indexModels(list []models.ModelConfig, logger func(format string, args ...interface{})) map[string]*models.ModelConfig {
	indexed := make(map[string]*models.ModelConfig, len(list))
	for i := range list {
		m := &list[i]
		if m.Name == "" {
			logger("Warning: skipping model with empty name (index %d)", i)
			continue
		}
		indexed[m.Name] = m
	}
	return indexed
}

func New(configPath string, enableLogging bool, maxRetries int) (*App, error) {
	logger := func(format string, args ...interface{}) {
		log.Printf("[LLM Manager] "+format, args...)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	mgr, err := manager.New(indexModels(cfg.Models, logger), logger, enableLogging, maxRetries)
	if err != nil {
		return nil, fmt.Errorf("failed to create server manager: %w", err)
	}
	h := handlers.New(mgr, logger)

	app := &App{
		mgr:           mgr,
		handler:       h,
		logger:        logger,
		enableLogging: enableLogging,
		configPath:    configPath,
	}

	app.setupRouter()

	return app, nil
}

// GetModelCount returns the number of configured models. It reads through the
// manager so it stays consistent with concurrent config reloads.
func (a *App) GetModelCount() int {
	return a.mgr.ModelCount()
}

// configReloadDebounce is how long to wait after the last config file event
// before reloading, so a burst of events from a single save is coalesced into
// one reload.
const configReloadDebounce = 300 * time.Millisecond

// WatchConfig starts a goroutine that watches the config file and reloads the
// configuration whenever it changes.
//
// The parent directory is watched rather than the file itself: editors such as
// vim save by renaming the original file to a backup and then creating a new
// file, which replaces the inode and silently invalidates a watch placed on the
// file. Watching the directory and filtering events by file name keeps the
// watch alive across replacements on both Linux (inotify) and macOS (kqueue).
func (a *App) WatchConfig() {
	absConfig, err := filepath.Abs(a.configPath)
	if err != nil {
		a.logger("Failed to resolve config path: %v", err)
		return
	}
	dir, base := filepath.Dir(absConfig), filepath.Base(absConfig)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		a.logger("Failed to create config watcher: %v", err)
		return
	}

	if err := watcher.Add(dir); err != nil {
		a.logger("Failed to watch config directory: %v", err)
		watcher.Close()
		return
	}

	a.watcherWg.Add(1)
	go func() {
		defer a.watcherWg.Done()
		defer watcher.Close()

		// A single save can produce several events (rename of the old file,
		// create of the new one, chmod), so reload once the events settle.
		// Debouncing also avoids reading the file while it is still being
		// written.
		var timer *time.Timer
		var timerC <-chan time.Time
		resetTimer := func() {
			if timer == nil {
				timer = time.NewTimer(configReloadDebounce)
			} else {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(configReloadDebounce)
			}
			timerC = timer.C
		}

		for {
			select {
			case <-a.mgr.GetStopChan():
				return
			case <-timerC:
				timerC = nil
				a.logger("Config file changed, reloading...")
				if err := a.reloadConfig(); err != nil {
					a.logger("Failed to reload config: %v", err)
				}
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				// Compare base names: event paths may be canonicalised
				// differently than the configured path (e.g. symlinked
				// directories such as /var -> /private/var on macOS).
				if filepath.Base(event.Name) != base {
					continue
				}
				if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Chmod) == 0 {
					continue
				}
				resetTimer()
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

	a.mgr.ReloadConfigs(indexModels(cfg.Models, a.logger))
	return nil
}

func (a *App) setupRouter() {
	r := mux.NewRouter()

	r.Use(a.handler.CORS)

	// Rate limiting: 10 requests per second, burst of 20
	rl := newRateLimiter(rate.Limit(10), 20)
	r.Use(rl.limit)

	// Limit request body to 1KB to prevent memory exhaustion
	r.Use(maxRequestSize(1024))

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
