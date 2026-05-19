package cmd

import (
	"context"
	"fmt"
	"io"
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

func init() {
	versionCmd.Flags().BoolVar(&versionCheckFlag, "check", false, "check GitHub for a newer release")
	versionCmd.Flags().BoolVar(&versionNotifyOnlyIfNewerFlag, "notify-only-if-newer", false, "(implies --check) silent unless a newer release exists; prints two-line stderr notice when newer")
	rootCmd.AddCommand(versionCmd)
}
