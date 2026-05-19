package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/semaphoreio/sem-ai/pkg/versioncheck"
	"github.com/spf13/cobra"
)

// withGitHubMock spins an httptest.Server, swaps versioncheck.Endpoint to it,
// and isolates XDG_CACHE_HOME to a tempdir. Returns the server + a counter
// of requests received.
func withGitHubMock(t *testing.T, latest string, status int) (*httptest.Server, *int64) {
	t.Helper()

	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		if status != 0 && status != 200 {
			w.WriteHeader(status)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name":     "v" + latest,
			"published_at": "2026-05-18T09:30:00Z",
		})
	}))
	t.Cleanup(srv.Close)

	old := versioncheck.Endpoint
	versioncheck.Endpoint = srv.URL
	t.Cleanup(func() { versioncheck.Endpoint = old })

	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	return srv, &calls
}

func TestRunCheckVerbose_HappyPath_Newer(t *testing.T) {
	_, calls := withGitHubMock(t, "0.4.1", 200)

	oldV := Version
	Version = "0.3.0"
	t.Cleanup(func() { Version = oldV })

	if err := runCheckVerbose(context.Background()); err != nil {
		t.Fatalf("runCheckVerbose: %v", err)
	}
	if got := atomic.LoadInt64(calls); got != 1 {
		t.Errorf("HTTP calls = %d, want 1", got)
	}

	state, ok, err := versioncheck.ReadCache()
	if err != nil || !ok {
		t.Fatalf("ReadCache: ok=%v err=%v", ok, err)
	}
	if state.LatestVersion != "0.4.1" {
		t.Errorf("cache LatestVersion = %q, want 0.4.1", state.LatestVersion)
	}
	if state.CurrentVersionWhenChecked != "0.3.0" {
		t.Errorf("cache CurrentVersionWhenChecked = %q, want 0.3.0", state.CurrentVersionWhenChecked)
	}
}

func TestRunCheckVerbose_HTTPFailure_NoCacheUpdate(t *testing.T) {
	_, _ = withGitHubMock(t, "", 503)

	oldV := Version
	Version = "0.3.0"
	t.Cleanup(func() { Version = oldV })

	if err := runCheckVerbose(context.Background()); err != nil {
		t.Fatalf("runCheckVerbose should not return error on HTTP failure; got %v", err)
	}

	// Cache should NOT have been written.
	_, ok, _ := versioncheck.ReadCache()
	if ok {
		t.Error("cache was written despite HTTP failure")
	}
}

func TestRunCheckVerbose_EnvOptOut_NoHTTPCall(t *testing.T) {
	_, calls := withGitHubMock(t, "0.4.1", 200)
	t.Setenv("SEM_AI_NO_UPDATE_CHECK", "1")

	oldV := Version
	Version = "0.3.0"
	t.Cleanup(func() { Version = oldV })

	if err := runCheckVerbose(context.Background()); err != nil {
		t.Fatalf("runCheckVerbose: %v", err)
	}
	if got := atomic.LoadInt64(calls); got != 0 {
		t.Errorf("HTTP calls = %d, want 0 (env opt-out)", got)
	}

	_, ok, _ := versioncheck.ReadCache()
	if ok {
		t.Error("cache was written despite env opt-out")
	}
}

func TestRunNotifyOnlyIfNewer_ColdCacheNewer_PrintsOnce(t *testing.T) {
	_, calls := withGitHubMock(t, "0.4.1", 200)

	oldV := Version
	Version = "0.3.0"
	t.Cleanup(func() { Version = oldV })

	buf := new(bytes.Buffer)
	if err := runNotifyOnlyIfNewer(context.Background(), buf); err != nil {
		t.Fatalf("runNotifyOnlyIfNewer: %v", err)
	}
	if got := atomic.LoadInt64(calls); got != 1 {
		t.Errorf("HTTP calls = %d, want 1 (cold cache)", got)
	}
	out := buf.String()
	if !strings.Contains(out, "0.4.1") || !strings.Contains(out, "0.3.0") {
		t.Errorf("notice missing versions; got:\n%s", out)
	}
	if !strings.Contains(out, "install.sh") {
		t.Errorf("notice missing install.sh hint; got:\n%s", out)
	}

	// Second call: still newer, but already notified → silent.
	buf.Reset()
	if err := runNotifyOnlyIfNewer(context.Background(), buf); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("second call should be silent (notified); got:\n%s", buf.String())
	}
}

