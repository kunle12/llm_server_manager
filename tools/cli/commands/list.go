package commands

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"text/tabwriter"

	"llm-manager-cli/llmcontrol"

	"github.com/spf13/cobra"
)

type ListCommand struct {
	root *RootCommand
}

func NewListCommand(root *RootCommand) *ListCommand {
	return &ListCommand{root: root}
}

func (c *ListCommand) Cmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List all configured models",
		Aliases: []string{"ls", "models"},
		RunE:    c.run,
	}

	cmd.Flags().BoolP("short", "S", false, "Show only model names")
	return cmd
}

func (c *ListCommand) run(cmd *cobra.Command, args []string) error {
	resp, err := c.root.DoRequest(http.MethodGet, "/api/v1/models", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	outputJSON, _ := cmd.Flags().GetBool("json")
	short, _ := cmd.Flags().GetBool("short")

	if outputJSON {
		return PrintResponse(os.Stdout, resp, true, true)
	}

	// Decode the wrapped API response
	var apiResp llmcontrol.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	// Extract models from the data field
	data, ok := apiResp.Data.(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid response format")
	}

	modelsData, ok := data["models"].([]interface{})
	if !ok {
		return fmt.Errorf("invalid models format")
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	if short {
		for _, m := range modelsData {
			model := m.(map[string]interface{})
			fmt.Fprintln(w, model["name"])
		}
	} else {
		fmt.Fprintln(w, "NAME\tMODEL PATH\tCONTEXT SIZE\tTEMPERATURE\tTHREADS\tACTIVE")
		for _, m := range modelsData {
			model := m.(map[string]interface{})
			activeStr := "false"
			if active, ok := model["active"].(bool); ok && active {
				activeStr = "true"
			}
			modelPath := fmt.Sprintf("%v", model["model_path"])
			contextSize := "N/A"
			if v, ok := model["context_size"].(float64); ok {
				contextSize = fmt.Sprintf("%d", int(v))
			}
			temperature := "N/A"
			if v, ok := model["temperature"].(float64); ok {
				temperature = fmt.Sprintf("%.2f", v)
			}
			threads := "N/A"
			if v, ok := model["threads"].(float64); ok {
				threads = fmt.Sprintf("%d", int(v))
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
				model["name"],
				modelPath,
				contextSize,
				temperature,
				threads,
				activeStr)
		}
	}
	w.Flush()
	return nil
}
