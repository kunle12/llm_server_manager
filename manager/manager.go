package manager

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	stopOnce      sync.Once
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

	_, cancel := context.WithCancel(context.Background())

	return &ServerManager{
		configs:       configs,
		logger:        logger,
		llamaPath:     llamaPath,
		enableLogging: enableLogging,
		maxRetries:    maxRetries,
		stopChan:      make(chan struct{}),
		cancelFunc:    cancel,
	}, nil
}

func (sm *ServerManager) ListModels() map[string]*models.ModelConfig {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()

	models := make(map[string]*models.ModelConfig, len(sm.configs))
	for name, cfg := range sm.configs {
		models[name] = cfg
	}
	return models
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

	if sm.server != nil {
		switch sm.server.Status {
		case models.StatusRunning, models.StatusStarting, models.StatusStopping:
			return fmt.Errorf("a server is already %s with model: %s", sm.server.Status, sm.server.ModelConfig.Name)
		}
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
	sm.cancelFunc = cancel

	sm.server = &models.RunningServer{
		ModelConfig: *config,
		Status:      models.StatusStarting,
		StartTime:   time.Now(),
		CrashCount:  0,
	}

	go sm.launchServer(ctx, config)

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

	if config.LaunchCmd == nil || *config.LaunchCmd == "" {
		if config.Threads <= 0 {
			return fmt.Errorf("threads must be positive")
		}

		if config.Temperature < 0 || config.Temperature > 2 {
			return fmt.Errorf("temperature must be between 0 and 2")
		}
	}

	return nil
}

func (sm *ServerManager) launchServer(ctx context.Context, config *models.ModelConfig) {
	sm.mutex.Lock()
	sm.server.Status = models.StatusStarting
	sm.mutex.Unlock()

	custom := config.LaunchCmd != nil && *config.LaunchCmd != ""
	if custom {
		sm.logger("Starting custom command for model: %s", config.Name)
	} else {
		sm.logger("Starting llama.cpp server for model: %s", config.Name)
	}

	crashCount := 0
	registeredPID := 0

	for {
		select {
		case <-ctx.Done():
			sm.logger("Server context cancelled for model: %s", config.Name)
			sm.clearServerStateIfPIDMatches(registeredPID)
			return
		default:
		}

		var cmd *exec.Cmd
		if custom {
			cmd = exec.Command("bash", "-c", *config.LaunchCmd)
			// Run the wrapper in its own process group so StopCurrent can
			// kill the whole group (including any llama-server children).
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		} else {
			cmd = sm.buildCommand(config)
		}

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
			sm.closeLogFile()
			sm.clearServerStateIfPIDMatches(registeredPID)
			return
		}

		pid := cmd.Process.Pid
		sm.logger("Server started successfully with PID: %d", pid)

		// StopCurrent may have run while we were starting (it saw PID 0 and
		// skipped the kill). If the context is cancelled now, kill the orphan
		// process group and bail out.
		select {
		case <-ctx.Done():
			sm.logger("Server context cancelled after start, killing orphan PID: %d", pid)
			killProcessGroup(pid)
			sm.closeLogFile()
			sm.clearServerStateIfPIDMatches(pid)
			return
		default:
		}

		sm.mutex.Lock()
		// Claim the server state only if it still belongs to this launcher:
		// PID 0 on a fresh start, or our own previously-registered PID on a
		// restart after a crash. Anything else means the state was cleared or
		// replaced by a concurrent StartModel/StopCurrent.
		if sm.server != nil && (sm.server.PID == 0 || sm.server.PID == registeredPID) {
			sm.server.PID = pid
			sm.server.Status = models.StatusRunning
			sm.server.CrashCount = crashCount
			sm.mutex.Unlock()
			registeredPID = pid
		} else {
			sm.mutex.Unlock()
			sm.logger("Server state missing after start, killing orphan PID: %d", pid)
			killProcessGroup(pid)
			sm.closeLogFile()
			return
		}

		waitErr := cmd.Wait()
		sm.closeLogFile()

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
				if sm.server != nil && sm.server.PID == pidCopy {
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

// closeLogFile closes and releases the current log file handle
func (sm *ServerManager) closeLogFile() {
	if sm.logFile != nil {
		sm.logFile.Close()
		sm.logFile = nil
	}
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

	cmd := exec.Command(sm.llamaPath, args...)
	// Run the server in its own process group so StopCurrent can kill the
	// whole group (including any child processes) without hitting the manager.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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
	return sm.StopModel("")
}

// StopModel stops the running server, optionally requiring it to be the
// given model. The running-model check happens atomically under the lock,
// avoiding the check-then-act race between reading state and stopping it.
func (sm *ServerManager) StopModel(modelName string) error {
	sm.mutex.Lock()

	if sm.server == nil || (sm.server.Status != models.StatusRunning && sm.server.Status != models.StatusStarting) {
		sm.mutex.Unlock()
		return fmt.Errorf("no model is currently running")
	}

	if modelName != "" && sm.server.ModelConfig.Name != modelName {
		sm.mutex.Unlock()
		return fmt.Errorf("server is running model '%s', not '%s'", sm.server.ModelConfig.Name, modelName)
	}

	pid := sm.server.PID
	sm.server.Status = models.StatusStopping

	sm.mutex.Unlock()

	sm.cancelFunc()

	// Kill the server's process group. PID 0 means the process hasn't
	// started yet; killing PID 0 would target the manager's own process
	// group (suicide), so we skip and rely on the launcher's ctx.Done()
	// check to kill whatever it spawns once the context is cancelled.
	if pid > 0 {
		if err := killProcessGroup(pid); err != nil {
			sm.logger("Warning: failed to kill process group: %v", err)
		} else {
			sm.logger("Server process group killed for PID: %d", pid)
		}
	}

	sm.clearServerStateIfPIDMatches(pid)

	return nil
}

// killProcessGroup sends SIGKILL to the process group led by pid, which
// terminates the server and any child processes it spawned.
func killProcessGroup(pid int) error {
	return syscall.Kill(-pid, syscall.SIGKILL)
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

// CloseStopChan closes the stop channel. Safe to call multiple times.
func (sm *ServerManager) CloseStopChan() {
	sm.stopOnce.Do(func() {
		close(sm.stopChan)
	})
}
