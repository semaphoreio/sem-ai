package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// fakeOpener writes an executable script on PATH named after whichever
// opener binary openBrowser would invoke for the current GOOS (open,
// xdg-open, or rundll32), so we can control its exit behavior without
// actually launching a browser. Skips on platforms/openers this helper
// doesn't know how to fake.
func fakeOpener(t *testing.T, body string) {
	t.Helper()

	var name string
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "linux":
		name = "xdg-open"
	default:
		t.Skipf("fakeOpener does not support GOOS=%s", runtime.GOOS)
	}

	dir := t.TempDir()
	script := filepath.Join(dir, name)
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body+"\n"), 0755); err != nil {
		t.Fatalf("write fake opener: %v", err)
	}

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+origPath)
}

// TestOpenBrowser_OpenerExitsNonzero_ReturnsError is the HIGH-3 fix's core
// case: a fast, synchronous opener failure (no URL handler, no display,
// broken install) must surface as an error — cmd.Start() alone would report
// success here even though the browser never came up.
func TestOpenBrowser_OpenerExitsNonzero_ReturnsError(t *testing.T) {
	fakeOpener(t, "exit 1")

	if err := openBrowser("http://example.com"); err == nil {
		t.Fatal("openBrowser should error when the opener exits nonzero")
	}
}

// TestOpenBrowser_OpenerExitsZero_ReturnsNil is the positive-path
// counterpart: a normal, fast, successful opener exit must still report
// success and return promptly, not wait out the full exit-wait window. Proves
// the fix doesn't regress the working desktop path.
func TestOpenBrowser_OpenerExitsZero_ReturnsNil(t *testing.T) {
	fakeOpener(t, "exit 0")

	start := time.Now()
	if err := openBrowser("http://example.com"); err != nil {
		t.Fatalf("openBrowser: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 1*time.Second {
		t.Errorf("openBrowser took %v for an opener that exited immediately", elapsed)
	}
}

// TestOpenBrowser_OpenerStillRunning_ReturnsNilAfterGracePeriod covers the
// common real-world case where the opener hands off to a browser and keeps
// running (or the browser itself is the same process tree) well past a
// normal exit: openBrowser must not block on it — it returns nil once
// openBrowserExitWait elapses, treating "still running" as "launched fine".
func TestOpenBrowser_OpenerStillRunning_ReturnsNilAfterGracePeriod(t *testing.T) {
	fakeOpener(t, "sleep 5")

	origWait := openBrowserExitWait
	openBrowserExitWait = 50 * time.Millisecond
	t.Cleanup(func() { openBrowserExitWait = origWait })

	start := time.Now()
	if err := openBrowser("http://example.com"); err != nil {
		t.Fatalf("openBrowser: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("openBrowser took %v, want it to respect the shortened openBrowserExitWait", elapsed)
	}
}

// TestOpenBrowser_UnsupportedPlatform documents the existing default branch:
// no known opener means an explicit error, not a silent no-op.
func TestOpenBrowser_UnsupportedPlatform(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" || runtime.GOOS == "windows" {
		t.Skip("test target platform has a real opener case; nothing to assert here")
	}
	if err := openBrowser("http://example.com"); err == nil {
		t.Errorf("openBrowser should error on unsupported platform %s", runtime.GOOS)
	}
}
