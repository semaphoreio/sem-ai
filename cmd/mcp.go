package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// executeMu serializes cobra executions — cobra is not concurrent-safe.
var executeMu sync.Mutex

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Start MCP (Model Context Protocol) stdio server",
	Long: `Starts a persistent MCP server over stdin/stdout, exposing all sem-ai
commands as MCP tools. Config is loaded once at startup. Each tool call
routes directly through the in-memory cobra tree — no process spawn.

Any MCP-compatible client (Claude Code, Cursor, VS Code, etc.) can use it.

Add to .mcp.json in your project:
  {
    "mcpServers": {
      "semaphore": {
        "command": "sem-ai",
        "args": ["mcp"]
      }
    }
  }`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runMCPServer()
	},
}

func init() {
	rootCmd.AddCommand(mcpCmd)
}

func runMCPServer() error {
	// Silence log output — it would corrupt the MCP JSON-RPC stream on stdout
	log.SetOutput(io.Discard)

	// Mark this process as the MCP surface for the rest of its lifetime. Every
	// tool call re-enters the cobra tree, so PersistentPreRunE reads this when
	// stamping the x-semaphore-client-source header.
	invocationSource = "semai-mcp"

	s := server.NewMCPServer(
		"sem-ai",
		Version,
		server.WithToolCapabilities(false),
	)

	registerCobraTools(s, rootCmd, "")

	return server.ServeStdio(s)
}

// registerCobraTools walks the cobra command tree and registers each leaf command as an MCP tool.
func registerCobraTools(s *server.MCPServer, cmd *cobra.Command, prefix string) {
	for _, child := range cmd.Commands() {
		name := child.Name()
		// Skip non-tool commands and long-running commands that hold the mutex
		if name == "mcp" || name == "help" || name == "completion" ||
			name == "watch" || name == "promote-and-wait" {
			continue
		}

		fullName := name
		if prefix != "" {
			fullName = prefix + "_" + name
		}

		if child.HasSubCommands() {
			registerCobraTools(s, child, fullName)
			continue
		}

		tool := buildMCPTool(child, fullName)
		handler := buildMCPHandler(child)
		s.AddTool(tool, handler)
	}
}

// buildMCPTool creates an MCP tool definition from a cobra command.
func buildMCPTool(cmd *cobra.Command, toolName string) mcp.Tool {
	opts := []mcp.ToolOption{
		mcp.WithDescription(cmd.Short),
	}

	// Positional args → single "args" string parameter
	argsDesc := extractArgsDescription(cmd)
	if argsDesc != "" {
		opts = append(opts, mcp.WithString("args",
			mcp.Description(argsDesc),
		))
	}

	// Flags → tool parameters
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Name == "format" || f.Name == "verbose" || f.Name == "examples" || f.Name == "help" {
			return
		}

		desc := f.Usage
		if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "[]" && f.DefValue != "0" {
			desc += fmt.Sprintf(" (default: %s)", f.DefValue)
		}

		switch f.Value.Type() {
		case "bool":
			opts = append(opts, mcp.WithBoolean(f.Name, mcp.Description(desc)))
		case "int":
			opts = append(opts, mcp.WithNumber(f.Name, mcp.Description(desc)))
		case "stringArray", "stringSlice":
			opts = append(opts, mcp.WithArray(f.Name, mcp.Description(desc+" (array of strings)")))
		default:
			opts = append(opts, mcp.WithString(f.Name, mcp.Description(desc)))
		}
	})

	return mcp.NewTool(toolName, opts...)
}

// buildMCPHandler returns a handler that captures cobra output via SetOut/SetErr
// (never touching os.Stdout/os.Stderr which the MCP server uses).
func buildMCPHandler(target *cobra.Command) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		executeMu.Lock()
		defer executeMu.Unlock()

		args := request.GetArguments()

		// Build CLI args: command path + positional + flags
		var cliArgs []string
		cliArgs = append(cliArgs, commandPath(target)...)

		// Positional args
		if argsVal, ok := args["args"]; ok {
			if v, ok := argsVal.(string); ok && v != "" {
				cliArgs = append(cliArgs, v)
			}
		}

		// Flags
		target.Flags().VisitAll(func(f *pflag.Flag) {
			if f.Name == "format" || f.Name == "verbose" || f.Name == "examples" || f.Name == "help" {
				return
			}

			val, ok := args[f.Name]
			if !ok || val == nil {
				return
			}

			switch f.Value.Type() {
			case "bool":
				if b, ok := val.(bool); ok && b {
					cliArgs = append(cliArgs, "--"+f.Name)
				}
			case "stringArray", "stringSlice":
				if arr, ok := val.([]interface{}); ok {
					for _, item := range arr {
						cliArgs = append(cliArgs, "--"+f.Name, fmt.Sprintf("%v", item))
					}
				}
			case "int":
				cliArgs = append(cliArgs, "--"+f.Name, fmt.Sprintf("%v", val))
			default:
				cliArgs = append(cliArgs, "--"+f.Name, fmt.Sprintf("%v", val))
			}
		})

		// Force JSON
		cliArgs = append(cliArgs, "--format", "json")

		result, err := executeCobra(cliArgs)
		if err != nil {
			return mcp.NewToolResultError(result), nil
		}

		return mcp.NewToolResultText(result), nil
	}
}

// resetFlags resets all flag values in the command tree to their defaults.
// This prevents flag state from bleeding between MCP tool calls.
func resetFlags(cmd *cobra.Command) {
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		f.Value.Set(f.DefValue)
		f.Changed = false
	})
	cmd.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		f.Value.Set(f.DefValue)
		f.Changed = false
	})
	for _, child := range cmd.Commands() {
		resetFlags(child)
	}
}

// executeCobra runs rootCmd with given args, capturing output via SetOut/SetErr.
// Never touches os.Stdout/os.Stderr — those belong to the MCP server.
func executeCobra(args []string) (string, error) {
	// Reset all flags to defaults before each call
	resetFlags(rootCmd)

	var stdoutBuf, stderrBuf bytes.Buffer

	// Redirect both cobra and output package to buffers
	rootCmd.SetOut(&stdoutBuf)
	rootCmd.SetErr(&stderrBuf)
	output.SetWriters(&stdoutBuf, &stderrBuf)

	// Always reset writers, even on panic
	defer func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		output.SetWriters(nil, nil)
	}()

	rootCmd.SetArgs(args)
	execErr := rootCmd.Execute()

	stdout := strings.TrimSpace(stdoutBuf.String())
	stderr := strings.TrimSpace(stderrBuf.String())

	if execErr != nil {
		if stderr != "" {
			return stderr, execErr
		}
		if stdout != "" {
			return stdout, execErr
		}
		return execErr.Error(), execErr
	}

	if stdout != "" {
		return stdout, nil
	}
	if stderr != "" {
		return stderr, nil
	}
	return "{}", nil
}

// commandPath returns path segments from root to cmd (excluding root).
func commandPath(cmd *cobra.Command) []string {
	var parts []string
	for c := cmd; c != nil && c != rootCmd; c = c.Parent() {
		parts = append([]string{c.Name()}, parts...)
	}
	return parts
}

// extractArgsDescription extracts positional arg info from the Use string.
func extractArgsDescription(cmd *cobra.Command) string {
	use := cmd.Use
	idx := strings.Index(use, " ")
	if idx < 0 {
		return ""
	}
	argsPart := use[idx+1:]
	if strings.Contains(argsPart, "<") || strings.Contains(argsPart, "[") {
		return fmt.Sprintf("Positional argument(s): %s", argsPart)
	}
	return ""
}
