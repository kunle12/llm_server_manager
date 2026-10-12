package commands

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/spf13/cobra"
)

type StopCommand struct {
	root *RootCommand
}

func NewStopCommand(root *RootCommand) *StopCommand {
	return &StopCommand{root: root}
}

func (c *StopCommand) Cmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop <model>",
		Short: "Stop a running model server",
		Long: `Stop a running model server.

On a coordinator instance, pass --worker to stop a model on a remote worker
(by worker name or host) instead of locally.`,
		Args: cobra.ExactArgs(1),
		RunE: c.run,
	}
	cmd.Flags().String("worker", "", "stop the model on this worker of a coordinator (name or host)")
	return cmd
}

func (c *StopCommand) run(cmd *cobra.Command, args []string) error {
	modelName := args[0]

	worker, _ := cmd.Flags().GetString("worker")
	var path string
	if worker != "" {
		path = fmt.Sprintf("/api/v1/workers/%s/models/%s/stop", worker, modelName)
	} else {
		path = fmt.Sprintf("/api/v1/models/%s/stop", modelName)
	}

	resp, err := c.root.DoRequest(http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	outputJSON, _ := cmd.Flags().GetBool("json")

	if outputJSON {
		return PrintResponse(os.Stdout, resp, true, true)
	}

	var result APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	if result.Success {
		fmt.Printf("Stopped: %s\n", result.Message)
	} else {
		fmt.Fprintf(os.Stderr, "Error: %s\n", result.Message)
	}

	return nil
}
