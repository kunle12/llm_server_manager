package manager

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
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
	maxRetries    int
	stopChan      chan struct{}
	cancelCtx     context.Context
	cancelFunc    context.CancelFunc
	logFile       *os.File
}

func New(configs map[string]*models.ModelConfig, logger func(format string, args ...interface{}), enableLogging bool, maxRetries int) (*ServerManager, error) {
	llamaPath := getLlamaServerPath()
	if err := validateLlamaServerPath(llamaPath); err != nil {
		return nil, err
	}

	if maxRetries < 0 {
		maxRetries = 0
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &ServerManager{
		configs:       configs,
		logger:        logger,
		llamaPath:     llamaPath,
		enableLogging: enableLogging,
		maxRetries:    maxRetries,
		stopChan:      make(chan struct{}),
		cancelCtx:     ctx,
		cancelFunc:    cancel,
	}, nil
}

func (sm *ServerManager) ListModels() map[string]*models.ModelConfig {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()
	return sm.configs
}

func (sm *ServerManager) GetCurrentServer() *models.RunningServer {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	server := sm.server
	if server == nil {
		return nil
	}

	if server.Status == models.StatusRunning && !isProcessAlive(server.PID) {
		sm.server = nil
		return nil
	}

	return server
}

func isProcessAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil
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

	if err := validateModelConfig(config); err != nil {
		return err
	}

	if sm.cancelFunc != nil {
		sm.cancelFunc()
	}

	ctx, cancel := context.WithCancel(context.Background())

	sm.server = &models.RunningServer{
		ModelConfig: *config,
		Status:      models.StatusStarting,
		StartTime:   time.Now(),
		CrashCount:  0,
	}

	go sm.launchServer(ctx, config, cancel)

	return nil
}

func validateModelConfig(config *models.ModelConfig) error {
	if config.Name == "" {
		return fmt.Errorf("model name cannot be empty")
	}

	if config.LaunchCmd == nil || *config.LaunchCmd == "" {
		if config.ModelPath == "" {
			return fmt.Errorf("model_path is required when launch_cmd is not set")
		}
		if err := validateModelFile(config.ModelPath); err != nil {
			return fmt.Errorf("model file validation failed: %w", err)
		}
	}

	if config.Threads <= 0 {
		return fmt.Errorf("threads must be positive")
	}

	if config.Temperature < 0 || config.Temperature > 2 {
		return fmt.Errorf("temperature must be between 0 and 2")
	}

	return nil
}

func (sm *ServerManager) launchServer(ctx context.Context, config *models.ModelConfig, cancelFunc context.CancelFunc) {
	sm.mutex.Lock()
	sm.server.Status = models.StatusStarting
	sm.mutex.Unlock()

	if config.LaunchCmd != nil && *config.LaunchCmd != "" {
		sm.logger("Starting custom command for model: %s", config.Name)
		sm.launchCustomCommand(ctx, config, cancelFunc)
		return
	}

	sm.logger("Starting llama.cpp server for model: %s", config.Name)

	crashCount := 0

	for {
		select {
		case <-ctx.Done():
			sm.logger("Server context cancelled for model: %s", config.Name)
			sm.clearServerState()
			return
		default:
		}

		cmd := sm.buildCommand(config)

		if sm.enableLogging {
			if sm.logFile != nil {
				sm.logFile.Close()
			}
			logFile, err := sm.createLogFile(config.Name)
			if err != nil {
				sm.logger("Warning: failed to create log file: %v", err)
			} else {
				sm.logFile = logFile
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

		waitErr := cmd.Wait()

		if sm.logFile != nil {
			sm.logFile.Close()
			sm.logFile = nil
		}

		pidCopy := pid

		select {
		case <-ctx.Done():
			sm.logger("Server context cancelled for model: %s", config.Name)
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
		"-t", fmt.Sprintf("%d", config.Threads),
		"--temp", fmt.Sprintf("%f", config.Temperature),
		"--no-webui",
		"--host", "0.0.0.0",
		"--port", fmt.Sprintf("%d", port),
	}

	if config.ContextSize != nil && *config.ContextSize > 0 {
		args = append(args, "-c", fmt.Sprintf("%d", *config.ContextSize))
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

	if config.SpecDraftNMax != nil && *config.SpecDraftNMax > 0 {
		args = append(args, "--spec-type", "draft-mtp")
		args = append(args, "--spec-draft-n-max", fmt.Sprintf("%d", *config.SpecDraftNMax))
	}

	if runtime.GOOS == "darwin" {
		args = append(args, "--cache-ram", "0")
	}

	cmd := exec.Command(sm.llamaPath, args...)
	return cmd
}

func (sm *ServerManager) launchCustomCommand(ctx context.Context, config *models.ModelConfig, cancelFunc context.CancelFunc) {
	crashCount := 0

	for {
		select {
		case <-ctx.Done():
			sm.logger("Custom command context cancelled for model: %s", config.Name)
			sm.clearServerState()
			return
		default:
		}

		cmd := exec.Command("bash", "-c", *config.LaunchCmd)

		if sm.enableLogging {
			if sm.logFile != nil {
				sm.logFile.Close()
			}
			logFile, err := sm.createLogFile(config.Name)
			if err != nil {
				sm.logger("Warning: failed to create log file: %v", err)
			} else {
				sm.logFile = logFile
				cmd.Stdout = logFile
				cmd.Stderr = logFile
			}
		} else {
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
		}

		if err := cmd.Start(); err != nil {
			sm.logger("Failed to start custom command: %v", err)
			sm.clearServerState()
			return
		}

		pid := cmd.Process.Pid
		sm.logger("Custom command started successfully with PID: %d", pid)

		sm.mutex.Lock()
		if sm.server != nil {
			sm.server.PID = pid
			sm.server.Status = models.StatusRunning
			sm.server.CrashCount = crashCount
		}
		sm.mutex.Unlock()

		waitErr := cmd.Wait()

		if sm.logFile != nil {
			sm.logFile.Close()
			sm.logFile = nil
		}

		pidCopy := pid

		select {
		case <-ctx.Done():
			sm.logger("Custom command context cancelled for model: %s", config.Name)
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
				sm.logger("Custom command crashed (exit code %d), restarting in %v (attempt %d/%d)",
					exitCode, delay, crashCount, sm.maxRetries)

				sm.mutex.Lock()
				if sm.server != nil {
					sm.server.Status = models.StatusStarting
				}
				sm.mutex.Unlock()

				time.Sleep(delay)
				continue
			}

			if crashCount > sm.maxRetries && sm.maxRetries > 0 {
				sm.logger("Custom command crashed (exit code %d), max restart attempts (%d) reached",
					exitCode, sm.maxRetries)
			} else {
				sm.logger("Custom command process exited with error: %v", waitErr)
			}
		} else {
			sm.logger("Custom command process exited cleanly")
		}

		sm.clearServerStateIfPIDMatches(pidCopy)
		return
	}
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

	if sm.server == nil || (sm.server.Status != models.StatusRunning && sm.server.Status != models.StatusStarting) {
		sm.mutex.Unlock()
		return fmt.Errorf("no model is currently running")
	}

	pid := sm.server.PID
	sm.server.Status = models.StatusStopping

	sm.mutex.Unlock()

	sm.cancelFunc()

	p, err := os.FindProcess(pid)
	if err != nil {
		sm.logger("Failed to find process: %v", err)
	} else {
		if err := p.Kill(); err != nil {
			sm.logger("Warning: failed to kill process: %v", err)
		} else {
			sm.logger("Server process killed for PID: %d", pid)
		}
	}

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
