package versioncheck

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CacheSchemaVersion bumps when the on-disk cache layout changes.
// Older binaries treat a newer schema as "missing" (refresh; rewrite).
const CacheSchemaVersion = 1

// DefaultTTL is the freshness window for a cache hit.
const DefaultTTL = 6 * time.Hour

// CacheState is the on-disk shape of ~/.cache/sem-ai/version-check.json.
// Old cache files may contain a `notified_for_version` field — it is silently
// ignored on read (encoding/json discards unknown fields) and not written on
// updates. The notice is now stderr-on-every-command by design.
type CacheState struct {
	Schema                    int       `json:"schema"`
	LastCheckedAt             time.Time `json:"last_checked_at"`
	LatestVersion             string    `json:"latest_version,omitempty"`
	LatestPublishedAt         time.Time `json:"latest_published_at,omitempty"`
	CurrentVersionWhenChecked string    `json:"current_version_when_checked,omitempty"`
}

// CachePath returns the location of the cache file. Honors XDG_CACHE_HOME;
// falls back to ~/.cache/sem-ai/version-check.json.
func CachePath() string {
	if p := os.Getenv("XDG_CACHE_HOME"); p != "" {
		return filepath.Join(p, "sem-ai", "version-check.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// Last-resort fallback; ReadCache treats unreadable paths as missing.
		return filepath.Join(os.TempDir(), "sem-ai-version-check.json")
	}
	return filepath.Join(home, ".cache", "sem-ai", "version-check.json")
}

// ReadCache returns the cached state. ok=false when the file is missing,
// malformed, or has an unsupported schema — none of which are errors.
// True I/O failures (permission denied, etc.) are returned as errors so the
// caller can log them under --verbose.
func ReadCache() (CacheState, bool, error) {
	data, err := os.ReadFile(CachePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return CacheState{}, false, nil
		}
		return CacheState{}, false, fmt.Errorf("read cache: %w", err)
	}

	var s CacheState
	if err := json.Unmarshal(data, &s); err != nil {
		return CacheState{}, false, nil // malformed → treat as missing
	}
	if s.Schema != CacheSchemaVersion {
		return CacheState{}, false, nil // schema mismatch → treat as missing
	}
	return s, true, nil
}

// WriteCache writes the state atomically. Creates parent dir 0700 if absent;
// file mode 0644.
func WriteCache(state CacheState) error {
	state.Schema = CacheSchemaVersion

	path := CachePath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	tmp, err := os.CreateTemp(dir, "version-check-*.json")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()

	var renamed bool
	defer func() {
		if !renamed {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmpName, path, err)
	}
	renamed = true
	return nil
}

// Fresh returns true when the cache contains a usable LatestVersion AND
// `now - state.LastCheckedAt < DefaultTTL`. Boundary is strictly less than.
func Fresh(state CacheState, now time.Time) bool {
	if state.LatestVersion == "" {
		return false
	}
	return now.Sub(state.LastCheckedAt) < DefaultTTL
}
