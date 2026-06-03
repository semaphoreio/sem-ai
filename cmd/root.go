package cmd

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/semaphoreio/sem-ai/pkg/output"

	homedir "github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	Version string
	Commit  string
	Date    string

	formatFlag   string
	verboseFlag  bool
	examplesFlag bool

	errExamplesShown = fmt.Errorf("examples shown")

	// invocationSource is the process-wide client surface stamped onto request
	// headers. Defaults to the CLI; runMCPServer flips it to "semai-mcp".
	invocationSource = "semai-cli"
)

var rootCmd = &cobra.Command{
	Use:   "sem-ai",
	Short: "Agent-first CLI for Semaphore CI/CD",
	Long:  "sem-ai — structured, composable CLI designed for AI agents to drive the full CI/CD loop.",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if !verboseFlag {
			log.SetOutput(io.Discard)
		}
		output.SetFormat(formatFlag)

		// Stamp client identification for server-side request metrics. Runs for
		// every command on both the CLI and MCP surfaces (each MCP tool call
		// re-enters the cobra tree), so this is the single choke point that knows
		// the command being executed.
		client.Source = invocationSource
		client.Command = commandSlug(cmd.CommandPath())

		if examplesFlag {
			if cmd.Example != "" {
				output.Result(map[string]string{
					"command":  cmd.CommandPath(),
					"examples": cmd.Example,
				})
			} else {
				output.Result(map[string]string{
					"command":  cmd.CommandPath(),
					"examples": "(no examples defined)",
				})
			}
			return errExamplesShown
		}

		// Best-effort passive version notice. Synchronous cache-fresh path
		// is sub-ms; stale path spawns a goroutine and returns immediately.
		// Gating handled in shouldSkipPersistentCheck.
		maybeNotifyOnCommand(cmd, cmd.ErrOrStderr())

		return nil
	},
	SilenceUsage:  true,
	SilenceErrors: true,
}

// commandSlug turns a cobra command path ("sem-ai analytics summary") into the
// underscore-joined leaf path ("analytics_summary") used as the
// x-client-command header value. Returns "" for the bare root command.
func commandSlug(path string) string {
	fields := strings.Fields(path)
	if len(fields) <= 1 {
		return ""
	}
	return strings.Join(fields[1:], "_")
}

func Execute() {
	// Wrap all commands' Args validators to skip when --examples is set
	patchArgsForExamples(rootCmd)

	err := rootCmd.Execute()

	// `status --exit-code` requests a poll-friendly process exit code. This is
	// the only place os.Exit may run for it — the in-process MCP server calls
	// rootCmd.Execute() directly (cmd/mcp.go) and never reaches here.
	if statusExitCode != 0 {
		os.Exit(statusExitCode)
	}

	if err != nil {
		if err == errExamplesShown {
			return
		}
		os.Exit(1)
	}
}

// patchArgsForExamples wraps each command's Args validator so that
// --examples bypasses arg requirements (e.g. ExactArgs(1)).
func patchArgsForExamples(cmd *cobra.Command) {
	for _, c := range cmd.Commands() {
		if c.Args != nil {
			original := c.Args
			c.Args = func(cmd *cobra.Command, args []string) error {
				if examplesFlag {
					return nil
				}
				return original(cmd, args)
			}
		}
		patchArgsForExamples(c)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVarP(&formatFlag, "format", "f", "json", "output format: json, table, yaml")
	rootCmd.PersistentFlags().BoolVarP(&verboseFlag, "verbose", "v", false, "verbose output (show HTTP requests)")
	rootCmd.PersistentFlags().BoolVar(&examplesFlag, "examples", false, "show command examples and exit")
}

func initConfig() {
	home, err := homedir.Dir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to find home directory: %v\n", err)
		os.Exit(1)
	}

	viper.AddConfigPath(home)
	viper.SetConfigName(".sem")
	viper.SetConfigType("yaml")

	path := fmt.Sprintf("%s/.sem.yaml", home)
	f, err := os.OpenFile(path, os.O_RDONLY|os.O_CREATE, 0600)
	if err == nil {
		f.Close()
	}

	if err := viper.ReadInConfig(); err != nil {
		log.Printf("warning: could not read config: %v", err)
	}

	config.Load()
}
