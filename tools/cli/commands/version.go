package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

type VersionCommand struct{}

func NewVersionCommand() *VersionCommand {
	return &VersionCommand{}
}

func (c *VersionCommand) Cmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Show CLI version",
		RunE:  c.run,
	}
	return cmd
}

func (c *VersionCommand) run(cmd *cobra.Command, args []string) error {
	fmt.Println("LLM Manager CLI v1.0.0")
	return nil
}
