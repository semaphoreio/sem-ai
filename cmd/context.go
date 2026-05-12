package cmd

import (
	"fmt"

	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Manage Semaphore contexts (orgs/auth)",
}

var contextListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List all configured contexts",
	Example: "  sem-ai context list\n  sem-ai context list --format table",
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
	Example: "  sem-ai context show",
	RunE: func(cmd *cobra.Command, args []string) error {
		output.Result(map[string]string{
			"name": config.GetActiveContext(),
			"host": config.GetHost(),
		})
		return nil
	},
}

var contextSwitchCmd = &cobra.Command{
	Use:   "switch [name-or-number]",
	Short: "Switch active context",
	Args:  cobra.MaximumNArgs(1),
	Example: `  sem-ai context switch
  sem-ai context switch myorg_semaphoreci_com
  sem-ai context switch 1`,
	RunE: func(cmd *cobra.Command, args []string) error {
		contexts, err := config.ContextList()
		if err != nil {
			output.Error("config_error", err.Error(), 1)
			return err
		}
		if len(contexts) == 0 {
			output.Error("config_error", "no contexts configured — run 'sem-ai connect' first", 1)
			return fmt.Errorf("no contexts")
		}

		active := config.GetActiveContext()

		if len(args) == 0 {
			type row struct {
				Number int    `json:"number"`
				Name   string `json:"name"`
				Host   string `json:"host"`
				Active bool   `json:"active"`
			}
			rows := make([]row, 0, len(contexts))
			for i, c := range contexts {
				rows = append(rows, row{
					Number: i + 1,
					Name:   c.Name,
					Host:   c.Host,
					Active: c.Name == active,
				})
			}
			output.Result(map[string]any{
				"message":  "pass a name or number to switch",
				"contexts": rows,
			})
			return nil
		}

		target := args[0]

		// Try as number first
		if n := 0; true {
			if _, err := fmt.Sscanf(target, "%d", &n); err == nil && n >= 1 && n <= len(contexts) {
				target = contexts[n-1].Name
			}
		}

		token := viper.GetString(fmt.Sprintf("contexts.%s.auth.token", target))
		if token == "" {
			output.Error("not_found", fmt.Sprintf("context %q not found", target), 404)
			return fmt.Errorf("context not found")
		}
		viper.Set("active-context", target)
		if err := viper.WriteConfig(); err != nil {
			output.Error("config_error", err.Error(), 1)
			return err
		}
		config.Load()
		output.Result(map[string]string{
			"status":  "switched",
			"context": target,
			"host":    config.GetHost(),
		})
		return nil
	},
}

func init() {
	contextCmd.AddCommand(contextListCmd)
	contextCmd.AddCommand(contextShowCmd)
	contextCmd.AddCommand(contextSwitchCmd)
	rootCmd.AddCommand(contextCmd)
}
