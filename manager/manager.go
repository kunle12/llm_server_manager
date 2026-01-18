package manager

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"llamamanager/models"
)

type ServerManager struct {
	configs       map[string]*models.ModelConfig
	server        *models.RunningServer
	mutex         sync.RWMutex
	logger        func(format string, args ...interface{})
	llamaPath     string
	enableLogging bool
	// Channel to signal goroutine to stop
	stopChan chan struct{}
}

func New(configs map[string]*models.ModelConfig, logger func(format string, args ...interface{}), enableLogging bool) (*ServerManager, error) {
	llamaPath := getLlamaServerPath()
	if err := validateLlamaServerPath(llamaPath); err != nil {
		return nil, err
	}

	return &ServerManager{
		configs:       configs,
		logger:        logger,
		llamaPath:     llamaPath,
		enableLogging: enableLogging,
		stopChan:      make(chan struct{}),
	}, nil
}

func (sm *ServerManager) ListModels() map[string]*models.ModelConfig {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()
	return sm.configs
}

func (sm *ServerManager) GetCurrentServer() *models.RunningServer {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()
	return sm.server
}

func (sm *ServerManager) StartModel(modelName string) error {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	if sm.server != nil && sm.server.Status == models.StatusRunning {
		return fmt.Errorf("a server is already running with model: %s", sm.server.ModelConfig.Name)
	}

	config, exists := sm.configs[modelName]
	if !exists {
		return fmt.Errorf("model '%s' not found in configuration", modelName)
	}

	if err := validateModelFile(config.ModelPath); err != nil {
		return fmt.Errorf("model file validation failed: %w", err)
	}

	sm.server = &models.RunningServer{
		ModelConfig: *config,
		Status:      models.StatusStarting,
		StartTime:   time.Now(),
	}

	done := make(chan struct{})
	go sm.launchServer(config, done)

	return nil
}

func (sm *ServerManager) launchServer(config *models.ModelConfig, done chan struct{}) {
	// Update status to starting
	sm.mutex.Lock()
	sm.server.Status = models.StatusStarting
	sm.mutex.Unlock()

	sm.logger("Starting llama.cpp server for model: %s", config.Name)

	cmd := sm.buildCommand(config)

	if sm.enableLogging {
		logFile, err := sm.createLogFile(config.Name)
		if err != nil {
			sm.logger("Warning: failed to create log file: %v", err)
		} else {
			cmd.Stdout = logFile
			cmd.Stderr = logFile
		}
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}

	if err := cmd.Start(); err != nil {
		sm.logger("Failed to start server: %v", err)
		sm.clearServerState()
		close(done)
		return
	}

	pid := cmd.Process.Pid
	sm.logger("Server started successfully with PID: %d", pid)

	// Update PID and status atomically
	sm.mutex.Lock()
	if sm.server != nil {
		sm.server.PID = pid
		sm.server.Status = models.StatusRunning
	}
	sm.mutex.Unlock()
	close(done)

	// Wait for process to complete
	err := cmd.Wait()
	if err != nil {
		sm.logger("Server process exited with error: %v", err)
	}

	// Clear server state if this is still the active server
	sm.clearServerStateIfPIDMatches(pid)

	sm.logger("Server stopped for model: %s", config.Name)
}

// clearServerStateIfPIDMatches clears server state if PID matches
func (sm *ServerManager) clearServerStateIfPIDMatches(pid int) {
	sm.mutex.Lock()
	if sm.server != nil && sm.server.PID == pid {
		sm.server = nil
	}
	sm.mutex.Unlock()
}

// clearServerState clears the server state
func (sm *ServerManager) clearServerState() {
	sm.mutex.Lock()
	sm.server = nil
	sm.mutex.Unlock()
}

func (sm *ServerManager) buildCommand(config *models.ModelConfig) *exec.Cmd {
	port := 8081
	if config.Port != nil {
		port = *config.Port
	}

	args := []string{
		"-m", config.ModelPath,
		"-c", fmt.Sprintf("%d", config.ContextSize),
		"--temp", fmt.Sprintf("%f", config.Temperature),
		"-t", fmt.Sprintf("%d", config.Threads),
		"--no-webui",
		"--host", "0.0.0.0",
		"--port", fmt.Sprintf("%d", port),
	}

	if !sm.enableLogging {
		args = append(args, "--log-disable")
	}

	if config.Mmproj != nil && *config.Mmproj != "" {
		if err := validateMmprojFile(*config.Mmproj); err != nil {
			sm.logger("Warning: mmproj validation failed: %v", err)
		} else {
			args = append(args, "--mmproj", *config.Mmproj)
		}
	}

	cmd := exec.Command(sm.llamaPath, args...)
	return cmd
}

func getLlamaServerPath() string {
	if path := os.Getenv("LLAMA_SERVER_PATH"); path != "" {
		return path
	}
	execPath, _ := os.Executable()
	return filepath.Join(filepath.Dir(execPath), "llama-server")
}

func validateLlamaServerPath(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("llama-server executable not found at: %s (set LLAMA_SERVER_PATH environment variable)", path)
	}
	return nil
}

func validateModelFile(modelPath string) error {
	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		return fmt.Errorf("model file not found: %s", modelPath)
	}
	return nil
}

func validateMmprojFile(mmprojPath string) error {
	if _, err := os.Stat(mmprojPath); os.IsNotExist(err) {
		return fmt.Errorf("mmproj file not found: %s", mmprojPath)
	}
	return nil
}

func (sm *ServerManager) createLogFile(modelName string) (*os.File, error) {
	timestamp := time.Now().Format("20060102-150405")
	safeModelName := strings.ReplaceAll(modelName, "/", "-")
	logFileName := fmt.Sprintf("llama-server-%s-%s.log", safeModelName, timestamp)
	logPath := filepath.Join("/tmp", logFileName)

	file, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create log file: %w", err)
	}

	sm.logger("Logging llama-server output to: %s", logPath)
	return file, nil
}

func (sm *ServerManager) StopCurrent() error {
	sm.mutex.Lock()

	if sm.server == nil || sm.server.Status != models.StatusRunning {
		sm.mutex.Unlock()
		return fmt.Errorf("no model is currently running")
	}

	pid := sm.server.PID
	sm.server.Status = models.StatusStopping

	sm.mutex.Unlock()

	// Kill the process
	p, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find process: %w", err)
	}

	if err := p.Kill(); err != nil {
		return fmt.Errorf("failed to kill process: %w", err)
	}

	sm.logger("Server process killed for PID: %d", pid)

	// Clear server state
	sm.clearServerStateIfPIDMatches(pid)

	return nil
}

// ReloadConfigs updates the manager's configurations with new ones
func (sm *ServerManager) ReloadConfigs(newConfigs map[string]*models.ModelConfig) {
	sm.mutex.Lock()
	sm.configs = newConfigs
	sm.mutex.Unlock()
	sm.logger("Configuration reloaded successfully")
}

// GetStopChan returns the stop channel for the watcher
func (sm *ServerManager) GetStopChan() <-chan struct{} {
	return sm.stopChan
}

// CloseStopChan closes the stop channel
func (sm *ServerManager) CloseStopChan() {
	close(sm.stopChan)
}
