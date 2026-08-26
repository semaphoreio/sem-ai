package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// A selector naming a context that does not exist yet must not stop the command
// that is about to create it. `--context neworg connect neworg.semaphoreci.com
// <token>` is the natural way for an agent to onboard an organization, and it
// used to die in PersistentPreRunE with config_error before connect ever ran.
func TestConnectRunsUnderAnUnresolvableContextPin(t *testing.T) {
	home := isolateConfigHome(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("[]"))
	}))
	t.Cleanup(srv.Close)
	client.SetBaseURLForTest(srv.URL)
	t.Cleanup(func() { client.SetBaseURLForTest("") })

	prev := contextFlag
	t.Cleanup(func() { contextFlag = prev })
	contextFlag = "neworg_semaphoreci_com"

	out, err := executeCobra([]string{"connect", "neworg.semaphoreci.com", "tok", "--context", "neworg_semaphoreci_com"})
	if err != nil {
		t.Fatalf("connect under an unresolvable pin: %v (%s)", err, out)
	}
	if strings.Contains(out, "config_error") {
		t.Fatalf("connect resolved the pin instead of ignoring it: %s", out)
	}

	written, err := os.ReadFile(filepath.Join(home, ".sem.yaml"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(written), "neworg_semaphoreci_com") {
		t.Errorf("connect did not persist the new context: %s", written)
	}
}

// `context switch` moves the very key a selector shadows, so it must run under
// an unresolvable pin too — otherwise a pinned session can never repoint the file.
func TestContextSwitchRunsUnderAnUnresolvableContextPin(t *testing.T) {
	isolateConfigHome(t, "target")

	prev := contextFlag
	t.Cleanup(func() { contextFlag = prev })
	contextFlag = "does_not_exist"

	out, err := executeCobra([]string{"context", "switch", "target", "--context", "does_not_exist"})
	if err != nil {
		t.Fatalf("context switch under an unresolvable pin: %v (%s)", err, out)
	}
	if strings.Contains(out, "config_error") {
		t.Fatalf("context switch resolved the pin instead of ignoring it: %s", out)
	}
	if got := viper.GetString("active-context"); got != "target" {
		t.Errorf("active-context = %q, want the switch target %q", got, "target")
	}
}

// signin runs a device flow, so it cannot be driven end to end here; the
// regression lives in PersistentPreRunE, which is where the pin was resolved.
func TestSigninPreRunSurvivesAnUnresolvableContextPin(t *testing.T) {
	isolateConfigHome(t)

	prev := contextFlag
	t.Cleanup(func() { contextFlag = prev })
	contextFlag = "does_not_exist"

	if err := rootCmd.PersistentPreRunE(signinCmd, nil); err != nil {
		t.Fatalf("signin PersistentPreRunE under an unresolvable pin: %v", err)
	}
}

// The pin still has to apply everywhere else, or the fix would have disabled it.
func TestUnannotatedCommandStillFailsOnAnUnresolvableContextPin(t *testing.T) {
	isolateConfigHome(t)

	prev := contextFlag
	t.Cleanup(func() { contextFlag = prev })
	contextFlag = "does_not_exist"

	if err := rootCmd.PersistentPreRunE(pipelineListCmd, nil); err == nil {
		t.Fatal("an unresolvable pin must still fail a command that reads through it")
	}
}

