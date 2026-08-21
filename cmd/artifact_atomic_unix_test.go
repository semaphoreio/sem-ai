//go:build unix

package cmd

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/semaphoreio/sem-ai/pkg/client"
)

// A rename-based write replaces the destination inode; an in-place
// os.WriteFile keeps it. That difference is what makes an interrupted pull
// unable to leave a truncated file behind, so it is asserted directly.
func TestPullReplacesTheDestinationInodeRatherThanTruncatingInPlace(t *testing.T) {
	store := fakeStore{
		scope:   "jobs",
		scopeID: "abababab-cdcd-efef-0101-232323232323",
		files:   map[string][]byte{"reports/junit.json": []byte(`{"testResults":[]}`)},
	}
	newArtifactAPI(t, store)
	dir := t.TempDir()
	setPullFlags(t, store, "", dir, false, false)

	walkRes, err := walkArtifacts(client.New(), store.scope, store.scopeID, "")
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if r := pullFiles(client.New(), walkRes.Files)[0]; r.Status != "downloaded" {
		t.Fatalf("first pull: %s %s", r.Status, r.Error)
	}
	dest := filepath.Join(dir, "reports", "junit.json")
	before := inodeOf(t, dest)

	artifactPullForce = true
	if r := pullFiles(client.New(), walkRes.Files)[0]; r.Status != "downloaded" {
		t.Fatalf("forced pull: %s %s", r.Status, r.Error)
	}
	after := inodeOf(t, dest)

	if before == after {
		t.Error("the destination was written in place; an interrupted pull would leave a truncated file that the next run skips")
	}
}

func inodeOf(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("no stat_t for %s", path)
	}
	return uint64(stat.Ino)
}
