package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"llamamanager/config"
	"llamamanager/fleet"
	"llamamanager/handlers"
	"llamamanager/manager"
	"llamamanager/models"
	"llamamanager/proxy"

	"github.com/fsnotify/fsnotify"
	"github.com/gorilla/mux"
	"golang.org/x/time/rate"
)

type App struct {
	mgr     *manager.ServerManager
	handler *handlers.Handler
	fleet   *fleet.Registry
	proxy   *proxy.Router
	// proxyEnabled gates the /v1 routes at request time so toggling the
	// "proxy" config key takes effect on reload without a restart.
	proxyEnabled  atomic.Bool
	logger        func(format string, args ...interface{})
	router        *mux.Router
	httpSrv       *http.Server
	enableLogging bool
	configPath    string
	watcherWg     sync.WaitGroup
	// reloadMu serialises reloads: the PUT callback and the file watcher can
	// both call reloadConfig, and config.Reload drives the global viper
	// singleton, which is not safe for concurrent use.
	reloadMu sync.Mutex

	// ServeUI controls serving the embedded web UI at /ui/ (see registerUIRoutes).
	ServeUI bool
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
// Duplicate aliases are warned about (not rejected): they resolve by
// first-match in the proxy routing table.
func indexModels(list []models.ModelConfig, logger func(format string, args ...interface{})) map[string]*models.ModelConfig {
	indexed := make(map[string]*models.ModelConfig, len(list))
	aliases := make(map[string]string)
	for i := range list {
		m := &list[i]
		if m.Name == "" {
			logger("Warning: skipping model with empty name (index %d)", i)
			continue
		}
		if m.Alias != "" {
			if prev, ok := aliases[m.Alias]; ok {
				logger("Warning: alias %q is declared by both %q and %q; the first declaration wins", m.Alias, prev, m.Name)
			} else {
				aliases[m.Alias] = m.Name
			}
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

	// The worker registry exists even in standalone mode (empty), so the
	// fleet endpoints answer consistently everywhere.
	fleetReg := fleet.NewRegistry(cfg.Workers, logger)

	h := handlers.New(mgr, fleetReg, logger, configPath)

	// The /v1 proxy routes are always registered but gated by a runtime flag,
	// so toggling "proxy" in the config applies on reload without a restart.
	// When off, /v1 answers 404 exactly as it did when the routes were absent.
	v1proxy := proxy.New(mgr, fleetReg, h.APIKey(), h.AuthEnabled(), logger)

	app := &App{
		mgr:           mgr,
		handler:       h,
		fleet:         fleetReg,
		proxy:         v1proxy,
		logger:        logger,
		enableLogging: enableLogging,
		configPath:    configPath,
	}
	app.proxyEnabled.Store(bool(cfg.Proxy))
	if bool(cfg.Proxy) {
		logger("Proxy enabled: /v1 routes active")
	} else {
		logger("Proxy disabled: /v1 routes return 404")
	}

	// The control API reports the runtime proxy state to the UI.
	h.SetProxyStatus(app.ProxyEnabled)

	// After an API config save, reload immediately so the response reflects
	// the new state. The file watcher reloads anyway if this fails.
	h.SetConfigSavedCallback(app.reloadConfig)

	app.setupRouter()

	return app, nil
}

// GetModelCount returns the number of configured models. It reads through the
// manager so it stays consistent with concurrent config reloads.
func (a *App) GetModelCount() int {
	return a.mgr.ModelCount()
}

// Validate loads the configuration and checks that llama-server is available,
// without constructing the application or logging. Used at startup to fail fast
// (and, in daemon mode, before forking) without building the runtime twice and
// duplicating the startup log lines.
func Validate(configPath string) error {
	if _, err := config.Load(configPath); err != nil {
		return err
	}
	return manager.CheckLlamaServer()
}

// warnIfNoModels logs a warning when no models are configured. An empty model
// set is not fatal: the server stays up so models can be added later through a
// config reload or the web UI.
func (a *App) warnIfNoModels() {
	if a.GetModelCount() == 0 {
		a.logger("Warning: no models are configured; the server will run but cannot serve any model until the configuration is updated")
	}
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
// and the worker registry. Only configuration changes: a running llama.cpp
// process is never restarted or stopped by a reload.
func (a *App) reloadConfig() error {
	a.reloadMu.Lock()
	defer a.reloadMu.Unlock()

	cfg, err := config.Reload(a.configPath)
	if err != nil {
		return err
	}

	a.mgr.ReloadConfigs(indexModels(cfg.Models, a.logger))
	a.fleet.Reload(cfg.Workers)
	a.warnIfNoModels()

	// Apply the proxy setting live (the /v1 routes are gated at request time).
	if enabled := bool(cfg.Proxy); a.proxyEnabled.Swap(enabled) != enabled {
		if enabled {
			a.logger("Proxy enabled via config reload: /v1 routes active")
		} else {
			a.logger("Proxy disabled via config reload: /v1 routes return 404")
		}
	}
	return nil
}

// ProxyEnabled reports whether the OpenAI-compatible /v1 proxy is active.
func (a *App) ProxyEnabled() bool {
	return a.proxyEnabled.Load()
}

func (a *App) setupRouter() {
	r := mux.NewRouter()

	// Rate limiting: 10 requests per second, burst of 20
	rl := newRateLimiter(rate.Limit(10), 20)
	r.Use(rl.limit)

	// Limit request bodies: 1KB is ample for the control API; the config
	// endpoint accepts up to 1MB since a config file with several models
	// easily exceeds 1KB; /v1 prompts may be large (10MB).
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			limit := int64(1024)
			switch {
			case r.URL.Path == "/api/v1/config":
				limit = 1 << 20
			case strings.HasPrefix(r.URL.Path, "/v1/"):
				limit = proxy.MaxBodyBytes
			}
			maxRequestSize(limit)(next).ServeHTTP(w, r)
		})
	})

	api := r.PathPrefix("/api/v1").Subrouter()
	api.Use(a.handler.RequireAPIKey)
	api.HandleFunc("/models", a.handler.ListModels).Methods("GET")
	api.HandleFunc("/models/{model}/start", a.handler.StartModel).Methods("POST")
	api.HandleFunc("/models/{model}/stop", a.handler.StopModel).Methods("DELETE")
	api.HandleFunc("/models/running", a.handler.GetRunningModel).Methods("GET")
	api.HandleFunc("/config", a.handler.GetConfig).Methods("GET")
	api.HandleFunc("/config", a.handler.SaveConfig).Methods("PUT")

	// Coordinator mode: control remote instances. Registered always; with no
	// workers configured the list/fleet views just contain the local machine.
	api.HandleFunc("/workers", a.handler.ListWorkers).Methods("GET")
	api.HandleFunc("/workers/{worker}/models", a.handler.WorkerListModels).Methods("GET")
	api.HandleFunc("/workers/{worker}/system", a.handler.WorkerSystem).Methods("GET")
	api.HandleFunc("/workers/{worker}/models/{model}/start", a.handler.WorkerStartModel).Methods("POST")
	api.HandleFunc("/workers/{worker}/models/{model}/stop", a.handler.WorkerStopModel).Methods("DELETE")
	api.HandleFunc("/fleet", a.handler.GetFleet).Methods("GET")
	api.HandleFunc("/proxy", a.handler.GetProxyStatus).Methods("GET")
	api.HandleFunc("/system", a.handler.GetSystem).Methods("GET")

	// OpenAI-compatible proxy (/v1). Routes are always registered and gated by
	// the runtime flag so toggling "proxy" in the config is live; when off they
	// answer the proxy's OpenAI-format 404.
	v1 := r.PathPrefix("/v1").Subrouter()
	v1.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !a.proxyEnabled.Load() {
				a.proxy.Disabled(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	v1.Use(a.proxy.RequireAuth)
	v1.HandleFunc("/models", a.proxy.ListModels).Methods("GET")
	v1.HandleFunc("/chat/completions", a.proxy.HandleGeneration).Methods("POST")
	v1.HandleFunc("/completions", a.proxy.HandleGeneration).Methods("POST")
	v1.HandleFunc("/embeddings", a.proxy.HandleGeneration).Methods("POST")
	v1.NotFoundHandler = http.HandlerFunc(a.proxy.NotFound)

	a.router = r
}

func (a *App) Start(listenAddr string) error {
	a.httpSrv = &http.Server{
		Addr: listenAddr,
		// CORS is applied as the outermost wrapper (not only on matched
		// routes) so that preflight OPTIONS requests — which mux would answer
		// with 405 before any route-level middleware runs — are handled. A UI
		// served from a different origin (another host/port, or localhost vs
		// 127.0.0.1) otherwise cannot reach the API at all.
		Handler: a.handler.CORS(a.router),
		// ReadHeaderTimeout keeps slowloris protection on headers. Read and
		// write timeouts are disabled: LLM generations stream for minutes and
		// the old 15s WriteTimeout truncated every long response. Control
		// handlers stay guarded by body caps and the rate limiter instead.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       0,
		WriteTimeout:      0,
		IdleTimeout:       60 * time.Second,
	}

	a.registerUIRoutes()
	a.warnIfNoModels()

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
