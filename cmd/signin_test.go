package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/spf13/viper"
)

// tokenStep is one scripted /cli/token response.
type tokenStep struct {
	status int
	body   string
}

// newDeviceServer serves /cli/device (a fixed device authorization) and /cli/token
// (walking tokenSeq, then repeating the last step). It records how many token
// polls it received.
func newDeviceServer(t *testing.T, tokenSeq []tokenStep) (*httptest.Server, *int64) {
	t.Helper()
	var polls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cli/device":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{
				"device_code": "dev-code-xyz",
				"user_code": "BCDF-GHJK",
				"verification_uri": "https://id.example.com/device",
				"verification_uri_complete": "https://id.example.com/device?user_code=BCDF-GHJK",
				"expires_in": 1800,
				"interval": 1
			}`)
		case "/cli/token":
			if err := r.ParseForm(); err != nil {
				t.Errorf("failed to parse token form: %v", err)
			}
			if got := r.PostForm.Get("grant_type"); got != deviceGrantType {
				t.Errorf("grant_type = %q, want %q", got, deviceGrantType)
			}
			if got := r.PostForm.Get("device_code"); got != "dev-code-xyz" {
				t.Errorf("device_code = %q, want dev-code-xyz", got)
			}
			n := atomic.AddInt64(&polls, 1) - 1
			step := tokenSeq[len(tokenSeq)-1]
			if int(n) < len(tokenSeq) {
				step = tokenSeq[n]
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(step.status)
			_, _ = io.WriteString(w, step.body)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &polls
}

func newClient(srv *httptest.Server) *cliAuthClient {
	return &cliAuthClient{baseURL: srv.URL, http: srv.Client()}
}

// noSleep is an injectable sleep that does nothing, so polling tests run instantly.
func noSleep(time.Duration) {}

// noBrowser: every device-flow test passes attemptBrowser=false so tests never
// try to open a real browser.
const noBrowser = false

func TestRunDeviceFlow_PendingThenMinted(t *testing.T) {
	srv, polls := newDeviceServer(t, []tokenStep{
		{400, `{"error":"authorization_pending"}`},
		{400, `{"error":"authorization_pending"}`},
		{200, `{"token":"tok-123","host":null,"token_action":"minted"}`},
	})

	tok, err := runDeviceFlow(newClient(srv), io.Discard, noSleep, noBrowser)
	if err != nil {
		t.Fatalf("runDeviceFlow: %v", err)
	}
	if tok.Token != "tok-123" {
		t.Errorf("token = %q, want tok-123", tok.Token)
	}
	if tok.Host != "" {
		t.Errorf("host = %q, want empty (server returns null)", tok.Host)
	}
	if tok.TokenAction != "minted" {
		t.Errorf("token_action = %q, want minted", tok.TokenAction)
	}
	if got := atomic.LoadInt64(polls); got != 3 {
		t.Errorf("token polls = %d, want 3", got)
	}
}

// TestRunDeviceFlow_Rotated covers the existing-account path: the human
// consented to the token reset in the browser and the server reports the
// rotation, which the CLI must surface (it gates --org org creation).
func TestRunDeviceFlow_Rotated(t *testing.T) {
	srv, _ := newDeviceServer(t, []tokenStep{
		{200, `{"token":"tok-rotated","host":null,"token_action":"rotated"}`},
	})

	tok, err := runDeviceFlow(newClient(srv), io.Discard, noSleep, noBrowser)
	if err != nil {
		t.Fatalf("runDeviceFlow: %v", err)
	}
	if tok.Token != "tok-rotated" {
		t.Errorf("token = %q, want tok-rotated", tok.Token)
	}
	if tok.TokenAction != "rotated" {
		t.Errorf("token_action = %q, want rotated", tok.TokenAction)
	}
}

// TestRunDeviceFlow_LegacyServerWithoutTokenAction: a server that omits
// token_action (older guard) must still sign in fine, with the action empty.
func TestRunDeviceFlow_LegacyServerWithoutTokenAction(t *testing.T) {
	srv, _ := newDeviceServer(t, []tokenStep{
		{200, `{"token":"tok-legacy","host":null}`},
	})

	tok, err := runDeviceFlow(newClient(srv), io.Discard, noSleep, noBrowser)
	if err != nil {
		t.Fatalf("runDeviceFlow: %v", err)
	}
	if tok.TokenAction != "" {
		t.Errorf("token_action = %q, want empty for a legacy server", tok.TokenAction)
	}
}

func TestRunDeviceFlow_SlowDownThenSuccess(t *testing.T) {
	srv, _ := newDeviceServer(t, []tokenStep{
		{400, `{"error":"slow_down"}`},
		{200, `{"token":"tok-abc","host":null,"token_action":"minted"}`},
	})

	tok, err := runDeviceFlow(newClient(srv), io.Discard, noSleep, noBrowser)
	if err != nil {
		t.Fatalf("runDeviceFlow: %v", err)
	}
	if tok.Token != "tok-abc" {
		t.Errorf("token = %q, want tok-abc", tok.Token)
	}
}

func TestRunDeviceFlow_AccessDenied(t *testing.T) {
	srv, _ := newDeviceServer(t, []tokenStep{
		{400, `{"error":"access_denied"}`},
	})

	_, err := runDeviceFlow(newClient(srv), io.Discard, noSleep, noBrowser)
	if err == nil {
		t.Fatal("expected error on access_denied")
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Errorf("error = %q, want it to mention denial", err.Error())
	}
	if !strings.Contains(err.Error(), "nothing was changed") {
		t.Errorf("error = %q, want it to reassure nothing changed", err.Error())
	}
}

func TestRunDeviceFlow_ExpiredToken(t *testing.T) {
	srv, _ := newDeviceServer(t, []tokenStep{
		{400, `{"error":"expired_token"}`},
	})

	_, err := runDeviceFlow(newClient(srv), io.Discard, noSleep, noBrowser)
	if err == nil {
		t.Fatal("expected error on expired_token")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("error = %q, want it to mention expiry", err.Error())
	}
	if !strings.Contains(err.Error(), "sem-ai signin") {
		t.Errorf("error = %q, want the clean re-prompt to re-run signin", err.Error())
	}
}

func TestRunDeviceFlow_TokenExists(t *testing.T) {
	msg := "This account received an API token while sign-in was in progress. Run `sem-ai signin` again to confirm what to do with it."
	srv, _ := newDeviceServer(t, []tokenStep{
		{409, `{"error":"token_exists","message":"` + msg + `"}`},
	})

	_, err := runDeviceFlow(newClient(srv), io.Discard, noSleep, noBrowser)
	if err == nil {
		t.Fatal("expected error on token_exists")
	}
	if !strings.Contains(err.Error(), "sem-ai signin") {
		t.Errorf("error = %q, want the re-run instruction from the server", err.Error())
	}
}

func TestRunDeviceFlow_UnknownError(t *testing.T) {
	srv, _ := newDeviceServer(t, []tokenStep{
		{400, `{"error":"invalid_grant"}`},
	})

	_, err := runDeviceFlow(newClient(srv), io.Discard, noSleep, noBrowser)
	if err == nil {
		t.Fatal("expected error on invalid_grant")
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("error = %q, want invalid_grant", err.Error())
	}
}

// TestRunDeviceFlow_PrintsCodeAndWarning pins the interactive contract: the
// one-time code and the verification URL are always printed, whether or not a
// browser could be opened.
func TestRunDeviceFlow_PrintsCodeAndURL(t *testing.T) {
	srv, _ := newDeviceServer(t, []tokenStep{
		{200, `{"token":"tok-1","host":null,"token_action":"minted"}`},
	})

	var buf strings.Builder
	_, err := runDeviceFlow(newClient(srv), &buf, noSleep, noBrowser)
	if err != nil {
		t.Fatalf("runDeviceFlow: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "BCDF-GHJK") {
		t.Errorf("output should contain the one-time code, got:\n%s", out)
	}
	if !strings.Contains(out, "https://id.example.com/device") {
		t.Errorf("output should contain the verification URL, got:\n%s", out)
	}
}

func TestSaveSigninContext_WritesTokenAndSetsPerms(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".sem.yaml")
	if err := os.WriteFile(cfgPath, []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.SetConfigFile(cfgPath)
	viper.SetConfigType("yaml")
	if err := viper.ReadInConfig(); err != nil {
		t.Fatalf("ReadInConfig: %v", err)
	}

	// Silence the success payload so it doesn't leak into test output.
	output.SetWriters(io.Discard, io.Discard)
	t.Cleanup(func() { output.SetWriters(nil, nil) })

	if err := saveSigninContext("myorg.semaphoreci.com", "secret-token", "minted"); err != nil {
		t.Fatalf("saveSigninContext: %v", err)
	}

	fi, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0600 {
		t.Errorf("config perms = %o, want 0600", perm)
	}

	// Read the file back through a fresh viper to confirm the persisted values.
	viper.Reset()
	viper.SetConfigFile(cfgPath)
	viper.SetConfigType("yaml")
	if err := viper.ReadInConfig(); err != nil {
		t.Fatalf("re-read config: %v", err)
	}
	if got := viper.GetString("active-context"); got != "myorg_semaphoreci_com" {
		t.Errorf("active-context = %q, want myorg_semaphoreci_com", got)
	}
	if got := viper.GetString("contexts.myorg_semaphoreci_com.auth.token"); got != "secret-token" {
		t.Errorf("token = %q, want secret-token", got)
	}
	if got := viper.GetString("contexts.myorg_semaphoreci_com.host"); got != "myorg.semaphoreci.com" {
		t.Errorf("host = %q, want myorg.semaphoreci.com", got)
	}
}

func TestIsHeadless(t *testing.T) {
	t.Setenv("SSH_TTY", "")
	t.Setenv("SSH_CONNECTION", "")

	if isHeadless(true) != true {
		t.Error("isHeadless(true) should suppress the browser attempt")
	}

	t.Setenv("SSH_CONNECTION", "10.0.0.1 22 10.0.0.2 2222")
	if !isHeadless(false) {
		t.Error("isHeadless should detect an SSH session")
	}
}

// TestIsHeadless_NoTTYSkipsBrowser covers the "skips when it should" side of
// the no-TTY signal: a non-interactive session (piped, run from a script,
// headless automation) means nobody sees a browser window, so signin should
// not try to open one.
func TestIsHeadless_NoTTYSkipsBrowser(t *testing.T) {
	t.Setenv("SSH_TTY", "")
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("DISPLAY", "not-empty")
	t.Setenv("WAYLAND_DISPLAY", "")

	orig := stderrIsTTY
	stderrIsTTY = func() bool { return false }
	t.Cleanup(func() { stderrIsTTY = orig })

	if !isHeadless(false) {
		t.Error("isHeadless should be true when there's no interactive terminal")
	}
}

// TestIsHeadless_InteractiveDesktopOpensBrowser covers the "works when it
// should" side: a real interactive desktop session (TTY, no SSH, a display)
// must keep the browser convenience — the no-TTY signal must not regress it.
func TestIsHeadless_InteractiveDesktopOpensBrowser(t *testing.T) {
	t.Setenv("SSH_TTY", "")
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("DISPLAY", "not-empty")
	t.Setenv("WAYLAND_DISPLAY", "")

	orig := stderrIsTTY
	stderrIsTTY = func() bool { return true }
	t.Cleanup(func() { stderrIsTTY = orig })

	if isHeadless(false) {
		t.Error("isHeadless should be false for an interactive desktop session")
	}
}

// ── --browser: forcing the browser open ──────────────────────────────────────

// noTTYHeadlessEnv simulates the agent-driving-the-CLI environment: no SSH,
// a display present, but stderr is not a TTY, so the heuristic says headless.
func noTTYHeadlessEnv(t *testing.T) {
	t.Helper()
	t.Setenv("SSH_TTY", "")
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("DISPLAY", "not-empty")
	t.Setenv("WAYLAND_DISPLAY", "")

	orig := stderrIsTTY
	stderrIsTTY = func() bool { return false }
	t.Cleanup(func() { stderrIsTTY = orig })
}

// TestShouldAttemptBrowser_ForceWinsOverHeuristic covers the working
// direction: --browser forces the attempt even when the environment reads as
// headless (the exact agent-native case the flag exists for).
func TestShouldAttemptBrowser_ForceWinsOverHeuristic(t *testing.T) {
	noTTYHeadlessEnv(t)

	if !shouldAttemptBrowser(true, false) {
		t.Error("--browser should force the attempt despite a headless-looking environment")
	}
}

// TestShouldAttemptBrowser_DefaultStillSkips covers the failing-correctly
// direction: without --browser the headless heuristic keeps suppressing the
// attempt, and --headless still forces it off on a real desktop.
func TestShouldAttemptBrowser_DefaultStillSkips(t *testing.T) {
	noTTYHeadlessEnv(t)

	if shouldAttemptBrowser(false, false) {
		t.Error("without --browser, a no-TTY environment must still skip the browser")
	}

	// Interactive desktop, but --headless set: stays off.
	orig := stderrIsTTY
	stderrIsTTY = func() bool { return true }
	t.Cleanup(func() { stderrIsTTY = orig })

	if shouldAttemptBrowser(false, true) {
		t.Error("--headless must keep suppressing the browser on an interactive desktop")
	}
}

// TestRunDeviceFlow_ForcedBrowserOpens pins the plumbing below the flag: with
// the attempt forced on, openBrowser is called with the verification URL and
// the output uses the "Opened ..." wording.
func TestRunDeviceFlow_ForcedBrowserOpens(t *testing.T) {
	srv, _ := newDeviceServer(t, []tokenStep{
		{200, `{"token":"tok-1","host":null,"token_action":"minted"}`},
	})

	var openedURL string
	orig := openBrowser
	openBrowser = func(url string) error {
		openedURL = url
		return nil
	}
	t.Cleanup(func() { openBrowser = orig })

	var buf strings.Builder
	_, err := runDeviceFlow(newClient(srv), &buf, noSleep, true)
	if err != nil {
		t.Fatalf("runDeviceFlow: %v", err)
	}

	if openedURL != "https://id.example.com/device?user_code=BCDF-GHJK" {
		t.Errorf("openBrowser called with %q, want the complete verification URL", openedURL)
	}
	if !strings.Contains(buf.String(), "Opened https://id.example.com/device in your browser.") {
		t.Errorf("output should use the opened-browser wording, got:\n%s", buf.String())
	}
}

// TestRunDeviceFlow_NoAttemptKeepsManualWording: with the attempt off,
// openBrowser is never called and the manual wording is printed.
func TestRunDeviceFlow_NoAttemptKeepsManualWording(t *testing.T) {
	srv, _ := newDeviceServer(t, []tokenStep{
		{200, `{"token":"tok-1","host":null,"token_action":"minted"}`},
	})

	called := false
	orig := openBrowser
	openBrowser = func(string) error {
		called = true
		return nil
	}
	t.Cleanup(func() { openBrowser = orig })

	var buf strings.Builder
	_, err := runDeviceFlow(newClient(srv), &buf, noSleep, noBrowser)
	if err != nil {
		t.Fatalf("runDeviceFlow: %v", err)
	}

	if called {
		t.Error("openBrowser must not be called when the attempt is off")
	}
	if !strings.Contains(buf.String(), "Open https://id.example.com/device in a browser and enter the code.") {
		t.Errorf("output should use the manual wording, got:\n%s", buf.String())
	}
}

// TestSignin_BrowserConflictsWithHeadless confirms the contradictory combo
// fails fast in RunE, before any network call.
func TestSignin_BrowserConflictsWithHeadless(t *testing.T) {
	output.SetWriters(io.Discard, io.Discard)
	t.Cleanup(func() { output.SetWriters(nil, nil) })

	signinForceBrowser = true
	signinForceDevice = true
	t.Cleanup(func() { signinForceBrowser, signinForceDevice = false, false })

	err := signinCmd.RunE(signinCmd, []string{"me.example.com"})
	if err == nil {
		t.Fatal("expected error when --browser is combined with --headless")
	}
	if !strings.Contains(err.Error(), "--browser") || !strings.Contains(err.Error(), "--headless") {
		t.Errorf("error = %q, want it to name both flags", err.Error())
	}
}

// ── validateHost ──────────────────────────────────────────────────────────────

func TestValidateHost_AcceptsBareHostnames(t *testing.T) {
	for _, host := range []string{
		"me.semaphoreci.com",
		"id.semaphoreci.com",
		"localhost",
		"my-onprem.example.com",
		"a.b-c.d",
	} {
		if err := validateHost("host", host); err != nil {
			t.Errorf("validateHost(%q) = %v, want nil", host, err)
		}
	}
}

func TestValidateHost_RejectsNonHostnames(t *testing.T) {
	for _, host := range []string{
		"",
		"victim@evil.com",
		"evil.com/x@real.com",
		"evil.com:8080",
		"https://evil.com",
		"evil.com/path",
		"evil.com?q=1",
		"evil com",
		"-leadinghyphen.com",
		strings.Repeat("a", 254),
	} {
		if err := validateHost("host", host); err == nil {
			t.Errorf("validateHost(%q) = nil, want error", host)
		}
	}
}

// TestSignin_RejectsBadHostArg confirms the fast, before-any-network-call
// guard is actually wired into RunE for the positional [host] argument.
func TestSignin_RejectsBadHostArg(t *testing.T) {
	output.SetWriters(io.Discard, io.Discard)
	t.Cleanup(func() { output.SetWriters(nil, nil) })

	err := signinCmd.RunE(signinCmd, []string{"victim@evil.com"})
	if err == nil {
		t.Fatal("expected error for a host containing userinfo")
	}
	if !strings.Contains(err.Error(), "not a valid hostname") {
		t.Errorf("error = %q, want it to mention invalid hostname", err.Error())
	}
}

// TestSignin_RejectsBadIDHostFlag confirms --id-host, which feeds the
// CLI-auth base URL directly, is validated too.
func TestSignin_RejectsBadIDHostFlag(t *testing.T) {
	output.SetWriters(io.Discard, io.Discard)
	t.Cleanup(func() { output.SetWriters(nil, nil) })

	signinIDHost = "attacker.com/x@real.com"
	t.Cleanup(func() { signinIDHost = "" })

	err := signinCmd.RunE(signinCmd, []string{"me.example.com"})
	if err == nil {
		t.Fatal("expected error for a malformed --id-host")
	}
	if !strings.Contains(err.Error(), "--id-host") {
		t.Errorf("error = %q, want it to mention --id-host", err.Error())
	}
}

// TestSignin_RejectsBadOrgHostFlag confirms --org-host, which becomes the
// saved context host for future URL construction, is validated too.
func TestSignin_RejectsBadOrgHostFlag(t *testing.T) {
	output.SetWriters(io.Discard, io.Discard)
	t.Cleanup(func() { output.SetWriters(nil, nil) })

	signinOrgName = "myorg"
	signinOrgHost = "evil.com:8080"
	t.Cleanup(func() { signinOrgName, signinOrgHost = "", "" })

	err := signinCmd.RunE(signinCmd, []string{"me.example.com"})
	if err == nil {
		t.Fatal("expected error for a malformed --org-host")
	}
	if !strings.Contains(err.Error(), "--org-host") {
		t.Errorf("error = %q, want it to mention --org-host", err.Error())
	}
}

// ── resolveSigninHosts ────────────────────────────────────────────────────────

// TestResolveSigninHosts covers both directions of host resolution: every
// legitimate arg/flag combo resolves to the right (host, authHost), and the
// one dangerous combo (--id-host with no [host]) is rejected before any
// network call, so the token can never be minted against a custom id host and
// then stored under the Semaphore Cloud default context.
func TestResolveSigninHosts(t *testing.T) {
	// Positive: bare signin defaults to Semaphore Cloud for both hosts.
	if host, authHost, err := resolveSigninHosts(nil, ""); err != nil ||
		host != defaultSigninHost || authHost != defaultSigninIDHost {
		t.Errorf("bare signin = (%q, %q, %v), want (%q, %q, nil)",
			host, authHost, err, defaultSigninHost, defaultSigninIDHost)
	}

	// Positive: explicit [host], no --id-host — the id host falls back to [host].
	if host, authHost, err := resolveSigninHosts([]string{"onprem.example.com"}, ""); err != nil ||
		host != "onprem.example.com" || authHost != "onprem.example.com" {
		t.Errorf("explicit host = (%q, %q, %v), want (onprem.example.com, onprem.example.com, nil)",
			host, authHost, err)
	}

	// Positive: explicit [host] AND --id-host — the token is stored under [host].
	if host, authHost, err := resolveSigninHosts([]string{"somehost.example.com"}, "id.example.com"); err != nil ||
		host != "somehost.example.com" || authHost != "id.example.com" {
		t.Errorf("host + id-host = (%q, %q, %v), want (somehost.example.com, id.example.com, nil)",
			host, authHost, err)
	}

	// Negative: --id-host with no [host] must error, before any network call,
	// rather than silently storing the token under me.semaphoreci.com.
	_, _, err := resolveSigninHosts(nil, "id.example.com")
	if err == nil {
		t.Fatal("expected error for --id-host without [host]")
	}
	if !strings.Contains(err.Error(), "--id-host") {
		t.Errorf("error = %q, want it to mention --id-host", err.Error())
	}
}

// TestSignin_IDHostWithoutHost confirms the guard is wired into RunE: an
// --id-host with no positional [host] fails fast, before any device flow runs.
func TestSignin_IDHostWithoutHost(t *testing.T) {
	output.SetWriters(io.Discard, io.Discard)
	t.Cleanup(func() { output.SetWriters(nil, nil) })

	signinIDHost = "id.example.com"
	t.Cleanup(func() { signinIDHost = "" })

	err := signinCmd.RunE(signinCmd, []string{})
	if err == nil {
		t.Fatal("expected error for --id-host without [host]")
	}
	if !strings.Contains(err.Error(), "--id-host") {
		t.Errorf("error = %q, want it to mention --id-host", err.Error())
	}
}

// ── signin command wiring ────────────────────────────────────────────────────

func TestSigninCmd_UseAliasesAndArgs(t *testing.T) {
	if signinCmd.Use != "signin [host]" {
		t.Errorf("Use = %q, want \"signin [host]\"", signinCmd.Use)
	}
	// signup and login converge on the same flow, as aliases.
	for _, alias := range []string{"signup", "login"} {
		if !signinCmd.HasAlias(alias) {
			t.Errorf("signin should have alias %q", alias)
		}
	}
	// Registered under root.
	found := false
	for _, c := range rootCmd.Commands() {
		if c.Name() == "signin" {
			found = true
			break
		}
	}
	if !found {
		t.Error("signin command is not registered on rootCmd")
	}
	// `connect` stays its own command — the manual-token path is untouched.
	for _, c := range rootCmd.Commands() {
		if c.Name() == "connect" && c.HasAlias("signin") {
			t.Error("connect must not alias signin")
		}
	}
	// Host is optional (defaults to Semaphore Cloud); zero or one arg is valid.
	if err := signinCmd.Args(signinCmd, []string{}); err != nil {
		t.Errorf("unexpected error with zero args (should default to prod): %v", err)
	}
	if err := signinCmd.Args(signinCmd, []string{"me.example.com"}); err != nil {
		t.Errorf("unexpected error with one arg: %v", err)
	}
	if err := signinCmd.Args(signinCmd, []string{"a", "b"}); err == nil {
		t.Error("expected error with two args")
	}
}

// TestSigninCmd_DefaultHosts pins the Semaphore Cloud defaults used when signin
// runs with no [host].
func TestSigninCmd_DefaultHosts(t *testing.T) {
	if defaultSigninHost != "me.semaphoreci.com" {
		t.Errorf("defaultSigninHost = %q, want me.semaphoreci.com", defaultSigninHost)
	}
	if defaultSigninIDHost != "id.semaphoreci.com" {
		t.Errorf("defaultSigninIDHost = %q, want id.semaphoreci.com", defaultSigninIDHost)
	}
	// Both defaults must survive the host validator (no scheme/port/userinfo).
	if err := validateHost("host", defaultSigninHost); err != nil {
		t.Errorf("defaultSigninHost rejected by validateHost: %v", err)
	}
	if err := validateHost("--id-host", defaultSigninIDHost); err != nil {
		t.Errorf("defaultSigninIDHost rejected by validateHost: %v", err)
	}
}

// TestSignin_OrgWithoutOrgHost checks the fast fail-before-network guard: --org
// without --org-host must error immediately, before any auth flow runs.
func TestSignin_OrgWithoutOrgHost(t *testing.T) {
	output.SetWriters(io.Discard, io.Discard)
	t.Cleanup(func() { output.SetWriters(nil, nil) })

	signinOrgName = "myorg"
	signinOrgHost = ""
	t.Cleanup(func() { signinOrgName, signinOrgHost = "", "" })

	err := signinCmd.RunE(signinCmd, []string{"me.example.com"})
	if err == nil {
		t.Fatal("expected error when --org is set without --org-host")
	}
	if !strings.Contains(err.Error(), "--org-host") {
		t.Errorf("error = %q, want it to mention --org-host", err.Error())
	}
}

// ── first-org creation (--org) ───────────────────────────────────────────────

// newOrgServer serves POST /api/v1alpha/organizations with a fixed status/body
// and records the last request's auth header and decoded JSON body.
func newOrgServer(t *testing.T, status int, body string) (*httptest.Server, *orgRequestCapture) {
	t.Helper()
	cap := &orgRequestCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1alpha/organizations" {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		if r.Method != "POST" {
			t.Errorf("method = %s, want POST", r.Method)
		}
		atomic.AddInt64(&cap.calls, 1)
		cap.auth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &cap.body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, cap
}

type orgRequestCapture struct {
	calls int64
	auth  string
	body  map[string]string
}

// setupConfig points viper at a fresh temp config file and silences output.
func setupConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".sem.yaml")
	if err := os.WriteFile(cfgPath, []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.SetConfigFile(cfgPath)
	viper.SetConfigType("yaml")
	if err := viper.ReadInConfig(); err != nil {
		t.Fatalf("ReadInConfig: %v", err)
	}
	output.SetWriters(io.Discard, io.Discard)
	t.Cleanup(func() { output.SetWriters(nil, nil) })
}

func TestCreateFirstOrg_Success(t *testing.T) {
	setupConfig(t)
	srv, cap := newOrgServer(t, 200, `{"organization_id":"org-123","name":"myorg","username":"myorg"}`)

	err := createFirstOrg(srv.Client(), srv.URL, "acct-token", "myorg", "myorg.semaphoreci.com", io.Discard)
	if err != nil {
		t.Fatalf("createFirstOrg: %v", err)
	}

	// Request shape: token auth + username in body.
	if cap.auth != "Token acct-token" {
		t.Errorf("Authorization = %q, want \"Token acct-token\"", cap.auth)
	}
	if cap.body["username"] != "myorg" {
		t.Errorf("request username = %q, want myorg", cap.body["username"])
	}

	// New org context is stored and active.
	if got := viper.GetString("active-context"); got != "myorg_semaphoreci_com" {
		t.Errorf("active-context = %q, want myorg_semaphoreci_com", got)
	}
	if got := viper.GetString("contexts.myorg_semaphoreci_com.auth.token"); got != "acct-token" {
		t.Errorf("org token = %q, want acct-token", got)
	}
	if got := viper.GetString("contexts.myorg_semaphoreci_com.host"); got != "myorg.semaphoreci.com" {
		t.Errorf("org host = %q, want myorg.semaphoreci.com", got)
	}
}

// TestCreateFirstOrg_FailurePreservesAccount confirms a create failure is
// surfaced clearly and does NOT roll back the already-saved account context.
func TestCreateFirstOrg_FailurePreservesAccount(t *testing.T) {
	setupConfig(t)

	// Pre-existing account context, active — as signin would have saved it.
	if _, err := writeContext("me.example.com", "acct-token", true); err != nil {
		t.Fatal(err)
	}

	srv, _ := newOrgServer(t, 422, `"Organization name is already taken"`)

	err := createFirstOrg(srv.Client(), srv.URL, "acct-token", "myorg", "myorg.semaphoreci.com", io.Discard)
	if err == nil {
		t.Fatal("expected error on org-create failure")
	}
	if !strings.Contains(err.Error(), "already taken") {
		t.Errorf("error = %q, want it to surface the server message", err.Error())
	}

	// Account context is intact and still active; org context was not created.
	if got := viper.GetString("active-context"); got != "me_example_com" {
		t.Errorf("active-context = %q, want me_example_com (account preserved)", got)
	}
	if got := viper.GetString("contexts.me_example_com.auth.token"); got != "acct-token" {
		t.Errorf("account token = %q, want acct-token", got)
	}
	if got := viper.GetString("contexts.myorg_semaphoreci_com.host"); got != "" {
		t.Errorf("org context should not exist, got host = %q", got)
	}
}

// ── finishSignin: org creation gating on token_action ───────────────────────

// TestFinishSignin_MintedCreatesOrg covers the legitimate new-account path:
// a minted token with --org creates the org and activates its context.
func TestFinishSignin_MintedCreatesOrg(t *testing.T) {
	setupConfig(t)
	srv, cap := newOrgServer(t, 200, `{"organization_id":"org-1","name":"myorg","username":"myorg"}`)

	tok := &tokenResp{Token: "acct-token", TokenAction: "minted"}
	err := finishSignin(tok, "me.example.com", "myorg", "myorg.example.com", srv.Client(), srv.URL, io.Discard)
	if err != nil {
		t.Fatalf("finishSignin: %v", err)
	}

	if got := atomic.LoadInt64(&cap.calls); got != 1 {
		t.Errorf("org create calls = %d, want 1", got)
	}
	if got := viper.GetString("active-context"); got != "myorg_example_com" {
		t.Errorf("active-context = %q, want myorg_example_com", got)
	}
	// The account context was saved too, before the org call.
	if got := viper.GetString("contexts.me_example_com.auth.token"); got != "acct-token" {
		t.Errorf("account token = %q, want acct-token", got)
	}
}

// TestFinishSignin_LegacyServerCreatesOrg: an empty token_action (older guard,
// which only ever minted) must not lose the --org convenience.
func TestFinishSignin_LegacyServerCreatesOrg(t *testing.T) {
	setupConfig(t)
	srv, cap := newOrgServer(t, 200, `{"organization_id":"org-1","name":"myorg","username":"myorg"}`)

	tok := &tokenResp{Token: "acct-token"}
	err := finishSignin(tok, "me.example.com", "myorg", "myorg.example.com", srv.Client(), srv.URL, io.Discard)
	if err != nil {
		t.Fatalf("finishSignin: %v", err)
	}
	if got := atomic.LoadInt64(&cap.calls); got != 1 {
		t.Errorf("org create calls = %d, want 1", got)
	}
}

// TestFinishSignin_RotatedSkipsOrg covers the fail-correctly direction of the
// gate: an existing account (token rotated) signs in fine, but --org is
// skipped with a note and no create request is ever sent.
func TestFinishSignin_RotatedSkipsOrg(t *testing.T) {
	setupConfig(t)
	srv, cap := newOrgServer(t, 200, `{"organization_id":"org-1","name":"myorg","username":"myorg"}`)

	var note strings.Builder
	tok := &tokenResp{Token: "acct-token", TokenAction: "rotated"}
	err := finishSignin(tok, "me.example.com", "myorg", "myorg.example.com", srv.Client(), srv.URL, &note)
	if err != nil {
		t.Fatalf("finishSignin: %v", err)
	}

	if got := atomic.LoadInt64(&cap.calls); got != 0 {
		t.Errorf("org create calls = %d, want 0 (existing account)", got)
	}
	if !strings.Contains(note.String(), "skipping organization creation") {
		t.Errorf("expected a skip note, got: %q", note.String())
	}
	// The sign-in itself still completed: account context saved and active.
	if got := viper.GetString("active-context"); got != "me_example_com" {
		t.Errorf("active-context = %q, want me_example_com", got)
	}
	if got := viper.GetString("contexts.me_example_com.auth.token"); got != "acct-token" {
		t.Errorf("account token = %q, want acct-token", got)
	}
	// No org context was created.
	if got := viper.GetString("contexts.myorg_example_com.host"); got != "" {
		t.Errorf("org context should not exist, got host = %q", got)
	}
}

// TestFinishSignin_NoOrgSavesAccount: the plain path (no --org) saves the
// account context and activates it, for both minted and rotated sign-ins.
func TestFinishSignin_NoOrgSavesAccount(t *testing.T) {
	for _, action := range []string{"minted", "rotated", ""} {
		t.Run("action="+action, func(t *testing.T) {
			setupConfig(t)

			tok := &tokenResp{Token: "acct-token", TokenAction: action}
			err := finishSignin(tok, "me.example.com", "", "", nil, "", io.Discard)
			if err != nil {
				t.Fatalf("finishSignin: %v", err)
			}
			if got := viper.GetString("active-context"); got != "me_example_com" {
				t.Errorf("active-context = %q, want me_example_com", got)
			}
			if got := viper.GetString("contexts.me_example_com.auth.token"); got != "acct-token" {
				t.Errorf("account token = %q, want acct-token", got)
			}
		})
	}
}
