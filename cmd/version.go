package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/semaphoreio/sem-ai/pkg/versioncheck"
	"github.com/spf13/cobra"
)

var (
	versionCheckFlag             bool
	versionNotifyOnlyIfNewerFlag bool
	versionHookFlag              bool
)

// upgradeHint is the canonical install.sh re-run command surfaced both in
// `version --check` stdout and the `--notify-only-if-newer` stderr notice.
const upgradeHint = "curl -fsSL https://raw.githubusercontent.com/semaphoreio/sem-ai/main/install.sh | sh"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Example: `  sem-ai version
  sem-ai version --check
  sem-ai version --check --notify-only-if-newer
  sem-ai version --hook`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if versionHookFlag {
			return runHookSystemMessage(cmd.Context(), cmd.OutOrStdout())
		}
		if versionNotifyOnlyIfNewerFlag {
			return runNotifyOnlyIfNewer(cmd.Context(), cmd.ErrOrStderr())
		}
		if versionCheckFlag {
			return runCheckVerbose(cmd.Context())
		}
		output.Result(map[string]string{
			"version": Version,
			"commit":  Commit,
			"date":    Date,
		})
		return nil
	},
}

// runCheckVerbose performs a foreground version check and emits the
// extended JSON shape on stdout. Network failure → base map + check_error;
// env opt-out → base map + check_skipped. Exit code 0 in all cases.
func runCheckVerbose(ctx context.Context) error {
	base := map[string]any{
		"version": Version,
		"commit":  Commit,
		"date":    Date,
	}

	if versioncheck.EnvOptOut() {
		base["update_available"] = nil
		base["check_skipped"] = "opt-out"
		output.Result(base)
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	rel, err := versioncheck.Latest(ctx)
	if err != nil {
		base["update_available"] = nil
		base["check_error"] = err.Error()
		output.Result(base)
		return nil
	}

	newer, _ := versioncheck.Compare(Version, rel.Version)
	base["latest_version"] = rel.Version
	base["latest_published_at"] = rel.PublishedAt
	base["update_available"] = newer
	if newer {
		base["upgrade_hint"] = upgradeHint
	}

	// Best-effort cache write — never fail the command on cache errors.
	state, _, _ := versioncheck.ReadCache()
	state.LastCheckedAt = time.Now().UTC()
	state.LatestVersion = rel.Version
	state.LatestPublishedAt = rel.PublishedAt
	state.CurrentVersionWhenChecked = Version
	_ = versioncheck.WriteCache(state)

	output.Result(base)
	return nil
}

// runNotifyOnlyIfNewer is the hook-friendly mode. Silent unless a newer
// release exists. Output goes to stderr (stdout JSON contract preserved
// for callers piping --format json to jq / agents). Notice fires every
// invocation while behind — single nag-on-disk state would just stale.
func runNotifyOnlyIfNewer(ctx context.Context, stderr io.Writer) error {
	if versioncheck.EnvOptOut() {
		return nil
	}

	state, _, _ := versioncheck.ReadCache()
	now := time.Now().UTC()

	if !versioncheck.Fresh(state, now) {
		fetchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		rel, err := versioncheck.Latest(fetchCtx)
		if err != nil {
			return nil // silent on error; LastCheckedAt NOT bumped so we retry
		}
		state.LastCheckedAt = now
		state.LatestVersion = rel.Version
		state.LatestPublishedAt = rel.PublishedAt
		state.CurrentVersionWhenChecked = Version
		_ = versioncheck.WriteCache(state)
	}

	newer, _ := versioncheck.Compare(Version, state.LatestVersion)
	if !newer {
		return nil
	}

	fmt.Fprintf(stderr, "sem-ai %s is available (you have %s). Upgrade:\n  %s\n",
		state.LatestVersion, Version, upgradeHint)
	return nil
}

// runHookSystemMessage is the Claude Code SessionStart-hook mode. Silent on
// stdout when up to date; on a newer release available emits a JSON document
// `{"systemMessage": "<two-line upgrade notice>"}` so Claude Code surfaces it
// as a visible banner. Exit 0 always — hooks must not block.
//
// Why this and not --notify-only-if-newer: Claude Code SessionStart hooks
// treat stderr as "user-only" (printed to the terminal, not surfaced in the
// UI). To get a visible banner, the hook must emit JSON on stdout with the
// documented `systemMessage` field.
func runHookSystemMessage(ctx context.Context, stdout io.Writer) error {
	if versioncheck.EnvOptOut() {
		return nil
	}

	state, _, _ := versioncheck.ReadCache()
	now := time.Now().UTC()

	if !versioncheck.Fresh(state, now) {
		fetchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		rel, err := versioncheck.Latest(fetchCtx)
		if err != nil {
			return nil // silent on error; LastCheckedAt NOT bumped so we retry
		}
		state.LastCheckedAt = now
		state.LatestVersion = rel.Version
		state.LatestPublishedAt = rel.PublishedAt
		state.CurrentVersionWhenChecked = Version
		_ = versioncheck.WriteCache(state)
	}

	newer, _ := versioncheck.Compare(Version, state.LatestVersion)
	if !newer {
		return nil
	}

	msg := fmt.Sprintf("sem-ai %s is available (you have %s). Upgrade:\n  %s",
		state.LatestVersion, Version, upgradeHint)

	payload, err := json.Marshal(map[string]string{"systemMessage": msg})
	if err != nil {
		return nil // best-effort; never block the session
	}
	fmt.Fprintln(stdout, string(payload))
	return nil
}

// stderrIsTTY is a package-level var so tests can override it.
var stderrIsTTY = func() bool {
	info, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

// preRunCheckTimeout caps how long the PersistentPreRunE auto-notice will
// wait for a cold-cache or stale-cache GitHub refresh. The cache absorbs
// subsequent calls for 6h, so this cost is rare. Tight enough that an
// offline user doesn't notice; loose enough to usually succeed.
const preRunCheckTimeout = 1500 * time.Millisecond

// maybeNotifyOnCommand is fired from rootCmd.PersistentPreRunE on every CLI
// invocation. Gated to avoid polluting scripts, CI logs, and the version
// subcommand's own check path.
//
// On cold or stale cache, performs a SYNCHRONOUS HTTP refresh with a tight
// timeout (preRunCheckTimeout). Background goroutines don't survive short
// CLI process lifetimes — the parent returns and the goroutine dies before
// the HTTP call lands. After one successful refresh the cache absorbs calls
// for 6h, so the sync-on-refresh cost is amortized.
//
// All gating is via shouldSkipPersistentCheck so tests can drive each lever.
func maybeNotifyOnCommand(cmd *cobra.Command, stderr io.Writer) {
	if shouldSkipPersistentCheck(cmd) {
		return
	}

	state, _, _ := versioncheck.ReadCache()
	now := time.Now().UTC()

	if !versioncheck.Fresh(state, now) {
		ctx, cancel := context.WithTimeout(context.Background(), preRunCheckTimeout)
		defer cancel()
		rel, err := versioncheck.Latest(ctx)
		if err != nil {
			return // offline / slow / rate-limited — try again next run
		}
		state.LastCheckedAt = now
		state.LatestVersion = rel.Version
		state.LatestPublishedAt = rel.PublishedAt
		state.CurrentVersionWhenChecked = Version
		_ = versioncheck.WriteCache(state)
	}

	if state.LatestVersion == "" {
		return
	}

	newer, _ := versioncheck.Compare(Version, state.LatestVersion)
	if !newer {
		return
	}

	fmt.Fprintf(stderr, "sem-ai %s is available (you have %s). Upgrade:\n  %s\n",
		state.LatestVersion, Version, upgradeHint)
}

// shouldSkipPersistentCheck encapsulates every gating decision so tests can
// hit each lever independently.
func shouldSkipPersistentCheck(cmd *cobra.Command) bool {
	name := cmd.Name()
	if name == "version" || name == "help" || strings.HasPrefix(name, "__complete") {
		return true
	}
	if versioncheck.EnvOptOut() {
		return true
	}
	if ci := os.Getenv("CI"); ci == "true" || ci == "1" {
		return true
	}
	if !stderrIsTTY() {
		return true
	}
	return false
}

func init() {
	versionCmd.Flags().BoolVar(&versionCheckFlag, "check", false, "check GitHub for a newer release")
	versionCmd.Flags().BoolVar(&versionNotifyOnlyIfNewerFlag, "notify-only-if-newer", false, "(implies --check) silent unless a newer release exists; prints two-line stderr notice when newer")
	versionCmd.Flags().BoolVar(&versionHookFlag, "hook", false, "Claude Code SessionStart-hook mode: silent on stdout when up to date; emits JSON {\"systemMessage\": ...} on stdout when a newer release exists")
	rootCmd.AddCommand(versionCmd)
}
