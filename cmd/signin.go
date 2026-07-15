package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// `signin` is the one way in: it signs the user in to Semaphore — routing a
// new user through the normal web signup in the browser — and stores the
// account API token in the same config slot as `connect`. `signup` and
// `login` are aliases for the same flow; `sem-ai connect` stays the
// manual-token path.
//
// The flow is a device authorization grant (RFC 8628), gh-auth-login style,
// against guard's CLI-auth endpoints (see guard/lib/guard/cli_auth.ex):
// POST /cli/device returns a human-typeable user_code + verification URL; the
// CLI prints them (opening the browser best-effort on interactive machines)
// and polls POST /cli/token until the human approves in the browser.
//
// Token semantics are consent-driven and live server-side:
//
//   - a fresh account gets its first API token minted — the success response
//     carries token_action "minted";
//   - an account that already holds THE api token is asked, on the
//     authenticated consent page in the browser, whether to reset it (the old
//     token stops working everywhere it is used) — token_action "rotated".
//     The CLI warns about this up front, but the authoritative consent is the
//     browser page.
//
// The success response is {"token": ..., "host": null, "token_action": ...}:
// guard returns a null host (an account has no org host), so the saved host
// comes from the positional [host] argument, exactly like `connect`.
//
// With --org, after the token is saved signin also creates a first
// organization via POST <host>/api/v1alpha/organizations (an account-level
// endpoint served from the me.<domain> host, authenticated with the account
// token). Org creation is a new-account convenience: it runs only when the
// token was minted, and is skipped with a note when an existing account
// signed in. The create response returns only {organization_id, name,
// username} — NOT the org's host — so the org's context host must be supplied
// explicitly with --org-host, and that context is then made active.

const (
	// Semaphore Cloud defaults used when signin runs with no [host].
	// defaultSigninHost is the account-level host; defaultSigninIDHost serves
	// guard's CLI-auth endpoints. Both are overridable ([host] / --id-host).
	defaultSigninHost   = "me.semaphoreci.com"
	defaultSigninIDHost = "id.semaphoreci.com"

	deviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"

	minPollInterval    = 1 * time.Second
	slowDownIncrement  = 5 * time.Second
	cliAuthHTTPTimeout = 30 * time.Second
	deviceDefaultTTL   = 1800 // seconds; fallback if the server omits expires_in
)

var (
	signinForceDevice bool
	signinIDHost      string
	signinOrgName     string
	signinOrgHost     string
)

// isHeadless reports whether opening a browser is pointless (SSH session, no
// display, no interactive terminal, or forced). The device flow itself is the
// same either way — this only gates the best-effort browser open, since the
// code and URL are always printed. Declared as a var so tests can override it.
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
	// non-interactive CI) — nobody is there to see a browser window. Reuses
	// the same TTY probe as the update-notice logic in version.go.
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

// validateHost rejects anything that isn't a bare hostname. signin builds
// URLs by concatenating "https://" with the [host] argument and the
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
		return fmt.Errorf("%s %q is not a valid hostname: expected a bare host like me.semaphoreci.com (no scheme, userinfo, path, port, or query)", flagName, host)
	}
	return nil
}

// resolveSigninHosts derives the account host (where the token is stored) and
// the CLI-auth "id" host (which serves /cli/device and /cli/token) from the
// positional [host] argument and --id-host, applying the Semaphore Cloud
// defaults when neither is given.
//
// It rejects --id-host without an explicit [host]: the token is stored under
// [host], so defaulting [host] to me.semaphoreci.com while authenticating
// against a custom id host would mint a token on one deployment and save it
// under Semaphore Cloud's context, clobbering whatever token was there.
func resolveSigninHosts(args []string, idHost string) (host, authHost string, err error) {
	if idHost != "" && len(args) == 0 {
		return "", "", errors.New("--id-host requires [host]: the token is stored under [host], and defaulting it to me.semaphoreci.com is almost never right with a custom id host")
	}

	// Default to Semaphore Cloud; a positional host overrides for on-prem or
	// other deployments.
	host = defaultSigninHost
	if len(args) > 0 {
		host = args[0]
	}

	// The CLI-auth endpoints (/cli/device, /cli/token) live on guard. --id-host
	// overrides; otherwise for the default (Semaphore Cloud) use its guard host,
	// and for an explicit [host] fall back to that same host.
	authHost = idHost
	if authHost == "" {
		if len(args) == 0 {
			authHost = defaultSigninIDHost
		} else {
			authHost = host
		}
	}
	return host, authHost, nil
}

