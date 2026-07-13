package cmd

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// `signup` creates a NEW Semaphore account. It obtains a fresh API token through
// guard's CLI-auth endpoints and stores it in the same config slot as `connect`.
// It is a one-time onboarding command: existing users should use `sem-ai connect`
// with a token from their Semaphore settings (guard rejects an already-registered
// account with the token_exists error, whose message points there).
//
// It supports the two flows the guard side exposes (see guard/lib/guard/cli_auth.ex):
//
//   - Loopback + PKCE (RFC 8252) — used when a browser is available. The CLI
//     binds a localhost server, opens the browser to guard's /cli/signup with a
//     PKCE challenge + loopback redirect_uri, receives a one-time code on the
//     loopback, and exchanges it at POST /cli/token (grant_type=authorization_code).
//   - Device authorization grant (RFC 8628) — used when headless. The CLI calls
//     POST /cli/device for a user_code + verification_uri, prints them, and polls
//     POST /cli/token (grant_type=urn:ietf:params:oauth:grant-type:device_code)
//     until the human approves.
//
// Both token responses are {"token": "...", "host": null}: guard returns a null
// host (a fresh signup has no org host yet), so the saved host comes from the
// positional <host> argument, exactly like `connect`.
//
// With --org, after the account+token are saved, signup also creates a first
// organization via POST <host>/api/v1alpha/organizations (an account-level
// endpoint served from the me.<domain> host, authenticated with the new account
// token). The create response returns only {organization_id, name, username} —
// NOT the org's host — so the org's context host must be supplied explicitly with
// --org-host, and that context is then made active.

const (
	loginCallbackPath = "/callback"

	// Semaphore Cloud defaults used when signup is run with no [host].
	// defaultSignupHost is the account-level host; defaultSignupIDHost serves
	// guard's CLI-auth endpoints. Both are overridable ([host] / --id-host).
	defaultSignupHost   = "me.semaphoreci.com"
	defaultSignupIDHost = "id.semaphoreci.com"

	deviceGrantType   = "urn:ietf:params:oauth:grant-type:device_code"
	authCodeGrantType = "authorization_code"

	minPollInterval    = 1 * time.Second
	slowDownIncrement  = 5 * time.Second
	cliAuthHTTPTimeout = 30 * time.Second
	deviceDefaultTTL   = 900 // seconds; fallback if the server omits expires_in
)

// loopbackTimeout bounds how long runLoopbackFlow waits for the browser to
// complete the redirect back to the loopback listener. It's a var (not a
// const) so tests can shrink it instead of actually waiting.
//
// cmd.Start() succeeding (see openBrowser in open.go) does not mean a
// browser actually came up — on a broken/headless opener the process can
// spawn fine and never render anything. This timeout is the backstop for
// that silent failure: on expiry we don't hard-fail, we return
// errLoopbackTimedOut so the caller falls back to the device flow, same as
// when opening the browser fails outright.
var loopbackTimeout = 3 * time.Minute

var (
	loginForceDevice bool
	loginIDHost      string
	signupOrgName    string
	signupOrgHost    string
)

// errBrowserUnavailable signals that the browser could not be opened, so the
// caller should fall back to the device flow.
var errBrowserUnavailable = errors.New("no browser available")

// errLoopbackTimedOut signals that the loopback listener never received a
// callback within loopbackTimeout. openBrowser's cmd.Start() succeeding is
// not proof a browser actually came up, so this is treated the same as
// errBrowserUnavailable: the caller falls back to the device flow.
var errLoopbackTimedOut = errors.New("timed out waiting for browser sign-in")

// isHeadless reports whether to skip the browser and use the device flow.
// Declared as a var so tests can override it.
var isHeadless = func(force bool) bool {
	if force {
		return true
	}
	if os.Getenv("SSH_TTY") != "" || os.Getenv("SSH_CONNECTION") != "" {
		return true
	}
	if runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return true
	}
	// No interactive terminal (piped/redirected, run from a script,
	// non-interactive CI) — there's no one to see the browser or complete a
	// sign-in in it, so go straight to device flow rather than risk the
	// loopback flow's silent-failure hang. Reuses the same TTY probe as the
	// update-notice logic in version.go.
	if !stderrIsTTY() {
		return true
	}
	return false
}

