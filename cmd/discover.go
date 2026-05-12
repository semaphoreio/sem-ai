package cmd

import (
	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var discoverCmd = &cobra.Command{
	Use:     "discover",
	Short:   "Introspect all registered commands — capability map for agents",
	Long:    "Returns a structured map of every command, its flags, and examples. Designed for AI agents to self-orient.",
	Example: "  sem-ai discover\n  sem-ai discover --format table",
	RunE: func(cmd *cobra.Command, args []string) error {
		caps := buildCapabilityMap(rootCmd)

		result := map[string]any{
			"commands": caps,
			"tips": map[string]string{
				"setup":       "Run 'sem-ai connect <host> <token>' to authenticate",
				"examples":    "Run 'sem-ai <command> --examples' for usage examples on any command",
				"skills":      "Run 'sem-ai install-skills' to install AI agent skill definitions",
				"debug":       "For CI failures, start with 'sem-ai diagnose <workflow-id>'",
				"format":      "All commands output JSON. Use --format table for human display",
			},
		}

		output.Result(result)
		return nil
	},
}

type capability struct {
	Command     string   `json:"command"`
	Description string   `json:"description"`
	Flags       []string `json:"flags,omitempty"`
	Examples    string   `json:"examples,omitempty"`
	Subcommands []string `json:"subcommands,omitempty"`
}

func buildCapabilityMap(cmd *cobra.Command) []capability {
	var caps []capability
	for _, c := range cmd.Commands() {
		if c.Hidden {
			continue
		}
		cap := capability{
			Command:     c.CommandPath(),
			Description: c.Short,
			Examples:    c.Example,
		}

		var flags []string
		c.NonInheritedFlags().VisitAll(func(f *pflag.Flag) {
			flags = append(flags, "--"+f.Name)
		})
		if len(flags) > 0 {
			cap.Flags = flags
		}

		if c.HasSubCommands() {
			var subs []string
			for _, sub := range c.Commands() {
				if !sub.Hidden {
					subs = append(subs, sub.Name())
				}
			}
			cap.Subcommands = subs
			caps = append(caps, cap)
			caps = append(caps, buildCapabilityMap(c)...)
		} else {
			caps = append(caps, cap)
		}
	}
	return caps
}

func init() {
	rootCmd.AddCommand(discoverCmd)
}
