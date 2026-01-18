package commands

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"llm-manager-cli/llmcontrol"

	"github.com/spf13/cobra"
)

type StatusCommand struct {
	root *RootCommand
}

func NewStatusCommand(root *RootCommand) *StatusCommand {
	return &StatusCommand{root: root}
}

func (c *StatusCommand) Cmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "status",
		Short:   "Show the current running model server",
		Aliases: []string{"running", "info"},
		RunE:    c.run,
	}
	return cmd
}

func (c *StatusCommand) run(cmd *cobra.Command, args []string) error {
	resp, err := c.root.DoRequest(http.MethodGet, "/api/v1/models/running", nil)
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
		if result.Message != "" {
			fmt.Println(result.Message)
		}
	} else {
		fmt.Println("No model is running")
	}

	return nil
}
