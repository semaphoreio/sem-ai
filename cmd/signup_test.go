package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
				"user_code": "ABCD-EFGH",
				"verification_uri": "https://id.example.com/device",
				"verification_uri_complete": "https://id.example.com/device?user_code=ABCD-EFGH",
				"expires_in": 900,
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

func TestRunDeviceFlow_PendingThenSuccess(t *testing.T) {
	srv, polls := newDeviceServer(t, []tokenStep{
		{400, `{"error":"authorization_pending"}`},
		{400, `{"error":"authorization_pending"}`},
		{200, `{"token":"tok-123","host":null}`},
	})

	tok, err := runDeviceFlow(newClient(srv), io.Discard, noSleep)
	if err != nil {
		t.Fatalf("runDeviceFlow: %v", err)
	}
	if tok.Token != "tok-123" {
		t.Errorf("token = %q, want tok-123", tok.Token)
	}
	if tok.Host != "" {
		t.Errorf("host = %q, want empty (server returns null)", tok.Host)
	}
	if got := atomic.LoadInt64(polls); got != 3 {
		t.Errorf("token polls = %d, want 3", got)
	}
}

func TestRunDeviceFlow_SlowDownThenSuccess(t *testing.T) {
	srv, _ := newDeviceServer(t, []tokenStep{
		{400, `{"error":"slow_down"}`},
		{200, `{"token":"tok-abc","host":null}`},
	})

	tok, err := runDeviceFlow(newClient(srv), io.Discard, noSleep)
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

	_, err := runDeviceFlow(newClient(srv), io.Discard, noSleep)
	if err == nil {
		t.Fatal("expected error on access_denied")
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Errorf("error = %q, want it to mention denial", err.Error())
	}
}

func TestRunDeviceFlow_ExpiredToken(t *testing.T) {
	srv, _ := newDeviceServer(t, []tokenStep{
		{400, `{"error":"expired_token"}`},
	})

	_, err := runDeviceFlow(newClient(srv), io.Discard, noSleep)
	if err == nil {
		t.Fatal("expected error on expired_token")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("error = %q, want it to mention expiry", err.Error())
	}
}

func TestRunDeviceFlow_TokenExists(t *testing.T) {
	msg := "This account already exists. Run `sem-ai connect <host> <token>` with a token from your Semaphore settings."
	srv, _ := newDeviceServer(t, []tokenStep{
		{409, `{"error":"token_exists","message":"` + msg + `"}`},
	})

	_, err := runDeviceFlow(newClient(srv), io.Discard, noSleep)
	if err == nil {
		t.Fatal("expected error on token_exists")
	}
	if !strings.Contains(err.Error(), "sem-ai connect") {
		t.Errorf("error = %q, want the connect instruction from the server", err.Error())
	}
}

func TestRunDeviceFlow_UnknownError(t *testing.T) {
	srv, _ := newDeviceServer(t, []tokenStep{
		{400, `{"error":"invalid_grant"}`},
	})

	_, err := runDeviceFlow(newClient(srv), io.Discard, noSleep)
	if err == nil {
		t.Fatal("expected error on invalid_grant")
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("error = %q, want invalid_grant", err.Error())
	}
}

func TestSaveLoginContext_WritesTokenAndSetsPerms(t *testing.T) {
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

	if err := saveLoginContext("myorg.semaphoreci.com", "secret-token"); err != nil {
		t.Fatalf("saveLoginContext: %v", err)
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
		t.Error("isHeadless(true) should force device flow")
	}

	t.Setenv("SSH_CONNECTION", "10.0.0.1 22 10.0.0.2 2222")
	if !isHeadless(false) {
		t.Error("isHeadless should detect an SSH session")
	}
}

// TestIsHeadless_NoTTYFallsBackToDevice covers the "falls back when it
// should" side of the no-TTY signal: a non-interactive stdin (piped, run
// from a script, headless automation) means there's no one to complete a
// browser sign-in, so signup should go straight to the device flow.
func TestIsHeadless_NoTTYFallsBackToDevice(t *testing.T) {
	t.Setenv("SSH_TTY", "")
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("DISPLAY", "not-empty")
	t.Setenv("WAYLAND_DISPLAY", "")

	orig := stderrIsTTY
	stderrIsTTY = func() bool { return false }
	t.Cleanup(func() { stderrIsTTY = orig })

	if !isHeadless(false) {
		t.Error("isHeadless should force the device flow when there's no interactive terminal")
	}
}

// TestIsHeadless_InteractiveDesktopStaysOnLoopback covers the "works when it
// should" side: a real interactive desktop session (TTY on stdin, no SSH, a
// display) must not be routed to the device flow — the no-TTY signal must
// not regress the working browser path.
func TestIsHeadless_InteractiveDesktopStaysOnLoopback(t *testing.T) {
	t.Setenv("SSH_TTY", "")
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("DISPLAY", "not-empty")
	t.Setenv("WAYLAND_DISPLAY", "")

	orig := stderrIsTTY
	stderrIsTTY = func() bool { return true }
	t.Cleanup(func() { stderrIsTTY = orig })

	if isHeadless(false) {
		t.Error("isHeadless should not force the device flow for an interactive desktop session")
	}
}

