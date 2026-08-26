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

// mcpBaseContext holds the --context this server process was started with, so
// executeCobra can put it back after resetFlags. Empty unless `sem-ai mcp
// --context <name>` was used.
var mcpBaseContext string

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Start MCP (Model Context Protocol) stdio server",
	// The server itself reads no credentials — each tool call resolves its own
	// context. Resolving the pin here as well would stop a server from starting
	// against an organization that has not been connected yet, which is exactly
	// the "pin now, onboard after" case the flag exists for.
	Annotations: map[string]string{contextAgnostic: "true"},
	Long: `Starts a persistent MCP server over stdin/stdout, exposing all sem-ai
commands as MCP tools. Config is loaded once at startup. Each tool call
routes directly through the in-memory cobra tree, with no process spawn.

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
	// stamping the x-client-source header.
	invocationSource = "semai-mcp"

	// resetFlags clears every root persistent flag before each tool call,
	// which would drop a server-wide pin passed as `sem-ai mcp --context X`
	// on the first call and silently fall back to the shared active-context.
	// Remember it here, while the mcp command's own flag parse is still the
	// most recent one.
	mcpBaseContext = contextFlag

	s := server.NewMCPServer(
		"sem-ai",
		Version,
		server.WithToolCapabilities(false),
	)

	registerCobraTools(s, rootCmd, "")

	return server.ServeStdio(s)
}

// eachToolLeaf visits every cobra leaf command that becomes an MCP tool,
// passing the leaf and its underscore-joined tool name.
func eachToolLeaf(cmd *cobra.Command, prefix string, fn func(*cobra.Command, string)) {
	for _, child := range cmd.Commands() {
		name := child.Name()
		// Skip non-tool commands and long-running commands that hold the mutex.
		//
		// signin and connect are onboarding commands that cannot work here.
		// signin runs a device flow: it would hold executeMu for up to the grant
		// TTL — measured, a `version` call queued behind one waited 30s — and its
		// user code goes to a buffered stderr the human only sees once the call
		// returns, by which time the code is spent. connect takes two positional
		// arguments, and toolCLIArgs passes `args` as a single argv element, so
		// it always fails validation ("accepts 2 arg(s), received 1"). Both are
		// one CLI command away; advertising them as tools only produces wedged
		// servers and arg errors.
		if name == "mcp" || name == "help" || name == "completion" ||
			name == "watch" || name == "promote-and-wait" ||
			name == "signin" || name == "connect" {
			continue
		}

		fullName := name
		if prefix != "" {
			fullName = prefix + "_" + name
		}

		if child.HasSubCommands() {
			eachToolLeaf(child, fullName, fn)
			continue
		}

		fn(child, fullName)
	}
}

// registerCobraTools walks the cobra command tree and registers each leaf command as an MCP tool.
func registerCobraTools(s *server.MCPServer, cmd *cobra.Command, prefix string) {
	eachToolLeaf(cmd, prefix, func(leaf *cobra.Command, toolName string) {
		s.AddTool(buildMCPTool(leaf, toolName), buildMCPHandler(leaf))
	})
}

// buildMCPTool creates an MCP tool definition from a cobra command.
func buildMCPTool(cmd *cobra.Command, toolName string) mcp.Tool {
	// Cobra folds a parent's persistent flags into cmd.Flags() lazily, at
	// ParseFlags time. Tool schemas are built at registration, before any leaf
	// has ever parsed, so without this the root persistent flags (--context)
	// are missing from every schema. InheritedFlags() forces the merge.
	cmd.InheritedFlags()

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

// toolCLIArgs turns an MCP tool call's arguments back into the argv the cobra
// tree expects: command path, positional args, then flags.
func toolCLIArgs(target *cobra.Command, args map[string]any) []string {
	cliArgs := commandPath(target)

	if argsVal, ok := args["args"]; ok {
		if v, ok := argsVal.(string); ok && v != "" {
			cliArgs = append(cliArgs, v)
		}
	}

	target.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Name == "format" || f.Name == "verbose" || f.Name == "examples" || f.Name == "help" {
			return
		}

		val, ok := args[f.Name]
		if !ok || val == nil {
			return
		}

		// An empty context is not a context. LLM clients routinely send "" for
		// an optional string, and forwarding `--context ""` would overwrite the
		// server-wide pin restored above with "no pin at all" — every such call
		// silently falling back to the shared active-context. Other flags keep
		// their empty-string meaning; only the selector is special.
		if f.Name == "context" {
			if sv, ok := val.(string); ok && sv == "" {
				return
			}
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
	return append(cliArgs, "--format", "json")
}

// buildMCPHandler returns a handler that captures cobra output via SetOut/SetErr
// (never touching os.Stdout/os.Stderr which the MCP server uses).
func buildMCPHandler(target *cobra.Command) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		executeMu.Lock()
		defer executeMu.Unlock()

		cliArgs := toolCLIArgs(target, request.GetArguments())

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
	cmd.Flags().VisitAll(resetFlag)
	cmd.PersistentFlags().VisitAll(resetFlag)
	for _, child := range cmd.Commands() {
		resetFlags(child)
	}
}

// resetFlag restores one flag to its default.
//
// Slice flags need Replace rather than Set. pflag's slice values carry their
// own "has been set" latch and append on every Set after the first, and the
// DefValue of an empty slice flag is the literal string "[]" — so Set(DefValue)
// pushes "[]" onto the slice instead of clearing it, once per MCP tool call.
// Every slice flag in this tree defaults to nil (asserted by
// TestSliceFlagsDefaultToEmpty), which is what makes Replace(nil) an exact
// reset rather than an approximation.
func resetFlag(f *pflag.Flag) {
	if sv, ok := f.Value.(pflag.SliceValue); ok {
		sv.Replace(nil)
	} else {
		f.Value.Set(f.DefValue)
	}
	f.Changed = false
}

// executeCobra runs rootCmd with given args, capturing output via SetOut/SetErr.
// Never touches os.Stdout/os.Stderr — those belong to the MCP server.
func executeCobra(args []string) (string, error) {
	// Reset all flags to defaults before each call
	resetFlags(rootCmd)

	// Restore the server-wide context pin the reset just cleared. A per-call
	// context argument still wins: it arrives in args and overwrites this
	// during the Execute below.
	contextFlag = mcpBaseContext

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