func TestRunNotifyOnlyIfNewer_WarmCache_NoHTTPCall(t *testing.T) {
	_, calls := withGitHubMock(t, "0.4.1", 200)

	oldV := Version
	Version = "0.3.0"
	t.Cleanup(func() { Version = oldV })

	// Pre-seed a fresh cache: newer is known, not yet notified.
	if err := versioncheck.WriteCache(versioncheck.CacheState{
		LastCheckedAt: time.Now().UTC(),
		LatestVersion: "0.4.1",
	}); err != nil {
		t.Fatal(err)
	}

	buf := new(bytes.Buffer)
	if err := runNotifyOnlyIfNewer(context.Background(), buf); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt64(calls); got != 0 {
		t.Errorf("HTTP calls = %d, want 0 (warm cache)", got)
	}
	if !strings.Contains(buf.String(), "0.4.1") {
		t.Errorf("expected notice for warm-cache newer; got:\n%s", buf.String())
	}
}

func TestRunNotifyOnlyIfNewer_NotNewer_Silent(t *testing.T) {
	_, _ = withGitHubMock(t, "0.3.0", 200)

	oldV := Version
	Version = "0.3.0"
	t.Cleanup(func() { Version = oldV })

	buf := new(bytes.Buffer)
	if err := runNotifyOnlyIfNewer(context.Background(), buf); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected silence when current == latest; got:\n%s", buf.String())
	}
}

func TestRunNotifyOnlyIfNewer_EnvOptOut_Silent_NoHTTP(t *testing.T) {
	_, calls := withGitHubMock(t, "0.4.1", 200)
	t.Setenv("SEM_AI_NO_UPDATE_CHECK", "1")

	oldV := Version
	Version = "0.3.0"
	t.Cleanup(func() { Version = oldV })

	buf := new(bytes.Buffer)
	if err := runNotifyOnlyIfNewer(context.Background(), buf); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected silence with env opt-out; got:\n%s", buf.String())
	}
	if got := atomic.LoadInt64(calls); got != 0 {
		t.Errorf("HTTP calls = %d, want 0", got)
	}

	_, ok, _ := versioncheck.ReadCache()
	if ok {
		t.Error("cache should not be touched with env opt-out")
	}
}

func TestRunNotifyOnlyIfNewer_HTTPFailure_NoLastCheckedBump(t *testing.T) {
	_, _ = withGitHubMock(t, "", 503)

	oldV := Version
	Version = "0.3.0"
	t.Cleanup(func() { Version = oldV })

	buf := new(bytes.Buffer)
	if err := runNotifyOnlyIfNewer(context.Background(), buf); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected silence on HTTP failure; got:\n%s", buf.String())
	}

	_, ok, _ := versioncheck.ReadCache()
	if ok {
		t.Error("cache should not have LastCheckedAt bumped on HTTP failure")
	}
}

func TestRunNotifyOnlyIfNewer_DevBuild_NeverNags(t *testing.T) {
	_, _ = withGitHubMock(t, "0.4.1", 200)

	oldV := Version
	Version = "dev"
	t.Cleanup(func() { Version = oldV })

	buf := new(bytes.Buffer)
	if err := runNotifyOnlyIfNewer(context.Background(), buf); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("dev build should never get a nag; got:\n%s", buf.String())
	}
}

// makeCmd returns a cobra.Command with the given name — enough for the
// gating logic that only inspects cmd.Name().
func makeCmd(name string) *cobra.Command {
	return &cobra.Command{Use: name}
}

// forceTTYStderr swaps stderrIsTTY for the duration of the test.
func forceTTYStderr(t *testing.T, isTTY bool) {
	t.Helper()
	old := stderrIsTTY
	stderrIsTTY = func() bool { return isTTY }
	t.Cleanup(func() { stderrIsTTY = old })
}

func TestMaybeNotifyOnCommand_SkipsVersionSubcommand(t *testing.T) {
	_, _ = withGitHubMock(t, "0.4.1", 200)
	forceTTYStderr(t, true)

	// Preseed a fresh cache with a newer version + not-yet-notified.
	if err := versioncheck.WriteCache(versioncheck.CacheState{
		LastCheckedAt: time.Now().UTC(),
		LatestVersion: "0.4.1",
	}); err != nil {
		t.Fatal(err)
	}
	oldV := Version
	Version = "0.3.0"
	t.Cleanup(func() { Version = oldV })

	buf := new(bytes.Buffer)
	maybeNotifyOnCommand(makeCmd("version"), buf)
	if buf.Len() != 0 {
		t.Errorf("expected silence on `version` subcommand; got:\n%s", buf.String())
	}
}

func TestMaybeNotifyOnCommand_SkipsHelpSubcommand(t *testing.T) {
	_, _ = withGitHubMock(t, "0.4.1", 200)
	forceTTYStderr(t, true)

	if err := versioncheck.WriteCache(versioncheck.CacheState{
		LastCheckedAt: time.Now().UTC(),
		LatestVersion: "0.4.1",
	}); err != nil {
		t.Fatal(err)
	}
	oldV := Version
	Version = "0.3.0"
	t.Cleanup(func() { Version = oldV })

	buf := new(bytes.Buffer)
	maybeNotifyOnCommand(makeCmd("help"), buf)
	if buf.Len() != 0 {
		t.Errorf("expected silence on `help` subcommand; got:\n%s", buf.String())
	}
}