// hostnameRe allowlists bare hostnames (RFC 1123 labels, dot-separated).
// Deliberately excludes every character a scheme, userinfo, path, query,
// fragment, or port would need (":", "/", "@", "?", "#", whitespace, ...),
// so anything other than a plain hostname is rejected by construction rather
// than by trying to enumerate bad patterns.
var hostnameRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,62}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,62}[a-zA-Z0-9])?)*$`)

// validateHost rejects anything that isn't a bare hostname. signup builds
// URLs by concatenating "https://" with the <host> argument and the
// --id-host/--org-host flags, so a value like "victim@evil.com" (userinfo),
// "evil.com/x@real.com" (path), or "evil.com:8080" (port) could redirect
// where the token or auth requests actually go. Fails fast, before any
// network call.
func validateHost(flagName, host string) error {
	if host == "" {
		return fmt.Errorf("%s must not be empty", flagName)
	}
	if len(host) > 253 {
		return fmt.Errorf("%s %q is too long to be a hostname", flagName, host)
	}
	if !hostnameRe.MatchString(host) {
		return fmt.Errorf("%s %q is not a valid hostname — expected a bare host like me.semaphoreci.com (no scheme, userinfo, path, port, or query)", flagName, host)
	}
	return nil
}

var signupCmd = &cobra.Command{
	Use:   "signup [host]",
	Short: "Create a new Semaphore account and save an API token",
	Long: `Create a NEW Semaphore account and store an API token.

Defaults to Semaphore Cloud (me.semaphoreci.com). Pass a different [host] (and
--id-host for the CLI-auth endpoints) to sign up against another deployment.

signup is a one-time onboarding command. If you already have a Semaphore
account, use 'sem-ai connect <host> <token>' with a token from your Semaphore
settings instead; signup refuses an account that already exists.

Prefers a browser-based loopback + PKCE flow. When no browser is available
(SSH session, no display, or --headless), it falls back to the device
authorization grant: it prints a code and URL for you to open elsewhere and
polls until you approve.

With --org, signup also creates a first organization after the account is
ready. The account token authenticates the create call against the account-level
endpoint on [host]. The create API does not return the new org's host, so pass
--org-host with the org's host; that context is then made active.`,
	Args: cobra.MaximumNArgs(1),
	Example: `  sem-ai signup
  sem-ai signup --headless
  sem-ai signup me.semaphoreci.com --id-host id.semaphoreci.com
  sem-ai signup my-onprem.example.com --org myorg --org-host myorg.example.com`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Default to Semaphore Cloud; a positional host overrides for on-prem
		// or other deployments.
		host := defaultSignupHost
		if len(args) > 0 {
			host = args[0]
		}

		// Reject anything but a bare hostname before touching the network — a
		// userinfo/scheme/path/port trick in <host>, --id-host, or --org-host
		// could otherwise reroute where the token or auth requests go.
		if err := validateHost("host", host); err != nil {
			output.Error("signup_error", err.Error(), 1)
			return err
		}
		if loginIDHost != "" {
			if err := validateHost("--id-host", loginIDHost); err != nil {
				output.Error("signup_error", err.Error(), 1)
				return err
			}
		}
		if signupOrgHost != "" {
			if err := validateHost("--org-host", signupOrgHost); err != nil {
				output.Error("signup_error", err.Error(), 1)
				return err
			}
		}

		// Validate the org flag combo before touching the network, so a bad
		// invocation fails fast without creating a half-finished account.
		if signupOrgName != "" && signupOrgHost == "" {
			err := errors.New("--org requires --org-host: the create API does not return the new org's host, so it must be given explicitly")
			output.Error("signup_error", err.Error(), 1)
			return err
		}

		// The CLI-auth endpoints (/cli/device, /cli/token, /cli/signup) live on
		// guard. --id-host overrides; otherwise for the default (Semaphore Cloud)
		// use its guard host, and for an explicit [host] fall back to that host.
		authHost := loginIDHost
		if authHost == "" {
			if len(args) == 0 {
				authHost = defaultSignupIDHost
			} else {
				authHost = host
			}
		}

		c := &cliAuthClient{
			baseURL: "https://" + authHost,
			http:    &http.Client{Timeout: cliAuthHTTPTimeout},
		}
		w := cmd.ErrOrStderr()

		var tok *tokenResp
		var err error
		if isHeadless(loginForceDevice) {
			tok, err = runDeviceFlow(c, w, time.Sleep)
		} else {
			tok, err = runLoopbackFlow(cmd.Context(), c, w)
			switch {
			case errors.Is(err, errBrowserUnavailable):
				fmt.Fprintln(w, "Could not open a browser; falling back to device authorization.")
				tok, err = runDeviceFlow(c, w, time.Sleep)
			case errors.Is(err, errLoopbackTimedOut):
				fmt.Fprintln(w, "Browser sign-in did not complete in time; falling back to device authorization.")
				tok, err = runDeviceFlow(c, w, time.Sleep)
			}
		}
		if err != nil {
			output.Error("signup_error", err.Error(), 1)
			return err
		}

		saveHost := host
		if tok.Host != "" {
			saveHost = tok.Host
		}

		// No org requested: save the account context and report it.
		if signupOrgName == "" {
			return saveLoginContext(saveHost, tok.Token)
		}

		// Optional first-org creation. Save the account context silently first,
		// then create the org and let createFirstOrg report the final, activated
		// org context. If org creation fails we surface a clear error but do NOT
		// roll the account context back — the account exists and the token works.
		if _, err := writeContext(saveHost, tok.Token, true); err != nil {
			output.Error("config_error", fmt.Sprintf("failed to write config: %s", err), 1)
			return err
		}
		orgc := &http.Client{Timeout: cliAuthHTTPTimeout}
		return createFirstOrg(orgc, "https://"+host, tok.Token, signupOrgName, signupOrgHost, w)
	},
}