var signinCmd = &cobra.Command{
	Use:     "signin [host]",
	Aliases: []string{"signup", "login"},
	Short:   "Sign in to Semaphore (creating an account if needed) and save an API token",
	Long: `Sign in to Semaphore and store the account API token.

Shows a one-time code and a verification URL (opening your browser when one is
available). Enter the code in the browser, sign in (or create your account
right there if you don't have one), and approve. The terminal finishes
automatically. 'signup' and 'login' run this same flow.

Defaults to Semaphore Cloud (me.semaphoreci.com). Pass a different [host] (and
--id-host for the CLI-auth endpoints) for another deployment.

An account has a single API token. If yours already has one, approving the
sign-in RESETS it after an explicit confirmation in the browser. The previous
token immediately stops working everywhere it is used (CI secrets, scripts,
other machines). To authenticate with an existing token instead, use
'sem-ai connect <host> <token>'.

With --org, a first organization is created after sign-in, for new accounts
only (skipped with a note when an existing account signs in). The create API
does not return the new org's host, so pass it with --org-host; that context
is then made active.`,
	Args: cobra.MaximumNArgs(1),
	Example: `  sem-ai signin
  sem-ai signin --headless
  sem-ai signin me.semaphoreci.com --id-host id.semaphoreci.com
  sem-ai signup my-onprem.example.com --org myorg --org-host myorg.example.com`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Resolve where the token is stored (host) and which host serves the
		// CLI-auth endpoints (authHost), applying the Semaphore Cloud defaults.
		// Also rejects --id-host without an explicit [host], before any network
		// call.
		host, authHost, err := resolveSigninHosts(args, signinIDHost)
		if err != nil {
			output.Error("signin_error", err.Error(), 1)
			return err
		}

		// Reject anything but a bare hostname before touching the network — a
		// userinfo/scheme/path/port trick in [host], --id-host, or --org-host
		// could otherwise reroute where the token or auth requests go.
		if err := validateHost("host", host); err != nil {
			output.Error("signin_error", err.Error(), 1)
			return err
		}
		if signinIDHost != "" {
			if err := validateHost("--id-host", signinIDHost); err != nil {
				output.Error("signin_error", err.Error(), 1)
				return err
			}
		}
		if signinOrgHost != "" {
			if err := validateHost("--org-host", signinOrgHost); err != nil {
				output.Error("signin_error", err.Error(), 1)
				return err
			}
		}

		// Validate the org flag combo before touching the network, so a bad
		// invocation fails fast without running the whole auth flow.
		if signinOrgName != "" && signinOrgHost == "" {
			err := errors.New("--org requires --org-host: the create API does not return the new org's host, so it must be given explicitly")
			output.Error("signin_error", err.Error(), 1)
			return err
		}

		c := &cliAuthClient{
			baseURL: "https://" + authHost,
			http:    &http.Client{Timeout: cliAuthHTTPTimeout},
		}
		w := cmd.ErrOrStderr()

		// The authoritative reset consent lives on the authenticated browser
		// page; this is the up-front warning (the CLI cannot know the
		// account's token state before the user authenticates).
		fmt.Fprintln(w, "Heads up: if your account already has an API token, completing this sign-in")
		fmt.Fprintln(w, "lets you reset it (you confirm in the browser first). A reset token stops")
		fmt.Fprintln(w, "working everywhere it is used: CI secrets, scripts, other machines.")

		tok, err := runDeviceFlow(c, w, time.Sleep, !isHeadless(signinForceDevice))
		if err != nil {
			output.Error("signin_error", err.Error(), 1)
			return err
		}

		orgc := &http.Client{Timeout: cliAuthHTTPTimeout}
		return finishSignin(tok, host, signinOrgName, signinOrgHost, orgc, "https://"+host, w)
	},
}

// finishSignin saves the account context and handles the optional first-org
// creation. Extracted from RunE so the post-auth behavior is testable:
// org creation is a new-account convenience and runs only when the token was
// minted — an existing account ("rotated") signs in, gets its context saved,
// and --org is skipped with a note.
func finishSignin(tok *tokenResp, host, orgName, orgHost string, orgc *http.Client, orgBaseURL string, w io.Writer) error {
	saveHost := host
	if tok.Host != "" {
		saveHost = tok.Host
	}

	// No org requested: save the account context and report it.
	if orgName == "" {
		return saveSigninContext(saveHost, tok.Token, tok.TokenAction)
	}

	if tok.TokenAction == "rotated" {
		fmt.Fprintf(w, "Signed in to an existing account; skipping organization creation (--org %s). Create organizations from the web app.\n", orgName)
		return saveSigninContext(saveHost, tok.Token, tok.TokenAction)
	}

	// Optional first-org creation. Save the account context silently first,
	// then create the org and let createFirstOrg report the final, activated
	// org context. If org creation fails we surface a clear error but do NOT
	// roll the account context back — the account exists and the token works.
	if _, err := writeContext(saveHost, tok.Token, true); err != nil {
		output.Error("config_error", fmt.Sprintf("failed to write config: %s", err), 1)
		return err
	}
	return createFirstOrg(orgc, orgBaseURL, tok.Token, orgName, orgHost, w)
}

// ── HTTP client for guard's CLI-auth endpoints ──────────────────────────────

