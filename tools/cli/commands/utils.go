package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"llm-manager-cli/llmcontrol"
)

// APIResponse matches the API response structure
type APIResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

// PrintResponse outputs the API response
func PrintResponse(w io.Writer, resp *http.Response, statusOK bool, outputJSON bool) error {
	if outputJSON {
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read response: %w", err)
		}
		var prettyJSON map[string]interface{}
		if err := json.Unmarshal(data, &prettyJSON); err != nil {
			return fmt.Errorf("failed to parse JSON: %w", err)
		}
		prettyData, _ := json.MarshalIndent(prettyJSON, "", "  ")
		fmt.Fprintln(w, string(prettyData))
		return nil
	}

	var result llmcontrol.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	if result.Success {
		fmt.Fprintf(w, "Success: %s\n", result.Message)
	} else {
		fmt.Fprintf(w, "Error: %s\n", result.Message)
	}

	return nil
}

// CheckServer checks if the server is reachable
func CheckServer(root *RootCommand) error {
	resp, err := root.DoRequest(http.MethodGet, "/api/v1/models", nil)
	if err != nil {
		return fmt.Errorf("server not reachable: %w", err)
	}
	resp.Body.Close()
	return nil
}