// ── HTTP client for guard's CLI-auth endpoints ──────────────────────────────

type cliAuthClient struct {
	baseURL string // e.g. https://id.semaphoreci.com (no trailing slash)
	http    *http.Client
}

type tokenResp struct {
	Token string `json:"token"`
	Host  string `json:"host"`
}

type deviceAuth struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// cliAuthError carries the server's error code (and message when present) from a
// non-200 /cli/token or /cli/device response.
type cliAuthError struct {
	Code    string
	Message string
	Status  int
}

func (e *cliAuthError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

// requestDevice performs POST /cli/device (RFC 8628 device authorization request).
func (c *cliAuthClient) requestDevice() (*deviceAuth, error) {
	req, err := http.NewRequest("POST", c.baseURL+"/cli/device", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", client.UserAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, parseCLIError(resp.StatusCode, body)
	}
	var da deviceAuth
	if err := json.Unmarshal(body, &da); err != nil {
		return nil, fmt.Errorf("invalid device authorization response: %w", err)
	}
	if da.DeviceCode == "" {
		return nil, fmt.Errorf("device authorization response missing device_code")
	}
	return &da, nil
}

// postToken performs POST /cli/token. On HTTP 200 it returns the token; on any
// other status it returns a *cliAuthError (never both). A transport error is
// returned as the third value.
func (c *cliAuthClient) postToken(form url.Values) (*tokenResp, *cliAuthError, error) {
	req, err := http.NewRequestWithContext(context.Background(), "POST", c.baseURL+"/cli/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", client.UserAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode == 200 {
		var tr tokenResp
		if err := json.Unmarshal(body, &tr); err != nil {
			return nil, nil, fmt.Errorf("invalid token response: %w", err)
		}
		if tr.Token == "" {
			return nil, nil, fmt.Errorf("empty token in response")
		}
		return &tr, nil, nil
	}
	return nil, parseCLIError(resp.StatusCode, body), nil
}

func parseCLIError(status int, body []byte) *cliAuthError {
	var e struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &e)
	code := e.Error
	if code == "" {
		code = fmt.Sprintf("http_%d", status)
	}
	return &cliAuthError{Code: code, Message: e.Message, Status: status}
}

// ── device authorization grant (RFC 8628) ───────────────────────────────────

func runDeviceFlow(c *cliAuthClient, w io.Writer, sleep func(time.Duration)) (*tokenResp, error) {
	da, err := c.requestDevice()
	if err != nil {
		return nil, err
	}

	fmt.Fprintf(w, "\nTo sign in, visit:\n  %s\n\nand enter the code:\n\n    %s\n\n", da.VerificationURI, da.UserCode)
	if da.VerificationURIComplete != "" {
		fmt.Fprintf(w, "Or open this URL to skip entering the code:\n  %s\n\n", da.VerificationURIComplete)
	}
	fmt.Fprintln(w, "Waiting for authorization...")

	interval := time.Duration(da.Interval) * time.Second
	if interval < minPollInterval {
		interval = minPollInterval
	}
	expiresIn := da.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = deviceDefaultTTL
	}
	deadline := time.Now().Add(time.Duration(expiresIn) * time.Second)

	form := url.Values{
		"grant_type":  {deviceGrantType},
		"device_code": {da.DeviceCode},
	}

	// Poll immediately (paced by interval) — never gate on a keypress.
	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("device code expired before authorization completed")
		}
		sleep(interval)

		tok, apiErr, err := c.postToken(form)
		if err != nil {
			return nil, err
		}
		if tok != nil {
			return tok, nil
		}

		switch apiErr.Code {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += slowDownIncrement
			continue
		case "access_denied":
			return nil, fmt.Errorf("authorization was denied")
		case "expired_token":
			return nil, fmt.Errorf("device code expired before authorization completed")
		case "token_exists":
			return nil, apiErr // server message tells the user to run `sem-ai connect`
		default:
			return nil, apiErr
		}
	}
}

