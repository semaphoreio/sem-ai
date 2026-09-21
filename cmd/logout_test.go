package cmd

import (
	"io"
	"strings"
	"testing"

	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/spf13/viper"
)

// resetLogoutFlags restores the package-level flag vars logout's RunE reads,
// so tests don't leak state into each other (mirrors signin_test.go's pattern
// for signinForceBrowser etc).
func resetLogoutFlags(t *testing.T) {
	t.Helper()
	logoutContext = ""
	logoutAll = false
	t.Cleanup(func() {
		logoutContext = ""
		logoutAll = false
	})
}

// ── default: logs out the active context ────────────────────────────────────

func TestLogout_DefaultLogsOutActiveContext(t *testing.T) {
	setupConfig(t)
	resetLogoutFlags(t)

	if _, err := writeContext("me.example.com", "acct-token", true); err != nil {
		t.Fatal(err)
	}

	if err := logoutCmd.RunE(logoutCmd, []string{}); err != nil {
		t.Fatalf("logout: %v", err)
	}

	if got := viper.GetString("contexts.me_example_com.auth.token"); got != "" {
		t.Errorf("token = %q, want empty after logout", got)
	}
	if got := viper.GetString("active-context"); got != "" {
		t.Errorf("active-context = %q, want empty after logout", got)
	}
	// The host stays so connect/signin can restore the context later.
	if got := viper.GetString("contexts.me_example_com.host"); got != "me.example.com" {
		t.Errorf("host = %q, want it preserved as me.example.com", got)
	}
}

// TestLogout_DefaultPrintsConfirmation pins the "clear confirmation naming
// what was logged out" contract from the result payload.
func TestLogout_DefaultPrintsConfirmation(t *testing.T) {
	setupConfig(t)
	resetLogoutFlags(t)

	if _, err := writeContext("me.example.com", "acct-token", true); err != nil {
		t.Fatal(err)
	}

	var buf strings.Builder
	output.SetWriters(&buf, io.Discard)
	t.Cleanup(func() { output.SetWriters(nil, nil) })

	if err := logoutCmd.RunE(logoutCmd, []string{}); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if !strings.Contains(buf.String(), "me_example_com") {
		t.Errorf("confirmation should name the logged-out context, got:\n%s", buf.String())
	}
}

// ── --context: logs out a named context, leaves others intact ──────────────

func TestLogout_ContextFlagLogsOutNamedOnly(t *testing.T) {
	setupConfig(t)
	resetLogoutFlags(t)

	if _, err := writeContext("one.example.com", "tok-one", false); err != nil {
		t.Fatal(err)
	}
	if _, err := writeContext("two.example.com", "tok-two", true); err != nil {
		t.Fatal(err)
	}

	logoutContext = "one_example_com"
	if err := logoutCmd.RunE(logoutCmd, []string{}); err != nil {
		t.Fatalf("logout: %v", err)
	}

	if got := viper.GetString("contexts.one_example_com.auth.token"); got != "" {
		t.Errorf("one's token = %q, want empty", got)
	}
	// Untouched: token, host, and the active pointer.
	if got := viper.GetString("contexts.two_example_com.auth.token"); got != "tok-two" {
		t.Errorf("two's token = %q, want tok-two (untouched)", got)
	}
	if got := viper.GetString("contexts.two_example_com.host"); got != "two.example.com" {
		t.Errorf("two's host = %q, want two.example.com (untouched)", got)
	}
	if got := viper.GetString("active-context"); got != "two_example_com" {
		t.Errorf("active-context = %q, want two_example_com (untouched, since 'one' wasn't active)", got)
	}
}

// TestLogout_ContextFlagOnActiveClearsActivePointer covers logging out the
// active context via --context (not just the no-flag default path).
func TestLogout_ContextFlagOnActiveClearsActivePointer(t *testing.T) {
	setupConfig(t)
	resetLogoutFlags(t)

	if _, err := writeContext("me.example.com", "acct-token", true); err != nil {
		t.Fatal(err)
	}

	logoutContext = "me_example_com"
	if err := logoutCmd.RunE(logoutCmd, []string{}); err != nil {
		t.Fatalf("logout: %v", err)
	}

	if got := viper.GetString("contexts.me_example_com.auth.token"); got != "" {
		t.Errorf("token = %q, want empty", got)
	}
	if got := viper.GetString("active-context"); got != "" {
		t.Errorf("active-context = %q, want empty (was pointing at the logged-out context)", got)
	}
}

// TestLogout_ContextFlagUnknownNameErrors is the failing-correctly side of
// --context: a typo'd/nonexistent name must error, not silently no-op.
func TestLogout_ContextFlagUnknownNameErrors(t *testing.T) {
	setupConfig(t)
	resetLogoutFlags(t)

	logoutContext = "does_not_exist"
	err := logoutCmd.RunE(logoutCmd, []string{})
	if err == nil {
		t.Fatal("expected error for an unknown --context name")
	}
}

// ── --all: clears every configured context ──────────────────────────────────

