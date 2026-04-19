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
	configs        map[string]*models.ModelConfig
	server         *models.RunningServer
	mutex          sync.RWMutex
	logger         func(format string, args ...interface{})
	llamaPath      string
	enableLogging  bool
	maxRetries     int
	stopChan       chan struct{}
	serverStopChan chan chan struct{}
}

func New(configs map[string]*models.ModelConfig, logger func(format string, args ...interface{}), enableLogging bool, maxRetries int) (*ServerManager, error) {
	llamaPath := getLlamaServerPath()
	if err := validateLlamaServerPath(llamaPath); err != nil {
		return nil, err
	}

	if maxRetries < 0 {
		maxRetries = 0
	}

	return &ServerManager{
		configs:        configs,
		logger:         logger,
		llamaPath:      llamaPath,
		enableLogging:  enableLogging,
		maxRetries:     maxRetries,
		stopChan:       make(chan struct{}),
		serverStopChan: make(chan chan struct{}),
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
		CrashCount:  0,
	}

	stopChan := make(chan struct{})
	sm.serverStopChan <- stopChan

	done := make(chan struct{})
	go sm.launchServer(config, done, stopChan)

	return nil
}

func (sm *ServerManager) launchServer(config *models.ModelConfig, done chan struct{}, stopChan chan struct{}) {
	sm.mutex.Lock()
	sm.server.Status = models.StatusStarting
	sm.mutex.Unlock()

	sm.logger("Starting llama.cpp server for model: %s", config.Name)

	crashCount := 0

	for {
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

		sm.mutex.Lock()
		if sm.server != nil {
			sm.server.PID = pid
			sm.server.Status = models.StatusRunning
			sm.server.CrashCount = crashCount
		}
		sm.mutex.Unlock()
		close(done)

		waitErr := cmd.Wait()
		pidCopy := pid

		select {
		case <-stopChan:
			sm.logger("Server stopped for model: %s", config.Name)
			sm.clearServerStateIfPIDMatches(pidCopy)
			return
		default:
		}

		if waitErr != nil {
			exitCode := -1
			if cmd.ProcessState != nil {
				exitCode = cmd.ProcessState.ExitCode()
			}

			crashCount++
			if sm.maxRetries > 0 && crashCount <= sm.maxRetries {
				delay := time.Duration(crashCount) * time.Second
				if delay > 10*time.Second {
					delay = 10 * time.Second
				}
				sm.logger("Server crashed (exit code %d), restarting in %v (attempt %d/%d)",
					exitCode, delay, crashCount, sm.maxRetries)

				sm.mutex.Lock()
				if sm.server != nil {
					sm.server.Status = models.StatusStarting
				}
				sm.mutex.Unlock()

				time.Sleep(delay)
				done = make(chan struct{})
				continue
			}

			if crashCount > sm.maxRetries && sm.maxRetries > 0 {
				sm.logger("Server crashed (exit code %d), max restart attempts (%d) reached",
					exitCode, sm.maxRetries)
			} else {
				sm.logger("Server process exited with error: %v", waitErr)
			}
		} else {
			sm.logger("Server process exited cleanly")
		}

		sm.clearServerStateIfPIDMatches(pidCopy)
		return
	}
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
		"-a", config.Name,
		"-m", config.ModelPath,
		"-c", fmt.Sprintf("%d", config.ContextSize),
		"-t", fmt.Sprintf("%d", config.Threads),
		"--temp", fmt.Sprintf("%f", config.Temperature),
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
	if config.TopK != nil && *config.TopK > 0 {
		args = append(args, "--top-k", fmt.Sprintf("%d", *config.TopK))
	}
	if config.TopP != nil && *config.TopP > 0.0 && *config.TopP <= 1.0 {
		args = append(args, "--top-p", fmt.Sprintf("%f", *config.TopP))
	}

	if config.ChatTemplateKwargs != nil && *config.ChatTemplateKwargs != "" {
		args = append(args, "--chat-template-kwargs", *config.ChatTemplateKwargs)
	}

	if config.Ngl != nil && *config.Ngl > 0 {
		args = append(args, "-ngl", fmt.Sprintf("%d", *config.Ngl))
	}

	if config.Mmap != nil && !*config.Mmap {
		args = append(args, "--no-mmap")
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

	select {
	case stopChan := <-sm.serverStopChan:
		close(stopChan)
	default:
	}

	p, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find process: %w", err)
	}

	if err := p.Kill(); err != nil {
		return fmt.Errorf("failed to kill process: %w", err)
	}

	sm.logger("Server process killed for PID: %d", pid)

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
