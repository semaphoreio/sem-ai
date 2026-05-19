package cmd

import (
	"context"
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
)

// upgradeHint is the canonical install.sh re-run command surfaced both in
// `version --check` stdout and the `--notify-only-if-newer` stderr notice.
const upgradeHint = "curl -fsSL https://raw.githubusercontent.com/semaphoreio/sem-ai/main/install.sh | sh"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Example: `  sem-ai version
  sem-ai version --check
  sem-ai version --check --notify-only-if-newer`,
	RunE: func(cmd *cobra.Command, args []string) error {
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
// release exists AND the user hasn't been notified for that release yet.
// Output goes to stderr (surfaces in chat as developer context).
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
	if state.NotifiedForVersion == state.LatestVersion {
		return nil
	}

	fmt.Fprintf(stderr, "sem-ai %s is available (you have %s). Upgrade:\n  %s\n",
		state.LatestVersion, Version, upgradeHint)

	state.NotifiedForVersion = state.LatestVersion
	_ = versioncheck.WriteCache(state) // best-effort; failure non-fatal
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

// maybeNotifyOnCommand is fired from rootCmd.PersistentPreRunE on every CLI
// invocation. Best-effort, non-blocking, gated to avoid polluting scripts,
// CI logs, and the version subcommand's own check path.
//
// Behavior:
//   - Cache fresh: synchronously evaluate notify-eligibility from cache, emit
//     stderr notice if eligible, bump notified_for_version.
//   - Cache stale or empty: spawn a background goroutine to refresh from
//     GitHub. The current command returns immediately; the *next* command
//     reads the refreshed cache. Never blocks foreground.
//
// All gating is via shouldSkipPersistentCheck so tests can drive each lever.
func maybeNotifyOnCommand(cmd *cobra.Command, stderr io.Writer) {
	if shouldSkipPersistentCheck(cmd) {
		return
	}

	state, _, _ := versioncheck.ReadCache()
	now := time.Now().UTC()

	if !versioncheck.Fresh(state, now) {
		// Stale or empty cache → background refresh. Do not notify this run.
		go refreshCacheInBackground(now)
		return
	}

	if state.LatestVersion == "" {
		return
	}

	newer, _ := versioncheck.Compare(Version, state.LatestVersion)
	if !newer {
		return
	}
	if state.NotifiedForVersion == state.LatestVersion {
		return
	}

	fmt.Fprintf(stderr, "sem-ai %s is available (you have %s). Upgrade:\n  %s\n",
		state.LatestVersion, Version, upgradeHint)

	state.NotifiedForVersion = state.LatestVersion
	_ = versioncheck.WriteCache(state) // best-effort
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

// refreshCacheInBackground is the goroutine body for the stale-cache path.
// 3s HTTP timeout; merges into a fresh ReadCache to avoid clobbering a
// concurrent NotifiedForVersion bump.
func refreshCacheInBackground(now time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	rel, err := versioncheck.Latest(ctx)
	if err != nil {
		return // silent on error; LastCheckedAt NOT bumped so we retry next run
	}

	current, _, _ := versioncheck.ReadCache()
	current.LastCheckedAt = now
	current.LatestVersion = rel.Version
	current.LatestPublishedAt = rel.PublishedAt
	current.CurrentVersionWhenChecked = Version
	_ = versioncheck.WriteCache(current)
}

func init() {
	versionCmd.Flags().BoolVar(&versionCheckFlag, "check", false, "check GitHub for a newer release")
	versionCmd.Flags().BoolVar(&versionNotifyOnlyIfNewerFlag, "notify-only-if-newer", false, "(implies --check) silent unless a newer release exists; prints two-line stderr notice when newer")
	rootCmd.AddCommand(versionCmd)
}