func TestMaybeNotifyOnCommand_SkipsCompletionSubcommand(t *testing.T) {
	_, _ = withGitHubMock(t, "0.4.1", 200)
	forceTTYStderr(t, true)

	if err := versioncheck.WriteCache(versioncheck.CacheState{
		LastCheckedAt: time.Now().UTC(),
		LatestVersion: "0.4.1",
	}); err != nil {
		t.Fatal(err)
	}
	oldV := Version
	Version = "0.3.0"
	t.Cleanup(func() { Version = oldV })

	buf := new(bytes.Buffer)
	maybeNotifyOnCommand(makeCmd("__complete"), buf)
	if buf.Len() != 0 {
		t.Errorf("expected silence on completion subcommand; got:\n%s", buf.String())
	}
}

func TestMaybeNotifyOnCommand_SkipsOnEnvOptOut(t *testing.T) {
	_, _ = withGitHubMock(t, "0.4.1", 200)
	forceTTYStderr(t, true)
	t.Setenv("SEM_AI_NO_UPDATE_CHECK", "1")

	if err := versioncheck.WriteCache(versioncheck.CacheState{
		LastCheckedAt: time.Now().UTC(),
		LatestVersion: "0.4.1",
	}); err != nil {
		t.Fatal(err)
	}
	oldV := Version
	Version = "0.3.0"
	t.Cleanup(func() { Version = oldV })

	buf := new(bytes.Buffer)
	maybeNotifyOnCommand(makeCmd("status"), buf)
	if buf.Len() != 0 {
		t.Errorf("expected silence on env opt-out; got:\n%s", buf.String())
	}
}

func TestMaybeNotifyOnCommand_SkipsOnCI(t *testing.T) {
	_, _ = withGitHubMock(t, "0.4.1", 200)
	forceTTYStderr(t, true)
	t.Setenv("CI", "true")

	if err := versioncheck.WriteCache(versioncheck.CacheState{
		LastCheckedAt: time.Now().UTC(),
		LatestVersion: "0.4.1",
	}); err != nil {
		t.Fatal(err)
	}
	oldV := Version
	Version = "0.3.0"
	t.Cleanup(func() { Version = oldV })

	buf := new(bytes.Buffer)
	maybeNotifyOnCommand(makeCmd("status"), buf)
	if buf.Len() != 0 {
		t.Errorf("expected silence in CI; got:\n%s", buf.String())
	}
}

func TestMaybeNotifyOnCommand_SkipsOnNonTTY(t *testing.T) {
	_, _ = withGitHubMock(t, "0.4.1", 200)
	forceTTYStderr(t, false)
	// Make sure CI env doesn't accidentally also-skip — isolate the TTY lever.
	t.Setenv("CI", "")

	if err := versioncheck.WriteCache(versioncheck.CacheState{
		LastCheckedAt: time.Now().UTC(),
		LatestVersion: "0.4.1",
	}); err != nil {
		t.Fatal(err)
	}
	oldV := Version
	Version = "0.3.0"
	t.Cleanup(func() { Version = oldV })

	buf := new(bytes.Buffer)
	maybeNotifyOnCommand(makeCmd("status"), buf)
	if buf.Len() != 0 {
		t.Errorf("expected silence on non-TTY stderr; got:\n%s", buf.String())
	}
}

func TestMaybeNotifyOnCommand_FiresWhenEligible(t *testing.T) {
	_, calls := withGitHubMock(t, "0.4.1", 200)
	forceTTYStderr(t, true)
	t.Setenv("CI", "")

	if err := versioncheck.WriteCache(versioncheck.CacheState{
		LastCheckedAt: time.Now().UTC(),
		LatestVersion: "0.4.1",
	}); err != nil {
		t.Fatal(err)
	}
	oldV := Version
	Version = "0.3.0"
	t.Cleanup(func() { Version = oldV })

	buf := new(bytes.Buffer)
	maybeNotifyOnCommand(makeCmd("status"), buf)
	if !strings.Contains(buf.String(), "0.4.1") || !strings.Contains(buf.String(), "0.3.0") {
		t.Errorf("expected notice; got:\n%s", buf.String())
	}
	if got := atomic.LoadInt64(calls); got != 0 {
		t.Errorf("HTTP calls = %d, want 0 (fresh cache)", got)
	}

	// Second call: notified_for_version bumped → silent.
	buf.Reset()
	maybeNotifyOnCommand(makeCmd("status"), buf)
	if buf.Len() != 0 {
		t.Errorf("second call should be silent; got:\n%s", buf.String())
	}
}

