package commands

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"llm-manager-cli/llmcontrol"

	"github.com/spf13/cobra"
)

type StartCommand struct {
	root *RootCommand
}

func NewStartCommand(root *RootCommand) *StartCommand {
	return &StartCommand{root: root}
}

func (c *StartCommand) Cmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start <model>",
		Short: "Start a model server",
		Long: `Start a model server.

On a coordinator instance, pass --worker to start a model on a remote worker
(by worker name or host) instead of locally.`,
		Args: cobra.ExactArgs(1),
		RunE: c.run,
	}
	cmd.Flags().String("worker", "", "start the model on this worker of a coordinator (name or host)")
	return cmd
}

func (c *StartCommand) run(cmd *cobra.Command, args []string) error {
	modelName := args[0]

	worker, _ := cmd.Flags().GetString("worker")
	var path string
	if worker != "" {
		path = fmt.Sprintf("/api/v1/workers/%s/models/%s/start", worker, modelName)
	} else {
		path = fmt.Sprintf("/api/v1/models/%s/start", modelName)
	}

	resp, err := c.root.DoRequest(http.MethodPost, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	outputJSON, _ := cmd.Flags().GetBool("json")

	if outputJSON {
		return PrintResponse(os.Stdout, resp, true, true)
	}

	var result llmcontrol.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	if result.Success {
		fmt.Printf("Server starting: %s\n", result.Message)
		if server, ok := result.Data.(map[string]interface{}); ok {
			if pid, ok := server["pid"].(float64); ok {
				fmt.Printf("PID: %d\n", int(pid))
			}
			if status, ok := server["status"].(string); ok {
				fmt.Printf("Status: %s\n", status)
			}
		}
	} else {
		fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
	}

	return nil
}
