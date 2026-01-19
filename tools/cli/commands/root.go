package commands

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	timeout    = 30 * time.Second
	envAPIKey  = "LLM_MANAGER_API_KEY"
	apiKeyFlag = "api-key"
)

// validAPIKeyPattern matches exactly 16 alphanumeric characters
var validAPIKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9]{16}$`)

type RootCommand struct {
	Cmd            *cobra.Command
	HTTPClient     *http.Client
	TLSClient      *http.Client
	SkipTLSVerify  bool
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
  llm-cli --server=https://remote:8080 list
`,
	}

	// Validate API key at startup and warn if invalid
	apiKey := os.Getenv(envAPIKey)
	if apiKey != "" && !validAPIKeyPattern.MatchString(apiKey) {
		fmt.Fprintf(os.Stderr, "Warning: %s is set but does not match expected format (16 alphanumeric characters)\n", envAPIKey)
	}

	r := &RootCommand{
		Cmd:            cmd,
		HTTPClient:     &http.Client{Timeout: timeout},
		TLSClient:      createTLSClient(),
		SkipTLSVerify:  false,
	}

	r.AddCommands()
	return r
}

// createTLSClient creates a reusable HTTP client that skips TLS verification
func createTLSClient() *http.Client {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	return &http.Client{Transport: tr, Timeout: timeout}
}

func (r *RootCommand) Execute() error {
	return r.Cmd.Execute()
}

func (r *RootCommand) AddCommands() {
	r.Cmd.PersistentFlags().StringP("server", "s", "", "LLM Server address")
	r.Cmd.PersistentFlags().BoolVar(&r.SkipTLSVerify, "skip-tls-verify", false, "Skip TLS certificate verification for HTTPS connections")
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
	server := os.Getenv("LLAMA_SERVER_URL")
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
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

func (r *RootCommand) DoRequest(method, path string, body interface{}) (*http.Response, error) {
	urlStr := r.GetAPIURL(path)

	req, err := http.NewRequest(method, urlStr, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add API key header if environment variable is set and valid
	apiKey := os.Getenv(envAPIKey)
	if apiKey != "" && validAPIKeyPattern.MatchString(apiKey) {
		req.Header.Set(apiKeyFlag, apiKey)
	}

	// Use pre-created TLS client if needed
	var client *http.Client
	if r.SkipTLSVerify && strings.HasPrefix(urlStr, "https://") {
		client = r.TLSClient
	} else {
		client = r.HTTPClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}
