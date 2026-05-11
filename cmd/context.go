package cmd

import (
	"github.com/semaphoreio/agent-cli/pkg/config"
	"github.com/semaphoreio/agent-cli/pkg/output"
	"github.com/spf13/cobra"
)

var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Manage Semaphore contexts (orgs/auth)",
}

var contextListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List all configured contexts",
	Example: "  sem-agent context list\n  sem-agent context list --format table",
	RunE: func(cmd *cobra.Command, args []string) error {
		contexts, err := config.ContextList()
		if err != nil {
			output.Error("config_error", err.Error(), 1)
			return err
		}

		active := config.GetActiveContext()
		type row struct {
			Name   string `json:"name" yaml:"name"`
			Host   string `json:"host" yaml:"host"`
			Active bool   `json:"active" yaml:"active"`
		}
		rows := make([]row, 0, len(contexts))
		for _, c := range contexts {
			rows = append(rows, row{
				Name:   c.Name,
				Host:   c.Host,
				Active: c.Name == active,
			})
		}
		output.Result(rows)
		return nil
	},
}

var contextShowCmd = &cobra.Command{
	Use:     "show",
	Short:   "Show active context details",
	Example: "  sem-agent context show",
	RunE: func(cmd *cobra.Command, args []string) error {
		output.Result(map[string]string{
			"name": config.GetActiveContext(),
			"host": config.GetHost(),
		})
		return nil
	},
}

func init() {
	contextCmd.AddCommand(contextListCmd)
	contextCmd.AddCommand(contextShowCmd)
	rootCmd.AddCommand(contextCmd)
}
