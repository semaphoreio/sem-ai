package versioncheck

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// isolateCacheHome points the cache file at a unique tempdir per test.
func isolateCacheHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	return dir
}

func TestCachePath_XDG(t *testing.T) {
	dir := isolateCacheHome(t)
	want := filepath.Join(dir, "sem-ai", "version-check.json")
	if got := CachePath(); got != want {
		t.Errorf("CachePath() = %q, want %q", got, want)
	}
}

func TestCachePath_FallbackToHome(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	want := filepath.Join(home, ".cache", "sem-ai", "version-check.json")
	if got := CachePath(); got != want {
		t.Errorf("CachePath() = %q, want %q", got, want)
	}
}

func TestReadWriteRoundTrip(t *testing.T) {
	isolateCacheHome(t)

	now := time.Now().UTC().Truncate(time.Second)
	state := CacheState{
		LastCheckedAt:             now,
		LatestVersion:             "0.4.1",
		LatestPublishedAt:         now.Add(-time.Hour),
		CurrentVersionWhenChecked: "0.3.0",
		NotifiedForVersion:        "0.4.1",
	}

	if err := WriteCache(state); err != nil {
		t.Fatalf("WriteCache: %v", err)
	}

	got, ok, err := ReadCache()
	if err != nil {
		t.Fatalf("ReadCache: %v", err)
	}
	if !ok {
		t.Fatal("ReadCache: ok=false on freshly-written file")
	}
	if got.LatestVersion != state.LatestVersion {
		t.Errorf("LatestVersion = %q, want %q", got.LatestVersion, state.LatestVersion)
	}
	if !got.LastCheckedAt.Equal(state.LastCheckedAt) {
		t.Errorf("LastCheckedAt = %v, want %v", got.LastCheckedAt, state.LastCheckedAt)
	}
	if got.Schema != CacheSchemaVersion {
		t.Errorf("Schema = %d, want %d", got.Schema, CacheSchemaVersion)
	}
}

func TestReadCache_Missing(t *testing.T) {
	isolateCacheHome(t)

	_, ok, err := ReadCache()
	if err != nil {
		t.Fatalf("unexpected error on missing file: %v", err)
	}
	if ok {
		t.Error("ok=true on missing file")
	}
}

func TestReadCache_Corrupted(t *testing.T) {
	dir := isolateCacheHome(t)
	path := filepath.Join(dir, "sem-ai", "version-check.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, ok, err := ReadCache()
	if err != nil {
		t.Fatalf("corrupted file should not error; got %v", err)
	}
	if ok {
		t.Error("ok=true on corrupted file")
	}
}

func TestReadCache_SchemaMismatch(t *testing.T) {
	dir := isolateCacheHome(t)
	path := filepath.Join(dir, "sem-ai", "version-check.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"schema":99,"latest_version":"0.4.1"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, ok, err := ReadCache()
	if err != nil {
		t.Fatalf("schema mismatch should not error; got %v", err)
	}
	if ok {
		t.Error("ok=true on schema=99 (current=1)")
	}
}

func TestFresh_Boundaries(t *testing.T) {
	now := time.Now()

	cases := []struct {
		name           string
		state          CacheState
		now            time.Time
		want           bool
	}{
		{
			name:  "empty version → not fresh",
			state: CacheState{LastCheckedAt: now, LatestVersion: ""},
			now:   now,
			want:  false,
		},
		{
			name:  "just-written → fresh",
			state: CacheState{LastCheckedAt: now, LatestVersion: "0.4.1"},
			now:   now,
			want:  true,
		},
		{
			name:  "TTL-1ns → fresh",
			state: CacheState{LastCheckedAt: now, LatestVersion: "0.4.1"},
			now:   now.Add(DefaultTTL - time.Nanosecond),
			want:  true,
		},
		{
			name:  "exactly TTL → NOT fresh (strict <)",
			state: CacheState{LastCheckedAt: now, LatestVersion: "0.4.1"},
			now:   now.Add(DefaultTTL),
			want:  false,
		},
		{
			name:  "way past TTL → not fresh",
			state: CacheState{LastCheckedAt: now, LatestVersion: "0.4.1"},
			now:   now.Add(24 * time.Hour),
			want:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Fresh(tc.state, tc.now); got != tc.want {
				t.Errorf("Fresh() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWriteCache_Concurrent(t *testing.T) {
	isolateCacheHome(t)

	const N = 50
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(n int) {
			defer wg.Done()
			_ = WriteCache(CacheState{
				LastCheckedAt: time.Now().UTC(),
				LatestVersion: "0.4.1",
			})
		}(i)
	}
	wg.Wait()

	// File must be readable and valid (last-writer-wins is fine).
	_, ok, err := ReadCache()
	if err != nil {
		t.Fatalf("ReadCache after concurrent writes: %v", err)
	}
	if !ok {
		t.Fatal("ok=false after concurrent writes — file may be corrupt")
	}
}

func TestWriteCache_StampsSchema(t *testing.T) {
	isolateCacheHome(t)

	// Caller passes Schema=0; WriteCache must stamp the current version.
	state := CacheState{
		LastCheckedAt: time.Now().UTC(),
		LatestVersion: "0.4.1",
	}
	if err := WriteCache(state); err != nil {
		t.Fatal(err)
	}

	got, ok, err := ReadCache()
	if err != nil || !ok {
		t.Fatalf("ReadCache: ok=%v err=%v", ok, err)
	}
	if got.Schema != CacheSchemaVersion {
		t.Errorf("Schema = %d, want %d", got.Schema, CacheSchemaVersion)
	}
}
