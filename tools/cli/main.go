package main

import (
	"fmt"
	"os"

	"llm-manager-cli/commands"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func main() {
	cobra.OnInitialize(initConfig)

	rootCmd := commands.NewRootCommand()

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func initConfig() {
	viper.SetDefault("server", "http://localhost:8080")
	viper.SetEnvPrefix("LLM_MANAGER")
	viper.BindEnv("server", "URL")
}