// `context list`/`switch` report what is stored, so the active marking follows
// the file rather than a pin that only affects this invocation.
func TestContextListMarksTheFilesActiveContextNotThePin(t *testing.T) {
	isolateConfigHome(t, "other")

	prev := contextFlag
	t.Cleanup(func() { contextFlag = prev })
	contextFlag = "other"

	out, err := executeCobra([]string{"context", "list", "--context", "other", "--format", "json"})
	if err != nil {
		t.Fatalf("context list: %v (%s)", err, out)
	}

	var rows []struct {
		Name   string `json:"name"`
		Active bool   `json:"active"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("parse listing %q: %v", out, err)
	}
	if len(rows) != 2 {
		t.Fatalf("want both seeded contexts, got %d: %s", len(rows), out)
	}
	for _, r := range rows {
		want := r.Name == "isolated"
		if r.Active != want {
			t.Errorf("context %q active = %v, want %v — the listing must follow the file's active-context, not the pin", r.Name, r.Active, want)
		}
	}
}

// Both selectors have to be ignored, not just the flag: an implementation that
// skipped --context while still resolving SEM_CONTEXT would pass every test
// above and still fail the regression this fixes.
func TestExemptCommandsIgnoreTheEnvSelectorToo(t *testing.T) {
	isolateConfigHome(t, "target")
	t.Setenv(config.EnvContext, "does_not_exist")

	out, err := executeCobra([]string{"context", "switch", "target"})
	if err != nil {
		t.Fatalf("context switch under an unresolvable SEM_CONTEXT: %v (%s)", err, out)
	}
	if got := viper.GetString("active-context"); got != "target" {
		t.Errorf("active-context = %q, want %q", got, "target")
	}

	// And the env selector is still honoured where it means something.
	if err := rootCmd.PersistentPreRunE(pipelineListCmd, nil); err == nil {
		t.Error("SEM_CONTEXT naming a missing context must still fail a command that reads through it")
	}
}

// The flag and the env var can disagree; an exempt command ignores both rather
// than resolving whichever wins.
func TestExemptCommandIgnoresBothSelectorsAtOnce(t *testing.T) {
	isolateConfigHome(t, "target")
	t.Setenv(config.EnvContext, "also_missing")

	prev := contextFlag
	t.Cleanup(func() { contextFlag = prev })
	contextFlag = "does_not_exist"

	out, err := executeCobra([]string{"context", "switch", "target", "--context", "does_not_exist"})
	if err != nil {
		t.Fatalf("context switch with both selectors unresolvable: %v (%s)", err, out)
	}
}

// Load consumes the ignore flag. Leaving it set would silently disable an
// exported SEM_CONTEXT for whatever loaded config next — in the MCP server,
// every later tool call.
func TestIgnoreSelectorsDoesNotSurviveOneLoad(t *testing.T) {
	isolateConfigHome(t, "target")
	t.Setenv(config.EnvContext, "target")

	// An exempt command runs first and ignores the selector...
	if _, err := executeCobra([]string{"context", "list"}); err != nil {
		t.Fatalf("context list: %v", err)
	}
	// ...and the next Load still sees it.
	config.SetExplicitContext("")
	if err := config.Load(); err != nil {
		t.Fatalf("Load after an exempt command: %v", err)
	}
	if got := config.GetActiveContext(); got != "target" {
		t.Errorf("active context = %q, want SEM_CONTEXT's %q — the ignore flag leaked past its invocation", got, "target")
	}
}

// signin authenticates an account against a deployment; a context names an
// organization. On Semaphore Cloud every org's account lives on the same host,
// with the CLI-auth endpoints on another, so deriving signin's host from a
// pinned org context posts the device flow somewhere that does not serve it.
// The selector is ignored — and said out loud, because this flow can reset the
// account's only API token.
func TestSigninIgnoresSelectorsAndSaysSo(t *testing.T) {
	isolateConfigHome(t, "cloudorg")

	prev := contextFlag
	t.Cleanup(func() { contextFlag = prev })
	contextFlag = "cloudorg"
	if err := rootCmd.PersistentPreRunE(signinCmd, nil); err != nil {
		t.Fatalf("PersistentPreRunE: %v", err)
	}

	host, authHost, err := resolveSigninHosts(nil, "")
	if err != nil {
		t.Fatalf("resolveSigninHosts: %v", err)
	}
	if host != defaultSigninHost {
		t.Errorf("host = %q, want the account host %q — a pinned org is not a signin target", host, defaultSigninHost)
	}
	if authHost != defaultSigninIDHost {
		t.Errorf("authHost = %q, want %q — the device flow posts here", authHost, defaultSigninIDHost)
	}

	var stderr bytes.Buffer
	probe := &cobra.Command{}
	probe.SetErr(&stderr)
	noteIgnoredSelector(probe, host)
	if !strings.Contains(stderr.String(), "cloudorg") || !strings.Contains(stderr.String(), defaultSigninHost) {
		t.Errorf("an ignored selector must be reported, got %q", stderr.String())
	}

	// Without a selector there is nothing to report.
	contextFlag = ""
	stderr.Reset()
	noteIgnoredSelector(probe, host)
	if stderr.Len() != 0 {
		t.Errorf("unpinned signin must stay quiet, got %q", stderr.String())
	}
}

// connect stores a token and moves active-context. A selector that resolves to
// a different host means two organizations were named at once.
func TestConnectRefusesASelectorThatContradictsItsHost(t *testing.T) {
	isolateConfigHome(t, "other")

	prev := contextFlag
	t.Cleanup(func() { contextFlag = prev })
	contextFlag = "other"

	out, err := executeCobra([]string{"connect", "elsewhere.example.com", "tok", "--context", "other"})
	if err == nil {
		t.Fatalf("connect must refuse a pin naming a different host: %s", out)
	}
	if !strings.Contains(out, "connect_error") {
		t.Errorf("want a connect_error explaining the contradiction, got: %s", out)
	}
}

// The listing is the file's inventory, so `active` follows active-context — but
// a pinned caller has to be able to tell which row this invocation is using.
func TestContextListNamesThePinnedRow(t *testing.T) {
	isolateConfigHome(t, "other")

	prev := contextFlag
	t.Cleanup(func() { contextFlag = prev })
	contextFlag = "other"

	out, err := executeCobra([]string{"context", "list", "--context", "other", "--format", "json"})
	if err != nil {
		t.Fatalf("context list: %v (%s)", err, out)
	}

	var rows []struct {
		Name   string `json:"name"`
		Active bool   `json:"active"`
		Pinned bool   `json:"pinned"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("parse listing %q: %v", out, err)
	}
	for _, r := range rows {
		if r.Name == "isolated" && !r.Active {
			t.Error("active must follow the file's active-context")
		}
		if r.Name == "other" && !r.Pinned {
			t.Error("the pinned row must say so, or a pinned caller is told the wrong org is live")
		}
		if r.Name == "isolated" && r.Pinned {
			t.Error("only the pinned context may be marked pinned")
		}
	}
}
