// Package versioncheck talks to GitHub to detect newer sem-ai releases.
// All functions are pure (caller-owned context, no internal goroutines)
// so they compose cleanly into either a foreground CLI call or a
// non-blocking host hook.
package versioncheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Endpoint is the GitHub releases API endpoint queried by Latest.
// Exported so tests can swap to an httptest.Server.URL.
var Endpoint = "https://api.github.com/repos/semaphoreio/sem-ai/releases/latest"

// UserAgent is the value sent in HTTP requests to GitHub. Default reflects
// an unreleased build. main.go updates this at startup alongside the
// pkg/client UserAgent var to match `sem-ai/<ver> (<os>; <arch>)`.
var UserAgent = "sem-ai/dev"

// DefaultTimeout is the per-call HTTP timeout for Latest when the caller's
// context has no tighter deadline.
const DefaultTimeout = 3 * time.Second

// ErrInvalidSemver is returned by Compare when either argument cannot be
// parsed as `v?N.N.N(-suffix)?`.
var ErrInvalidSemver = errors.New("invalid semver")

// Release is a minimal view of a GitHub release record. Version has the
// leading `v` stripped.
type Release struct {
	Version     string    `json:"version"`
	PublishedAt time.Time `json:"published_at"`
}

// Latest fetches the latest sem-ai release from GitHub. Honors ctx; applies
// DefaultTimeout if ctx has no tighter deadline.
func Latest(ctx context.Context) (Release, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultTimeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, Endpoint, nil)
	if err != nil {
		return Release{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("github API: HTTP %d", resp.StatusCode)
	}

	var raw struct {
		TagName     string    `json:"tag_name"`
		PublishedAt time.Time `json:"published_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return Release{}, fmt.Errorf("decode: %w", err)
	}

	if raw.TagName == "" {
		return Release{}, errors.New("release missing tag_name")
	}

	return Release{
		Version:     strings.TrimPrefix(raw.TagName, "v"),
		PublishedAt: raw.PublishedAt,
	}, nil
}

// Compare returns true iff latest > current. The literal "dev"
// (case-insensitive) is treated as the lowest possible version — dev builds
// never get nagged. Returns ErrInvalidSemver for unparseable input.
func Compare(current, latest string) (bool, error) {
	if strings.EqualFold(current, "dev") {
		return false, nil
	}
	if strings.EqualFold(latest, "dev") {
		return false, nil
	}

	cMain, cPre, err := parseSemver(current)
	if err != nil {
		return false, fmt.Errorf("current %q: %w", current, err)
	}
	lMain, lPre, err := parseSemver(latest)
	if err != nil {
		return false, fmt.Errorf("latest %q: %w", latest, err)
	}

	for i := 0; i < 3; i++ {
		if lMain[i] > cMain[i] {
			return true, nil
		}
		if lMain[i] < cMain[i] {
			return false, nil
		}
	}

	// Main segments equal — compare pre-release suffixes.
	// No-suffix beats has-suffix: 1.2.3 > 1.2.3-rc1.
	switch {
	case cPre == "" && lPre == "":
		return false, nil
	case cPre == "":
		return false, nil // current has no suffix, latest does → current wins
	case lPre == "":
		return true, nil // latest has no suffix, current does → latest wins
	default:
		return lPre > cPre, nil
	}
}

// parseSemver parses `v?N.N.N(-suffix)?` into [3]int + suffix.
func parseSemver(s string) ([3]int, string, error) {
	v := strings.TrimPrefix(s, "v")
	mainPart := v
	pre := ""
	if i := strings.Index(v, "-"); i >= 0 {
		mainPart = v[:i]
		pre = v[i+1:]
	}

	parts := strings.Split(mainPart, ".")
	if len(parts) != 3 {
		return [3]int{}, "", ErrInvalidSemver
	}

	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return [3]int{}, "", ErrInvalidSemver
		}
		out[i] = n
	}
	return out, pre, nil
}

// EnvOptOut returns true when SEM_AI_NO_UPDATE_CHECK is set to anything
// other than "" / "0" / "false" (case-insensitive).
func EnvOptOut() bool {
	v := os.Getenv("SEM_AI_NO_UPDATE_CHECK")
	if v == "" {
		return false
	}
	switch strings.ToLower(v) {
	case "0", "false":
		return false
	}
	return true
}
