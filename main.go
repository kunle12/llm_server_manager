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

	flag.StringVar(&configPath, "config", "llm_config.json", "path to configuration file")
	flag.StringVar(&listenAddr, "listen", ":8080", "address to listen on")
	flag.BoolVar(&enableLogging, "log", false, "enable logging llama-server output to /tmp/llama-server-{model}-{timestamp}.log")
	flag.BoolVar(&daemonMode, "daemon", false, "run in daemon mode (background)")
	flag.Parse()

	if err := validateConfigPath(configPath); err != nil {
		fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
		os.Exit(1)
	}

	// If daemon mode is enabled, fork and run in background
	if daemonMode {
		if err := runDaemon(configPath, enableLogging, listenAddr); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to start daemon: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Daemon started successfully")
		return
	}

	// Normal foreground mode
	app, err := server.New(configPath, enableLogging)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create application: %v\n", err)
		os.Exit(1)
	}

	if err := app.Start(listenAddr); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}

// runDaemon implements daemonization by re-executing the binary
func runDaemon(configPath string, enableLogging bool, listenAddr string) error {
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

	// Prepare command attributes
	attr := &os.ProcAttr{
		Files: []*os.File{
			os.Stdin,  // stdin
			os.Stdout, // stdout
			os.Stderr, // stderr
		},
		Sys: &syscall.SysProcAttr{
			Setsid: true, // Create new session
		},
	}

	// Start the process
	_, err = os.StartProcess(execPath, args, attr)
	if err != nil {
		return fmt.Errorf("failed to start daemon process: %w", err)
	}

	return nil
}

func validateConfigPath(configPath string) error {
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return fmt.Errorf("configuration file '%s' does not exist", configPath)
	}

	app, err := server.New(configPath, false)
	if err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	if app.GetModelCount() == 0 {
		return fmt.Errorf("configuration file '%s' contains no model definitions", configPath)
	}

	return nil
}