func TestLogout_AllClearsEverything(t *testing.T) {
	setupConfig(t)
	resetLogoutFlags(t)

	if _, err := writeContext("one.example.com", "tok-one", false); err != nil {
		t.Fatal(err)
	}
	if _, err := writeContext("two.example.com", "tok-two", true); err != nil {
		t.Fatal(err)
	}

	logoutAll = true
	if err := logoutCmd.RunE(logoutCmd, []string{}); err != nil {
		t.Fatalf("logout --all: %v", err)
	}

	if got := viper.GetString("contexts.one_example_com.auth.token"); got != "" {
		t.Errorf("one's token = %q, want empty", got)
	}
	if got := viper.GetString("contexts.two_example_com.auth.token"); got != "" {
		t.Errorf("two's token = %q, want empty", got)
	}
	if got := viper.GetString("active-context"); got != "" {
		t.Errorf("active-context = %q, want empty after --all", got)
	}
	// Hosts survive --all too.
	if got := viper.GetString("contexts.one_example_com.host"); got != "one.example.com" {
		t.Errorf("one's host = %q, want preserved", got)
	}
	if got := viper.GetString("contexts.two_example_com.host"); got != "two.example.com" {
		t.Errorf("two's host = %q, want preserved", got)
	}
}

// TestLogout_AllWithNoContextsIsNoop: --all on a fresh config must not error.
func TestLogout_AllWithNoContextsIsNoop(t *testing.T) {
	setupConfig(t)
	resetLogoutFlags(t)

	logoutAll = true
	if err := logoutCmd.RunE(logoutCmd, []string{}); err != nil {
		t.Fatalf("logout --all on empty config should be a no-op, got: %v", err)
	}
}

// ── no-op: nothing to log out ────────────────────────────────────────────────

func TestLogout_NoActiveContextIsNoop(t *testing.T) {
	setupConfig(t)
	resetLogoutFlags(t)

	if err := logoutCmd.RunE(logoutCmd, []string{}); err != nil {
		t.Fatalf("logout with no active context should be a no-op, got: %v", err)
	}
	if got := viper.GetString("active-context"); got != "" {
		t.Errorf("active-context = %q, want it to stay empty", got)
	}
}

// TestLogout_AlreadyLoggedOutContextIsNoop: an inactive context whose token
// is already cleared has nothing left to do.
func TestLogout_AlreadyLoggedOutContextIsNoop(t *testing.T) {
	setupConfig(t)
	resetLogoutFlags(t)

	if _, err := writeContext("one.example.com", "tok-one", false); err != nil {
		t.Fatal(err)
	}
	// Pre-clear it directly, bypassing logout, to simulate "already logged out".
	viper.Set("contexts.one_example_com.auth.token", "")
	if err := viper.WriteConfig(); err != nil {
		t.Fatal(err)
	}

	logoutContext = "one_example_com"
	if err := logoutCmd.RunE(logoutCmd, []string{}); err != nil {
		t.Fatalf("logout on an already-logged-out context should be a no-op, got: %v", err)
	}
	if got := viper.GetString("contexts.one_example_com.host"); got != "one.example.com" {
		t.Errorf("host = %q, want it left untouched", got)
	}
}

// ── flag validation ───────────────────────────────────────────────────────────

// TestLogout_ContextAndAllConflict: the two selection modes are mutually
// exclusive and must be rejected before any config write.
func TestLogout_ContextAndAllConflict(t *testing.T) {
	setupConfig(t)
	resetLogoutFlags(t)

	if _, err := writeContext("me.example.com", "acct-token", true); err != nil {
		t.Fatal(err)
	}

	logoutContext = "me_example_com"
	logoutAll = true
	err := logoutCmd.RunE(logoutCmd, []string{})
	if err == nil {
		t.Fatal("expected error when --context is combined with --all")
	}
	if !strings.Contains(err.Error(), "--context") || !strings.Contains(err.Error(), "--all") {
		t.Errorf("error = %q, want it to name both flags", err.Error())
	}
	// Nothing should have been touched.
	if got := viper.GetString("contexts.me_example_com.auth.token"); got != "acct-token" {
		t.Errorf("token = %q, want it untouched by the rejected combo", got)
	}
}

// ── command wiring ────────────────────────────────────────────────────────────

func TestLogoutCmd_Wiring(t *testing.T) {
	if logoutCmd.Use != "logout" {
		t.Errorf("Use = %q, want \"logout\"", logoutCmd.Use)
	}
	if !logoutCmd.HasAlias("signout") {
		t.Error("logout should have alias \"signout\"")
	}
	if logoutCmd.Short == "" {
		t.Error("Short must not be empty (it feeds the MCP tool description)")
	}
	found := false
	for _, c := range rootCmd.Commands() {
		if c.Name() == "logout" {
			found = true
			break
		}
	}
	if !found {
		t.Error("logout command is not registered on rootCmd")
	}
	if err := logoutCmd.Args(logoutCmd, []string{}); err != nil {
		t.Errorf("unexpected error with zero args: %v", err)
	}
	if err := logoutCmd.Args(logoutCmd, []string{"extra"}); err == nil {
		t.Error("expected error with a positional arg (logout takes none)")
	}
}
