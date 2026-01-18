package commands

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/spf13/cobra"
)

const timeout = 30 * time.Second

type RootCommand struct {
	Cmd        *cobra.Command
	HTTPClient *http.Client
}

func NewRootCommand() *RootCommand {
	cmd := &cobra.Command{
		Use:   "llm-cli",
		Short: "CLI tool to manage LLM Server Manager remotely",
		Long: `A CLI tool to interact with LLM Server Manager via its REST API.

Examples:
  llm-cli --server=http://localhost:8080 list
  llm-cli --server=:8080 start llama-7b
  llm-cli stop llama-7b
`,
	}

	r := &RootCommand{
		Cmd:        cmd,
		HTTPClient: &http.Client{Timeout: timeout},
	}

	r.AddCommands()
	return r
}

func (r *RootCommand) Execute() error {
	return r.Cmd.Execute()
}

func (r *RootCommand) AddCommands() {
	r.Cmd.PersistentFlags().StringP("server", "s", "", "LLM Server address")
	r.Cmd.Flags().BoolP("json", "j", false, "Output in JSON format")

	r.Cmd.AddCommand(
		NewListCommand(r).Cmd(),
		NewStartCommand(r).Cmd(),
		NewStopCommand(r).Cmd(),
		NewStatusCommand(r).Cmd(),
		NewVersionCommand().Cmd(),
	)
}

func (r *RootCommand) GetServerURL() string {
	// CLI flag takes priority
	flag := r.Cmd.Flag("server")
	if flag != nil && flag.Changed {
		server := flag.Value.String()
		if !hasScheme(server) {
			server = "http://" + server
		}
		return server
	}
	// Fall back to environment variable or default
	server := os.Getenv("LLM_MANAGER_URL")
	if server == "" {
		server = "http://localhost:8080"
	}
	if !hasScheme(server) {
		server = "http://" + server
	}
	return server
}

func (r *RootCommand) GetAPIURL(path string) string {
	baseURL := r.GetServerURL()
	u, err := url.Parse(baseURL)
	if err != nil {
		return baseURL + path
	}
	u.Path = path
	return u.String()
}

func hasScheme(s string) bool {
	return len(s) >= 7 && (s[:7] == "http://" || s[:8] == "https://")
}

func (r *RootCommand) DoRequest(method, path string, body interface{}) (*http.Response, error) {
	url := r.GetAPIURL(path)
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := r.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}