// ── loopback + PKCE (RFC 8252) ───────────────────────────────────────────────

type loopbackResult struct {
	code string
	err  error
}

func runLoopbackFlow(ctx context.Context, c *cliAuthClient, w io.Writer) (*tokenResp, error) {
	verifier := randToken(32)
	challenge := s256Challenge(verifier)
	state := randToken(16)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to bind loopback listener: %w", err)
	}
	defer listener.Close()
	redirectURI := fmt.Sprintf("http://%s%s", listener.Addr().String(), loginCallbackPath)

	// Start the local server before opening the browser, and never block on a
	// keypress, so the redirect can never race ahead of the listener.
	resultCh := make(chan loopbackResult, 1)
	srv := &http.Server{Handler: loopbackHandler(state, resultCh)}
	go func() { _ = srv.Serve(listener) }()
	defer srv.Close()

	q := url.Values{
		"redirect_uri":          {redirectURI},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	authURL := c.baseURL + "/cli/signup?" + q.Encode()

	if err := openBrowser(authURL); err != nil {
		return nil, errBrowserUnavailable
	}
	fmt.Fprintf(w, "Opened your browser to sign in. If it didn't open, visit:\n  %s\n", authURL)

	select {
	case res := <-resultCh:
		if res.err != nil {
			return nil, res.err
		}
		form := url.Values{
			"grant_type":    {authCodeGrantType},
			"code":          {res.code},
			"code_verifier": {verifier},
			"redirect_uri":  {redirectURI},
		}
		tok, apiErr, err := c.postToken(form)
		if err != nil {
			return nil, err
		}
		if apiErr != nil {
			return nil, apiErr
		}
		return tok, nil
	case <-time.After(loopbackTimeout):
		return nil, errLoopbackTimedOut
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// loopbackHandler captures the loopback redirect, verifies state, and reports the
// authorization code (or an error) over resultCh.
func loopbackHandler(wantState string, resultCh chan<- loopbackResult) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(loginCallbackPath, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			desc := q.Get("error_description")
			finishPage(w, false, desc)
			resultCh <- loopbackResult{err: fmt.Errorf("%s: %s", e, desc)}
			return
		}
		if q.Get("state") != wantState {
			finishPage(w, false, "state mismatch")
			resultCh <- loopbackResult{err: fmt.Errorf("state mismatch — possible CSRF")}
			return
		}
		code := q.Get("code")
		if code == "" {
			finishPage(w, false, "no authorization code")
			resultCh <- loopbackResult{err: fmt.Errorf("no authorization code in callback")}
			return
		}
		finishPage(w, true, "")
		resultCh <- loopbackResult{code: code}
	})
	return mux
}

// finishPage renders the terminal-return page shown in the browser.
func finishPage(w http.ResponseWriter, ok bool, detail string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	msg := "Login complete. You can close this tab and return to your terminal."
	if !ok {
		msg = "Login failed: " + detail + ". Return to your terminal."
	}
	fmt.Fprintf(w, "<!doctype html><html><body style=\"font-family:sans-serif;text-align:center;padding-top:3rem\"><h3>%s</h3></body></html>", msg)
}

// ── shared helpers ───────────────────────────────────────────────────────────

// writeContext persists a token/host into ~/.sem.yaml — same shape as `connect`
// — optionally activating it, and enforces 0600 permissions on the config file
// (it holds a secret). It returns the context name it wrote to.
func writeContext(host, token string, active bool) (string, error) {
	name := strings.ReplaceAll(host, ".", "_")
	if active {
		viper.Set("active-context", name)
	}
	viper.Set(fmt.Sprintf("contexts.%s.auth.token", name), token)
	viper.Set(fmt.Sprintf("contexts.%s.host", name), host)
	if err := viper.WriteConfig(); err != nil {
		return name, err
	}
	if path := viper.ConfigFileUsed(); path != "" {
		_ = os.Chmod(path, 0600)
	}
	return name, nil
}