// ── loopback flow: false-success and timeout fallback ───────────────────────

// TestRunLoopbackFlow_BrowserUnavailable covers the existing fallback path: if
// openBrowser itself fails (e.g. the opener exits nonzero, or the binary is
// missing), runLoopbackFlow must report errBrowserUnavailable immediately.
func TestRunLoopbackFlow_BrowserUnavailable(t *testing.T) {
	origOpen := openBrowser
	openBrowser = func(string) error { return errors.New("no handler for URL") }
	t.Cleanup(func() { openBrowser = origOpen })

	c := &cliAuthClient{baseURL: "https://id.example.com", http: &http.Client{}}
	_, err := runLoopbackFlow(context.Background(), c, io.Discard)
	if !errors.Is(err, errBrowserUnavailable) {
		t.Fatalf("err = %v, want errBrowserUnavailable", err)
	}
}

// TestRunLoopbackFlow_TimesOutFallsBackToDevice is the HIGH-3 regression
// test: simulate a false success (openBrowser's cmd.Start() returns nil, but
// no browser ever completes the redirect — the "$DISPLAY set but the opener
// is broken" scenario) and verify runLoopbackFlow reports errLoopbackTimedOut
// — so the caller in RunE falls back to device flow — instead of a bare,
// unrecoverable error, and does so within the (shortened, for the test)
// timeout rather than hanging.
func TestRunLoopbackFlow_TimesOutFallsBackToDevice(t *testing.T) {
	origOpen := openBrowser
	openBrowser = func(string) error { return nil } // false success: never calls back
	t.Cleanup(func() { openBrowser = origOpen })

	origTimeout := loopbackTimeout
	loopbackTimeout = 50 * time.Millisecond
	t.Cleanup(func() { loopbackTimeout = origTimeout })

	c := &cliAuthClient{baseURL: "https://id.example.com", http: &http.Client{}}
	start := time.Now()
	_, err := runLoopbackFlow(context.Background(), c, io.Discard)
	if !errors.Is(err, errLoopbackTimedOut) {
		t.Fatalf("err = %v, want errLoopbackTimedOut", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("runLoopbackFlow took %v, want it to respect the shortened loopbackTimeout", elapsed)
	}
}

// TestRunLoopbackFlow_Success is the positive-path counterpart: a real
// browser (simulated by hitting the loopback redirect_uri with the state and
// an authorization code, exactly like guard's /cli/signup page does) must
// still complete the flow and mint a token. Proves the false-success fix
// above does not regress the working desktop path.
func TestRunLoopbackFlow_Success(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cli/token" {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		if got := r.PostForm.Get("grant_type"); got != authCodeGrantType {
			t.Errorf("grant_type = %q, want %q", got, authCodeGrantType)
		}
		if got := r.PostForm.Get("code"); got != "auth-code-xyz" {
			t.Errorf("code = %q, want auth-code-xyz", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"token":"tok-loopback","host":null}`)
	}))
	t.Cleanup(tokenSrv.Close)

	var wg sync.WaitGroup
	origOpen := openBrowser
	openBrowser = func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			t.Errorf("parse authURL: %v", err)
			return err
		}
		q := u.Query()
		wg.Add(1)
		go func() {
			defer wg.Done()
			cbURL := q.Get("redirect_uri") + "?state=" + q.Get("state") + "&code=auth-code-xyz"
			resp, err := http.Get(cbURL)
			if err != nil {
				t.Errorf("callback GET failed: %v", err)
				return
			}
			resp.Body.Close()
		}()
		return nil
	}
	t.Cleanup(func() { openBrowser = origOpen })

	c := &cliAuthClient{baseURL: tokenSrv.URL, http: tokenSrv.Client()}
	tok, err := runLoopbackFlow(context.Background(), c, io.Discard)
	wg.Wait()
	if err != nil {
		t.Fatalf("runLoopbackFlow: %v", err)
	}
	if tok.Token != "tok-loopback" {
		t.Errorf("token = %q, want tok-loopback", tok.Token)
	}
}

// ── validateHost ──────────────────────────────────────────────────────────────

func TestValidateHost_AcceptsBareHostnames(t *testing.T) {
	valid := []string{
		"me.semaphoreci.com",
		"localhost",
		"id-host2.example.co",
		"a.b.c.d.example.org",
	}
	for _, h := range valid {
		if err := validateHost("host", h); err != nil {
			t.Errorf("validateHost(%q) = %v, want nil", h, err)
		}
	}
}

// TestValidateHost_RejectsNonHostnames covers the MEDIUM fix: a userinfo
// trick (victim@evil.com), an embedded scheme, a path, a port, or a
// query/fragment must all be rejected — only a bare hostname is accepted.
func TestValidateHost_RejectsNonHostnames(t *testing.T) {
	invalid := []string{
		"",
		"victim@evil.com",
		"me.semaphoreci.com@evil.com",
		"http://evil.com",
		"https://evil.com",
		"evil.com/path",
		"evil.com:8080",
		"evil.com?x=1",
		"evil.com#frag",
		"evil .com",
		"evil.com\t",
	}
	for _, h := range invalid {
		if err := validateHost("host", h); err == nil {
			t.Errorf("validateHost(%q) = nil, want error", h)
		}
	}
}

// TestSignup_RejectsBadHostArg confirms the fast, before-any-network-call
// guard is actually wired into RunE for the positional <host> argument.
func TestSignup_RejectsBadHostArg(t *testing.T) {
	output.SetWriters(io.Discard, io.Discard)
	t.Cleanup(func() { output.SetWriters(nil, nil) })

	err := signupCmd.RunE(signupCmd, []string{"victim@evil.com"})
	if err == nil {
		t.Fatal("expected error for a host containing userinfo")
	}
	if !strings.Contains(err.Error(), "not a valid hostname") {
		t.Errorf("error = %q, want it to mention invalid hostname", err.Error())
	}
}

// TestSignup_RejectsBadIDHostFlag confirms --id-host, which feeds the
// CLI-auth base URL directly, is validated too.
func TestSignup_RejectsBadIDHostFlag(t *testing.T) {
	output.SetWriters(io.Discard, io.Discard)
	t.Cleanup(func() { output.SetWriters(nil, nil) })

	loginIDHost = "attacker.com/x@real.com"
	t.Cleanup(func() { loginIDHost = "" })

	err := signupCmd.RunE(signupCmd, []string{"me.example.com"})
	if err == nil {
		t.Fatal("expected error for a malformed --id-host")
	}
	if !strings.Contains(err.Error(), "--id-host") {
		t.Errorf("error = %q, want it to mention --id-host", err.Error())
	}
}

// TestSignup_RejectsBadOrgHostFlag confirms --org-host, which becomes the
// saved context host for future URL construction, is validated too.
func TestSignup_RejectsBadOrgHostFlag(t *testing.T) {
	output.SetWriters(io.Discard, io.Discard)
	t.Cleanup(func() { output.SetWriters(nil, nil) })

	signupOrgName = "myorg"
	signupOrgHost = "evil.com:8080"
	t.Cleanup(func() { signupOrgName, signupOrgHost = "", "" })

	err := signupCmd.RunE(signupCmd, []string{"me.example.com"})
	if err == nil {
		t.Fatal("expected error for a malformed --org-host")
	}
	if !strings.Contains(err.Error(), "--org-host") {
		t.Errorf("error = %q, want it to mention --org-host", err.Error())
	}
}

// ── signup command wiring ────────────────────────────────────────────────────

func TestSignupCmd_UseAndArgs(t *testing.T) {
	if signupCmd.Use != "signup [host]" {
		t.Errorf("Use = %q, want \"signup [host]\"", signupCmd.Use)
	}
	// Registered under root.
	found := false
	for _, c := range rootCmd.Commands() {
		if c.Name() == "signup" {
			found = true
			break
		}
	}
	if !found {
		t.Error("signup command is not registered on rootCmd")
	}
	// Host is optional (defaults to Semaphore Cloud); zero or one arg is valid.
	if err := signupCmd.Args(signupCmd, []string{}); err != nil {
		t.Errorf("unexpected error with zero args (should default to prod): %v", err)
	}
	if err := signupCmd.Args(signupCmd, []string{"me.example.com"}); err != nil {
		t.Errorf("unexpected error with one arg: %v", err)
	}
	if err := signupCmd.Args(signupCmd, []string{"a", "b"}); err == nil {
		t.Error("expected error with two args")
	}
}

// TestSignupCmd_DefaultHosts pins the Semaphore Cloud defaults used when signup
// runs with no [host].
func TestSignupCmd_DefaultHosts(t *testing.T) {
	if defaultSignupHost != "me.semaphoreci.com" {
		t.Errorf("defaultSignupHost = %q, want me.semaphoreci.com", defaultSignupHost)
	}
	if defaultSignupIDHost != "id.semaphoreci.com" {
		t.Errorf("defaultSignupIDHost = %q, want id.semaphoreci.com", defaultSignupIDHost)
	}
	// Both defaults must survive the host validator (no scheme/port/userinfo).
	if err := validateHost("host", defaultSignupHost); err != nil {
		t.Errorf("defaultSignupHost rejected by validateHost: %v", err)
	}
	if err := validateHost("--id-host", defaultSignupIDHost); err != nil {
		t.Errorf("defaultSignupIDHost rejected by validateHost: %v", err)
	}
}

// TestSignup_OrgWithoutOrgHost checks the fast fail-before-network guard: --org
// without --org-host must error immediately, before any auth flow runs.
func TestSignup_OrgWithoutOrgHost(t *testing.T) {
	output.SetWriters(io.Discard, io.Discard)
	t.Cleanup(func() { output.SetWriters(nil, nil) })

	signupOrgName = "myorg"
	signupOrgHost = ""
	t.Cleanup(func() { signupOrgName, signupOrgHost = "", "" })

	err := signupCmd.RunE(signupCmd, []string{"me.example.com"})
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
	auth string
	body map[string]string
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

	// Pre-existing account context, active — as signup would have saved it.
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
