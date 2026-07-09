package cmd

import (
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
	"runtime"
	"strings"
	"time"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// `login` obtains a Semaphore API token through guard's CLI-auth endpoints and
// stores it in the same config slot as `connect`. It supports the two flows the
// guard side exposes (see guard/lib/guard/cli_auth.ex):
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

const (
	loginCallbackPath = "/callback"
	loopbackTimeout   = 3 * time.Minute

	deviceGrantType   = "urn:ietf:params:oauth:grant-type:device_code"
	authCodeGrantType = "authorization_code"

	minPollInterval    = 1 * time.Second
	slowDownIncrement  = 5 * time.Second
	cliAuthHTTPTimeout = 30 * time.Second
	deviceDefaultTTL   = 900 // seconds; fallback if the server omits expires_in
)

var (
	loginForceDevice bool
	loginIDHost      string
)

// errBrowserUnavailable signals that the browser could not be opened, so the
// caller should fall back to the device flow.
var errBrowserUnavailable = errors.New("no browser available")

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
	return false
}

var loginCmd = &cobra.Command{
	Use:   "login <host>",
	Short: "Log in to Semaphore and save an API token",
	Long: `Log in to Semaphore and store an API token for <host>.

Prefers a browser-based loopback + PKCE flow. When no browser is available
(SSH session, no display, or --headless), it falls back to the device
authorization grant: it prints a code and URL for you to open elsewhere and
polls until you approve.`,
	Args: cobra.ExactArgs(1),
	Example: `  sem-ai login myorg.semaphoreci.com
  sem-ai login myorg.semaphoreci.com --headless
  sem-ai login myorg.semaphoreci.com --id-host id.semaphoreci.com`,
	RunE: func(cmd *cobra.Command, args []string) error {
		host := args[0]

		// The CLI-auth endpoints (/cli/device, /cli/token, /cli/signup) live on
		// guard. Default to <host>; --id-host overrides for deployments where
		// guard is served from a separate host.
		authHost := loginIDHost
		if authHost == "" {
			authHost = host
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
			if errors.Is(err, errBrowserUnavailable) {
				fmt.Fprintln(w, "Could not open a browser; falling back to device authorization.")
				tok, err = runDeviceFlow(c, w, time.Sleep)
			}
		}
		if err != nil {
			output.Error("login_error", err.Error(), 1)
			return err
		}

		saveHost := host
		if tok.Host != "" {
			saveHost = tok.Host
		}
		return saveLoginContext(saveHost, tok.Token)
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
		return nil, fmt.Errorf("timed out waiting for browser sign-in")
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

// saveLoginContext writes the token/host into ~/.sem.yaml — same shape as
// `connect` — and enforces 0600 permissions on the config file.
func saveLoginContext(host, token string) error {
	name := strings.ReplaceAll(host, ".", "_")
	viper.Set("active-context", name)
	viper.Set(fmt.Sprintf("contexts.%s.auth.token", name), token)
	viper.Set(fmt.Sprintf("contexts.%s.host", name), host)
	if err := viper.WriteConfig(); err != nil {
		output.Error("config_error", fmt.Sprintf("failed to write config: %s", err), 1)
		return err
	}
	// The config holds a secret; keep it owner-only regardless of viper's default.
	if path := viper.ConfigFileUsed(); path != "" {
		_ = os.Chmod(path, 0600)
	}

	output.Result(map[string]string{
		"status":  "logged_in",
		"host":    host,
		"context": name,
	})
	return nil
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
	loginCmd.Flags().BoolVar(&loginForceDevice, "headless", false, "force the device authorization flow (no browser)")
	loginCmd.Flags().BoolVar(&loginForceDevice, "device", false, "alias for --headless")
	loginCmd.Flags().StringVar(&loginIDHost, "id-host", "", "host serving the CLI-auth endpoints (defaults to <host>)")
	rootCmd.AddCommand(loginCmd)
}
