package main

import (
	"flag"
	"fmt"
	"os"
	"syscall"

	"llamamanager/server"
)

func main() {
	var configPath string
	var listenAddr string
	var enableLogging bool
	var daemonMode bool
	var maxRetries int

	flag.StringVar(&configPath, "config", "llm_config.json", "path to configuration file")
	flag.StringVar(&listenAddr, "listen", ":8080", "address to listen on")
	flag.BoolVar(&enableLogging, "log", false, "enable logging llama-server output to /tmp/llama-server-{model}-{timestamp}.log")
	flag.BoolVar(&daemonMode, "daemon", false, "run in daemon mode (background)")
	flag.IntVar(&maxRetries, "retries", 6, "max automatic restarts on crash (0 to disable)")
	flag.Parse()

	if maxRetries < 0 {
		fmt.Fprintf(os.Stderr, "Configuration error: -retries must be >= 0\n")
		os.Exit(1)
	}

	if err := validateConfigPath(configPath, maxRetries); err != nil {
		fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
		os.Exit(1)
	}

	// If daemon mode is enabled, fork and run in background
	if daemonMode {
		if err := runDaemon(configPath, enableLogging, listenAddr, maxRetries); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to start daemon: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Daemon started successfully")
		return
	}

	// Normal foreground mode
	app, err := server.New(configPath, enableLogging, maxRetries)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create application: %v\n", err)
		os.Exit(1)
	}

	if err := app.Start(listenAddr); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
	}
	os.Remove("/tmp/llm_server_manager.pid")
}

// runDaemon implements daemonization by re-executing the binary
func runDaemon(configPath string, enableLogging bool, listenAddr string, maxRetries int) error {
	// Get the current executable path
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Build args for the daemon instance (without the daemon flag)
	args := []string{}
	for _, arg := range os.Args {
		if arg != "-daemon" && arg != "--daemon" {
			args = append(args, arg)
		}
	}

	devNull, err := os.Open("/dev/null")
	if err != nil {
		return fmt.Errorf("failed to open /dev/null: %w", err)
	}
	defer devNull.Close()

	attr := &os.ProcAttr{
		Files: []*os.File{
			devNull, // stdin
			devNull, // stdout
			devNull, // stderr
		},
		Sys: &syscall.SysProcAttr{
			Setsid: true,
		},
	}

	// Start the process
	proc, err := os.StartProcess(execPath, args, attr)
	if err != nil {
		return fmt.Errorf("failed to start daemon process: %w", err)
	}

	// Get the PID of the daemon process
	pid := proc.Pid

	// Write PID to file under /tmp with restricted permissions
	pidFile := "/tmp/llm_server_manager.pid"
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d\n", pid)), 0600); err != nil {
		return fmt.Errorf("failed to write PID file: %w", err)
	}

	// Parent process exits gracefully
	return nil
}

func validateConfigPath(configPath string, maxRetries int) error {
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return fmt.Errorf("configuration file '%s' does not exist", configPath)
	}

	app, err := server.New(configPath, false, maxRetries)
	if err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	if app.GetModelCount() == 0 {
		return fmt.Errorf("configuration file '%s' contains no model definitions", configPath)
	}

	return nil
}
