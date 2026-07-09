package cmd

import (
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
