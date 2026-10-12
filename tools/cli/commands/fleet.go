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

type FleetCommand struct {
	root *RootCommand
}

func NewFleetCommand(root *RootCommand) *FleetCommand {
	return &FleetCommand{root: root}
}

func (c *FleetCommand) Cmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fleet",
		Short: "Show the coordinator's fleet view (local machine + workers)",
		Long: `Show the aggregated fleet view of a coordinator instance: the local
machine first, then every configured worker with its health, running model,
and configured model count.

On a standalone instance (no workers configured) this shows just the local
machine.`,
		RunE: c.run,
	}
	return cmd
}

func (c *FleetCommand) run(cmd *cobra.Command, args []string) error {
	resp, err := c.root.DoRequest(http.MethodGet, "/api/v1/fleet", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	outputJSON, _ := cmd.Flags().GetBool("json")
	if outputJSON {
		return PrintResponse(os.Stdout, resp, true, true)
	}

	var payload struct {
		Success bool                     `json:"success"`
		Data    llmcontrol.FleetResponse `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "MACHINE\tSTATUS\tRUNNING\tPORT\tMODELS\t")
	for _, m := range payload.Data.Machines {
		running, port := "-", ""
		if m.Running != nil {
			running = m.Running.Alias
			if running == "" {
				running = m.Running.Name
			}
			port = fmt.Sprintf("%d", m.Running.Port)
		}
		status := m.Status
		if m.Status != "online" && m.Error != "" {
			status = fmt.Sprintf("%s (%s)", m.Status, m.Error)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t\n", m.Machine, status, running, port, len(m.Models))
	}
	w.Flush()

	return nil
}
