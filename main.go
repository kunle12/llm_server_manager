package main

import (
	"flag"
	"fmt"
	"os"

	"llamamanager/server"
)

func main() {
	var configPath string
	var listenAddr string
	var enableLogging bool

	flag.StringVar(&configPath, "config", "llm_config.json", "path to configuration file")
	flag.StringVar(&listenAddr, "listen", ":8080", "address to listen on")
	flag.BoolVar(&enableLogging, "log", false, "enable logging llama-server output to /tmp/llama-server-{model}-{timestamp}.log")
	flag.Parse()

	if err := validateConfigPath(configPath); err != nil {
		fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
		os.Exit(1)
	}

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