func TestMaybeNotifyOnCommand_StaleCache_SyncRefresh_ThenNotify(t *testing.T) {
	_, calls := withGitHubMock(t, "0.4.1", 200)
	forceTTYStderr(t, true)
	t.Setenv("CI", "")

	// Preseed STALE cache (12h old) with an older version + already-notified
	// for that older version. Refresh should overwrite latest → 0.4.1 →
	// notice fires (we have not yet been notified for 0.4.1).
	if err := versioncheck.WriteCache(versioncheck.CacheState{
		LastCheckedAt:      time.Now().UTC().Add(-12 * time.Hour),
		LatestVersion:      "0.3.5",
		NotifiedForVersion: "0.3.5",
	}); err != nil {
		t.Fatal(err)
	}
	oldV := Version
	Version = "0.3.0"
	t.Cleanup(func() { Version = oldV })

	buf := new(bytes.Buffer)
	maybeNotifyOnCommand(makeCmd("status"), buf)

	if got := atomic.LoadInt64(calls); got != 1 {
		t.Errorf("HTTP calls = %d, want 1 (sync refresh on stale)", got)
	}
	if !strings.Contains(buf.String(), "0.4.1") {
		t.Errorf("expected notice for refreshed 0.4.1; got:\n%s", buf.String())
	}

	state, ok, _ := versioncheck.ReadCache()
	if !ok {
		t.Fatal("cache disappeared after refresh")
	}
	if state.LatestVersion != "0.4.1" {
		t.Errorf("cache LatestVersion after refresh = %q, want 0.4.1", state.LatestVersion)
	}
	if state.NotifiedForVersion != "0.4.1" {
		t.Errorf("NotifiedForVersion = %q, want 0.4.1", state.NotifiedForVersion)
	}
}

func TestMaybeNotifyOnCommand_ColdCache_SyncRefresh_ThenNotify(t *testing.T) {
	_, calls := withGitHubMock(t, "0.4.1", 200)
	forceTTYStderr(t, true)
	t.Setenv("CI", "")
	// No preseeded cache.

	oldV := Version
	Version = "0.3.0"
	t.Cleanup(func() { Version = oldV })

	buf := new(bytes.Buffer)
	maybeNotifyOnCommand(makeCmd("status"), buf)

	if got := atomic.LoadInt64(calls); got != 1 {
		t.Errorf("HTTP calls = %d, want 1 (sync refresh on cold)", got)
	}
	if !strings.Contains(buf.String(), "0.4.1") {
		t.Errorf("expected first-run notice; got:\n%s", buf.String())
	}

	state, ok, _ := versioncheck.ReadCache()
	if !ok {
		t.Fatal("cache should have been written on cold-refresh")
	}
	if state.LatestVersion != "0.4.1" {
		t.Errorf("LatestVersion = %q, want 0.4.1", state.LatestVersion)
	}
}

func TestMaybeNotifyOnCommand_NetworkFailure_Silent_NoCacheBump(t *testing.T) {
	_, _ = withGitHubMock(t, "", 503)
	forceTTYStderr(t, true)
	t.Setenv("CI", "")

	// Cold cache.
	oldV := Version
	Version = "0.3.0"
	t.Cleanup(func() { Version = oldV })

	buf := new(bytes.Buffer)
	maybeNotifyOnCommand(makeCmd("status"), buf)

	if buf.Len() != 0 {
		t.Errorf("expected silence on network failure with cold cache; got:\n%s", buf.String())
	}
	_, ok, _ := versioncheck.ReadCache()
	if ok {
		t.Error("cache should not be written on network failure")
	}
}

func TestRunNotifyOnlyIfNewer_StaleCacheRefreshes(t *testing.T) {
	_, calls := withGitHubMock(t, "0.4.1", 200)

	oldV := Version
	Version = "0.3.0"
	t.Cleanup(func() { Version = oldV })

	// Pre-seed a STALE cache (LastCheckedAt = 12h ago).
	if err := versioncheck.WriteCache(versioncheck.CacheState{
		LastCheckedAt: time.Now().UTC().Add(-12 * time.Hour),
		LatestVersion: "0.3.5", // older than what GitHub will return
	}); err != nil {
		t.Fatal(err)
	}

	buf := new(bytes.Buffer)
	if err := runNotifyOnlyIfNewer(context.Background(), buf); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt64(calls); got != 1 {
		t.Errorf("HTTP calls = %d, want 1 (stale cache → refresh)", got)
	}
	// Notice should use the refreshed 0.4.1 (from mock), not stale 0.3.5.
	if !strings.Contains(buf.String(), "0.4.1") {
		t.Errorf("stale cache should have been refreshed to 0.4.1; got:\n%s", buf.String())
	}
}