// saveLoginContext writes the account token/host and reports the result. Used on
// the no-org path (and by tests); the --org path saves silently and lets
// createFirstOrg report the final, activated org context.
func saveLoginContext(host, token string) error {
	name, err := writeContext(host, token, true)
	if err != nil {
		output.Error("config_error", fmt.Sprintf("failed to write config: %s", err), 1)
		return err
	}
	output.Result(map[string]string{
		"status":  "signed_up",
		"host":    host,
		"context": name,
	})
	return nil
}

// orgCreateResp mirrors the account-level POST /api/v1alpha/organizations
// response (see public-api/v1alpha .../organizations/create.ex format_org). Note
// it carries NO host/subdomain — only the org's identity — which is why the
// org's context host must be supplied out of band via --org-host.
type orgCreateResp struct {
	OrganizationID string `json:"organization_id"`
	Name           string `json:"name"`
	Username       string `json:"username"`
}

// createFirstOrg creates an organization on the account-level endpoint served
// from the me.<domain> host (baseURL), authenticated with the freshly minted
// account token. On success it saves the org as a new context (host = orgHost,
// same token) and makes it active.
//
// The account context saved by signup is left untouched on failure — the account
// already exists, so we report the org-create error without rolling it back.
func createFirstOrg(httpc *http.Client, baseURL, token, name, orgHost string, w io.Writer) error {
	payload, err := json.Marshal(map[string]string{"username": name, "name": name})
	if err != nil {
		output.Error("signup_error", err.Error(), 1)
		return err
	}
	req, err := http.NewRequest("POST", baseURL+"/api/v1alpha/organizations", bytes.NewReader(payload))
	if err != nil {
		output.Error("signup_error", err.Error(), 1)
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Token "+token)
	req.Header.Set("User-Agent", client.UserAgent)

	resp, err := httpc.Do(req)
	if err != nil {
		err = fmt.Errorf("account created, but creating org %q failed: %w", name, err)
		output.Error("org_create_error", err.Error(), 1)
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		// Error bodies are a JSON-encoded string (Common.respond); unwrap it for
		// a clean message, falling back to the raw body.
		msg := strings.TrimSpace(string(body))
		var s string
		if json.Unmarshal(body, &s) == nil && s != "" {
			msg = s
		}
		err := fmt.Errorf("account created and active as %q, but creating org %q failed (HTTP %d): %s", signupOrgHostContextName(orgHost), name, resp.StatusCode, msg)
		output.Error("org_create_error", err.Error(), resp.StatusCode)
		return err
	}

	var org orgCreateResp
	if err := json.Unmarshal(body, &org); err != nil {
		err = fmt.Errorf("account created, but org create response was invalid: %w", err)
		output.Error("org_create_error", err.Error(), 1)
		return err
	}

	// Add the org as a new context (reusing the account token) and activate it.
	ctxName, err := writeContext(orgHost, token, true)
	if err != nil {
		output.Error("config_error", fmt.Sprintf("org created, but saving its context failed: %s", err), 1)
		return err
	}

	output.Result(map[string]string{
		"status":          "signed_up",
		"host":            orgHost,
		"context":         ctxName,
		"organization_id": org.OrganizationID,
		"organization":    org.Username,
	})
	return nil
}

// signupOrgHostContextName returns the context name a host maps to.
func signupOrgHostContextName(host string) string {
	return strings.ReplaceAll(host, ".", "_")
}

func randToken(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func s256Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func init() {
	signupCmd.Flags().BoolVar(&loginForceDevice, "headless", false, "force the device authorization flow (no browser)")
	signupCmd.Flags().BoolVar(&loginForceDevice, "device", false, "alias for --headless")
	signupCmd.Flags().StringVar(&loginIDHost, "id-host", "", "host serving the CLI-auth endpoints (defaults to <host>)")
	signupCmd.Flags().StringVar(&signupOrgName, "org", "", "also create a first organization with this name after signup")
	signupCmd.Flags().StringVar(&signupOrgHost, "org-host", "", "host for the --org organization's context (the create API does not return it)")
	rootCmd.AddCommand(signupCmd)
}
