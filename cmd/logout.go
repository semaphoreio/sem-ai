package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// `logout` is the inverse of `signin`/`connect`: it clears a context's stored
// API token instead of saving one. It reuses the same config shape written by
// writeContext (contexts.<name>.host / contexts.<name>.auth.token /
// active-context) and the same viper.Set + WriteConfig + chmod 0600 pattern,
// so it never touches keys it isn't asked to clear.
//
// Deleting a context entry outright isn't done here: viper (v1.19) has no
// Unset, and a Set() on a parent key only shadows leaves it explicitly sets,
// not the whole subtree underneath what ReadInConfig loaded — so a full
// "delete the map key" trick would silently resurrect the old entry on the
// next write. Clearing the token to "" is the safe, in-convention operation
// that actually persists: contexts.list still shows the host (so `connect`/
// `signin` can restore it later) but the context is unusable until it does,
// exactly like never having signed in.
var (
	logoutContext string
	logoutAll     bool
)

var logoutCmd = &cobra.Command{
	Use:     "logout",
	Aliases: []string{"signout"},
	Short:   "Log out of a Semaphore context, clearing its stored API token",
	Long: `Clear stored credentials for a Semaphore context (the inverse of signin/connect).

With no flags, logs out the currently active context: its API token is
cleared and, if it was active, active-context is unset so nothing keeps
pointing at a now-tokenless context. The context's host entry is left in
place so 'sem-ai connect' or 'sem-ai signin' can restore it later.

--context <name> logs out a specific context instead of the active one.
--all clears every configured context's token.

Nothing to log out (no active context, an already-logged-out context, or no
contexts at all) is a friendly no-op that exits 0.`,
	Example: `  sem-ai logout
  sem-ai logout --context myorg_semaphoreci_com
  sem-ai logout --all`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if logoutAll && logoutContext != "" {
			err := errors.New("--context cannot be combined with --all")
			output.Error("logout_error", err.Error(), 1)
			return err
		}

		// Populate config.GetActiveContext() from the current viper state.
		// initConfig() already does this at real-CLI startup, but RunE is also
		// called directly in tests (and could be re-entered after an earlier
		// command mutated viper in-process), so refresh it here rather than
		// assume it's already current.
		config.Load()

		if logoutAll {
			return logoutAllContexts()
		}
		return logoutOneContext(logoutContext)
	},
}

// persistLogoutConfig writes viper's in-memory state to the config file and
// re-enforces 0600 permissions, matching writeContext's pattern.
func persistLogoutConfig() error {
	if err := viper.WriteConfig(); err != nil {
		return err
	}
	if path := viper.ConfigFileUsed(); path != "" {
		_ = os.Chmod(path, 0600)
	}
	return nil
}

// logoutAllContexts clears the token for every configured context and
// unsets active-context (no context is usable without a token afterward, so
// nothing should be left "active"). A no-context config is a no-op.
func logoutAllContexts() error {
	contexts, err := config.ContextList()
	if err != nil {
		output.Error("config_error", err.Error(), 1)
		return err
	}
	if len(contexts) == 0 {
		output.Result(map[string]string{
			"status":  "noop",
			"message": "no contexts configured; nothing to log out",
		})
		return nil
	}

	names := make([]string, 0, len(contexts))
	for _, c := range contexts {
		viper.Set(fmt.Sprintf("contexts.%s.auth.token", c.Name), "")
		names = append(names, c.Name)
	}
	viper.Set("active-context", "")

	if err := persistLogoutConfig(); err != nil {
		output.Error("config_error", fmt.Sprintf("failed to write config: %s", err), 1)
		return err
	}
	config.Load()

	output.Result(map[string]any{
		"status":   "logged_out",
		"contexts": names,
	})
	return nil
}

// logoutOneContext logs out a single context: name if non-empty (from
// --context, validated to exist), otherwise the active context. It clears
// the token and, if the context was active, unsets active-context so it
// never points at a tokenless entry.
func logoutOneContext(name string) error {
	explicit := name != ""
	if !explicit {
		name = config.GetActiveContext()
		if name == "" {
			output.Result(map[string]string{
				"status":  "noop",
				"message": "no active context; nothing to log out",
			})
			return nil
		}
	}

	hostKey := fmt.Sprintf("contexts.%s.host", name)
	if !viper.IsSet(hostKey) {
		if explicit {
			output.Error("not_found", fmt.Sprintf("context %q not found", name), 404)
			return fmt.Errorf("context not found")
		}
		// active-context names an entry that doesn't actually exist (stale or
		// hand-edited config) — nothing to clear, but still repoint away from
		// it so nothing keeps referencing a missing context.
		viper.Set("active-context", "")
		if err := persistLogoutConfig(); err != nil {
			output.Error("config_error", fmt.Sprintf("failed to write config: %s", err), 1)
			return err
		}
		config.Load()
		output.Result(map[string]string{
			"status":  "logged_out",
			"context": name,
			"note":    "context had no stored entry; cleared the stale active-context pointer",
		})
		return nil
	}

	tokenKey := fmt.Sprintf("contexts.%s.auth.token", name)
	wasActive := config.GetActiveContext() == name
	hadToken := viper.GetString(tokenKey) != ""

	if !hadToken && !wasActive {
		output.Result(map[string]string{
			"status":  "noop",
			"context": name,
			"message": fmt.Sprintf("context %q has no stored token; nothing to log out", name),
		})
		return nil
	}

	viper.Set(tokenKey, "")
	if wasActive {
		viper.Set("active-context", "")
	}

	if err := persistLogoutConfig(); err != nil {
		output.Error("config_error", fmt.Sprintf("failed to write config: %s", err), 1)
		return err
	}
	config.Load()

	res := map[string]any{
		"status":  "logged_out",
		"context": name,
	}
	if wasActive {
		res["active_context_cleared"] = true
	}
	output.Result(res)
	return nil
}

func init() {
	logoutCmd.Flags().StringVar(&logoutContext, "context", "", "log out this context instead of the active one")
	logoutCmd.Flags().BoolVar(&logoutAll, "all", false, "log out every configured context")
	rootCmd.AddCommand(logoutCmd)
}