type cliAuthClient struct {
	baseURL string // e.g. https://id.semaphoreci.com (no trailing slash)
	http    *http.Client
}

type tokenResp struct {
	Token string `json:"token"`
	Host  string `json:"host"`
	// "minted" (fresh account, first token) or "rotated" (existing account,
	// token reset with browser consent). Empty on older servers, which only
	// ever minted.
	TokenAction string `json:"token_action"`
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
	req, err := http.NewRequest("POST", c.baseURL+"/cli/token", strings.NewReader(form.Encode()))
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

func runDeviceFlow(c *cliAuthClient, w io.Writer, sleep func(time.Duration), attemptBrowser bool) (*tokenResp, error) {
	da, err := c.requestDevice()
	if err != nil {
		return nil, err
	}

	fmt.Fprintf(w, "\nFirst, copy your one-time code:\n\n    %s\n\n", da.UserCode)

	browserURL := da.VerificationURIComplete
	if browserURL == "" {
		browserURL = da.VerificationURI
	}

	opened := attemptBrowser && openBrowser(browserURL) == nil
	if opened {
		fmt.Fprintf(w, "Opened %s in your browser.\n", da.VerificationURI)
		fmt.Fprintln(w, "If nothing appeared, open it yourself and enter the code.")
	} else {
		fmt.Fprintf(w, "Open %s in a browser and enter the code.\n", da.VerificationURI)
	}

	fmt.Fprintln(w, "Sign in there (or create your account), then approve. Waiting...")

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
			return nil, fmt.Errorf("the sign-in request expired before it was approved; run `sem-ai signin` again")
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
			return nil, fmt.Errorf("sign-in was denied in the browser; nothing was changed")
		case "expired_token":
			return nil, fmt.Errorf("the sign-in request expired before it was approved; run `sem-ai signin` again")
		case "token_exists":
			// Consent said "mint" but a token appeared on the account before
			// the poll redeemed the code; the server message says to re-run.
			return nil, apiErr
		default:
			return nil, apiErr
		}
	}
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

// saveSigninContext writes the account token/host and reports the result. Used
// on the no-org and org-skipped paths (and by tests); the --org create path
// saves silently and lets createFirstOrg report the final, activated context.
func saveSigninContext(host, token, tokenAction string) error {
	name, err := writeContext(host, token, true)
	if err != nil {
		output.Error("config_error", fmt.Sprintf("failed to write config: %s", err), 1)
		return err
	}

	res := map[string]string{
		"status":  "signed_in",
		"host":    host,
		"context": name,
	}
	if tokenAction != "" {
		res["token_action"] = tokenAction
	}

	output.Result(res)
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
// The account context saved by signin is left untouched on failure — the account
// already exists, so we report the org-create error without rolling it back.
func createFirstOrg(httpc *http.Client, baseURL, token, name, orgHost string, w io.Writer) error {
	payload, err := json.Marshal(map[string]string{"username": name, "name": name})
	if err != nil {
		output.Error("signin_error", err.Error(), 1)
		return err
	}
	req, err := http.NewRequest("POST", baseURL+"/api/v1alpha/organizations", bytes.NewReader(payload))
	if err != nil {
		output.Error("signin_error", err.Error(), 1)
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Token "+token)
	req.Header.Set("User-Agent", client.UserAgent)

	resp, err := httpc.Do(req)
	if err != nil {
		err = fmt.Errorf("signed in, but creating org %q failed: %w", name, err)
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
		err := fmt.Errorf("signed in and active as %q, but creating org %q failed (HTTP %d): %s", contextNameForHost(orgHost), name, resp.StatusCode, msg)
		output.Error("org_create_error", err.Error(), resp.StatusCode)
		return err
	}

	var org orgCreateResp
	if err := json.Unmarshal(body, &org); err != nil {
		err = fmt.Errorf("signed in, but the org create response was invalid: %w", err)
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
		"status":          "signed_in",
		"host":            orgHost,
		"context":         ctxName,
		"organization_id": org.OrganizationID,
		"organization":    org.Username,
	})
	return nil
}

// contextNameForHost returns the context name a host maps to.
func contextNameForHost(host string) string {
	return strings.ReplaceAll(host, ".", "_")
}

func init() {
	signinCmd.Flags().BoolVar(&signinForceDevice, "headless", false, "never try to open a browser; just print the code and URL")
	signinCmd.Flags().BoolVar(&signinForceDevice, "device", false, "alias for --headless")
	signinCmd.Flags().StringVar(&signinIDHost, "id-host", "", "host serving the CLI-auth endpoints (defaults to <host>)")
	signinCmd.Flags().StringVar(&signinOrgName, "org", "", "also create a first organization with this name (new accounts only)")
	signinCmd.Flags().StringVar(&signinOrgHost, "org-host", "", "host for the --org organization's context (the create API does not return it)")
	rootCmd.AddCommand(signinCmd)
}
