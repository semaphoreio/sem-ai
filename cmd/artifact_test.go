package cmd

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/spf13/pflag"
)

// A fake artifact store: paths mapping to file contents. Directories are
// derived from the path separators, mirroring how artifacthub reports them.
type fakeStore struct {
	scope   string
	scopeID string
	files   map[string][]byte
	// failList maps a directory path to the HTTP status its listing should
	// return, standing in for a subtree deleted by retention mid-walk.
	failList map[string]int
	// injectDirs appends extra directory entries to a path's listing, so a
	// store can be made to report a cycle the real one should never produce.
	injectDirs map[string][]string
}

type artifactAPI struct {
	srv *httptest.Server
	mu  sync.Mutex
	// listCalls records every path= value the client listed, in order, so
	// tests can prove the walk descended rather than guessed.
	listCalls []string
	// signedCalls counts signed_url requests.
	signedCalls int
	// onBlob, when set, runs for each blob fetch so a test can observe how
	// many downloads are in flight at once.
	onBlob func()
}

func newArtifactAPI(t *testing.T, store fakeStore) *artifactAPI {
	t.Helper()

	api := &artifactAPI{}
	mux := http.NewServeMux()

	mux.HandleFunc("/api/v1alpha/artifacts", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("scope") != store.scope || q.Get("scope_id") != store.scopeID {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		path := q.Get("path")

		api.mu.Lock()
		api.listCalls = append(api.listCalls, path)
		api.mu.Unlock()

		if status, fails := store.failList[path]; fails {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(fakeFailBody(status)))
			return
		}

		entries := listLevel(store.files, path)
		for _, extra := range store.injectDirs[path] {
			entries = append(entries, artifactEntry{
				Name: filepath.Base(extra), Path: extra, IsDirectory: true,
			})
		}
		if len(entries) == 0 && path != "" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("Artifact path not found"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"artifacts": entries})
	})

	mux.HandleFunc("/api/v1alpha/artifacts/signed_url", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		path := q.Get("path")

		api.mu.Lock()
		api.signedCalls++
		api.mu.Unlock()

		if _, ok := store.files[path]; !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("not found"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				{"path": path, "url": api.srv.URL + "/blob/" + url.QueryEscape(path)},
			},
		})
	})

	mux.HandleFunc("/blob/", func(w http.ResponseWriter, r *http.Request) {
		path, err := url.QueryUnescape(strings.TrimPrefix(r.URL.Path, "/blob/"))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		data, ok := store.files[path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if api.onBlob != nil {
			api.onBlob()
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	})

	api.srv = httptest.NewServer(mux)
	t.Cleanup(api.srv.Close)

	client.SetBaseURLForTest(api.srv.URL)
	t.Cleanup(func() { client.SetBaseURLForTest("") })

	t.Setenv("SEMAPHORE_API_TOKEN", "test-token")
	t.Setenv("SEMAPHORE_HOST", "example.test")
	config.Load()

	return api
}

// listLevel returns the immediate children of dir, as artifacthub would:
// files with a size, directories without, each de-duplicated.
func listLevel(files map[string][]byte, dir string) []artifactEntry {
	prefix := ""
	if dir != "" {
		prefix = strings.TrimSuffix(dir, "/") + "/"
	}

	seen := map[string]bool{}
	entries := []artifactEntry{}
	for path, data := range files {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		rest := strings.TrimPrefix(path, prefix)
		if rest == "" {
			continue
		}
		if i := strings.Index(rest, "/"); i >= 0 {
			name := rest[:i]
			full := prefix + name
			if seen[full] {
				continue
			}
			seen[full] = true
			entries = append(entries, artifactEntry{Name: name, Path: full, IsDirectory: true})
			continue
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		size := int64(len(data))
		entries = append(entries, artifactEntry{Name: rest, Path: path, Size: &size})
	}
	return entries
}

// artifactsGateBody is the exact text public-api/v1alpha returns when the
// artifacts API is gated off, so detection is pinned to the real contract
// rather than a paraphrase.
const artifactsGateBody = "The artifacts api feature is not enabled for your organization. Please contact support"

func fakeFailBody(status int) string {
	if status == http.StatusForbidden {
		return artifactsGateBody
	}
	return "listing unavailable"
}

func gzipped(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func nestedStore(t *testing.T) fakeStore {
	t.Helper()
	return fakeStore{
		scope:   "workflows",
		scopeID: "11111111-2222-3333-4444-555555555555",
		files: map[string][]byte{
			"summary.json":                              []byte(`{"top":true}`),
			"test-results/junit.json":                   []byte(`{"testResults":[]}`),
			"test-results/shard-1/junit.xml":            []byte(`<testsuite/>`),
			"test-results/shard-2/junit.xml":            []byte(`<testsuite/>`),
			"timing/e2e/selected_specs.txt":             []byte("spec_a\nspec_b\n"),
			"agent/job_logs.txt.gz":                     gzipped(t, []byte("log line\n")),
			"compilation/deep/deeper/deepest/blob.json": []byte(`{"deep":true}`),
		},
	}
}

// setPullFlags points the pull globals at one scope and restores them after,
// since cobra flags are package state.
func setPullFlags(t *testing.T, store fakeStore, path, outputDir string, force, dryRun bool) {
	t.Helper()
	prev := []any{artifactPullScope, artifactPullScopeID, artifactPullPath, artifactPullOutputDir, artifactPullForce, artifactPullDryRun}
	artifactPullScope = store.scope
	artifactPullScopeID = store.scopeID
	artifactPullPath = path
	artifactPullOutputDir = outputDir
	artifactPullForce = force
	artifactPullDryRun = dryRun
	t.Cleanup(func() {
		artifactPullScope = prev[0].(string)
		artifactPullScopeID = prev[1].(string)
		artifactPullPath = prev[2].(string)
		artifactPullOutputDir = prev[3].(string)
		artifactPullForce = prev[4].(bool)
		artifactPullDryRun = prev[5].(bool)
	})
}

func TestListArtifactPathOmitsEmptyPath(t *testing.T) {
	store := nestedStore(t)
	api := newArtifactAPI(t, store)

	entries, err := listArtifactPath(client.New(), store.scope, store.scopeID, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	var files, dirs int
	for _, e := range entries {
		if e.IsDirectory {
			dirs++
		} else {
			files++
		}
	}
	if files != 1 {
		t.Errorf("top level: got %d files, want 1 (summary.json)", files)
	}
	if dirs != 4 {
		t.Errorf("top level: got %d directories, want 4 (test-results, timing, agent, compilation)", dirs)
	}
	if len(api.listCalls) != 1 || api.listCalls[0] != "" {
		t.Errorf("expected one list call with no path, got %v", api.listCalls)
	}
}

func TestListArtifactPathSendsPath(t *testing.T) {
	store := nestedStore(t)
	newArtifactAPI(t, store)

	entries, err := listArtifactPath(client.New(), store.scope, store.scopeID, "test-results")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3 (junit.json + 2 shard dirs): %+v", len(entries), entries)
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Path, "test-results/") {
			t.Errorf("entry %q is not under the requested path", e.Path)
		}
	}
}

func TestListArtifactPathPropagatesNotFound(t *testing.T) {
	store := nestedStore(t)
	newArtifactAPI(t, store)

	_, err := listArtifactPath(client.New(), store.scope, store.scopeID, "nope")
	if err == nil {
		t.Fatal("expected an error for a path that does not exist")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("expected the HTTP status in the error, got %v", err)
	}
}

func TestWalkArtifactsFindsEveryFileSorted(t *testing.T) {
	store := nestedStore(t)
	api := newArtifactAPI(t, store)

	walkRes, err := walkArtifacts(client.New(), store.scope, store.scopeID, "")
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	files := walkRes.Files
	if walkRes.Truncated {
		t.Error("walk reported truncation on a small store")
	}
	if len(files) != len(store.files) {
		t.Fatalf("got %d files, want %d: %+v", len(files), len(store.files), files)
	}

	for _, f := range files {
		if f.IsDirectory {
			t.Errorf("walk returned a directory entry: %+v", f)
		}
		if _, ok := store.files[f.Path]; !ok {
			t.Errorf("walk returned an unknown path %q", f.Path)
		}
	}
	for i := 1; i < len(files); i++ {
		if files[i-1].Path > files[i].Path {
			t.Errorf("results are not sorted: %q before %q", files[i-1].Path, files[i].Path)
		}
	}
	// Four levels deep proves the queue keeps descending, not just one level.
	found := false
	for _, f := range files {
		if f.Path == "compilation/deep/deeper/deepest/blob.json" {
			found = true
		}
	}
	if !found {
		t.Error("walk did not reach the deepest file")
	}
	if len(api.listCalls) < 5 {
		t.Errorf("expected a list call per directory, got %v", api.listCalls)
	}
}

func TestWalkArtifactsFromSubdirectory(t *testing.T) {
	store := nestedStore(t)
	newArtifactAPI(t, store)

	walkRes, err := walkArtifacts(client.New(), store.scope, store.scopeID, "test-results")
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	files := walkRes.Files
	if len(files) != 3 {
		t.Fatalf("got %d files, want 3: %+v", len(files), files)
	}
	for _, f := range files {
		if !strings.HasPrefix(f.Path, "test-results/") {
			t.Errorf("file %q escaped the requested subtree", f.Path)
		}
	}
}

func TestWalkArtifactsTruncatesRunawayTree(t *testing.T) {
	// Every directory contains one more directory, so the walk would never
	// end without the request cap.
	files := map[string][]byte{}
	deep := ""
	for i := 0; i < defaultMaxWalkRequests+50; i++ {
		deep = filepath.Join(deep, fmt.Sprintf("d%d", i))
		files[filepath.Join(deep, "leaf.txt")] = []byte("x")
	}
	store := fakeStore{scope: "projects", scopeID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", files: files}
	api := newArtifactAPI(t, store)

	walkRes, err := walkArtifacts(client.New(), store.scope, store.scopeID, "")
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	found := walkRes.Files
	if !walkRes.Truncated {
		t.Error("expected truncated=true once the request cap is hit")
	}
	if len(api.listCalls) > defaultMaxWalkRequests {
		t.Errorf("issued %d list calls, cap is %d", len(api.listCalls), defaultMaxWalkRequests)
	}
	if len(found) == 0 {
		t.Error("a truncated walk should still return what it found")
	}
}

func TestSafeRelativePath(t *testing.T) {
	ok := []string{"a.json", "test-results/junit.xml", "a/b/c/d.txt"}
	for _, p := range ok {
		if _, err := safeRelativePath(p); err != nil {
			t.Errorf("%q: unexpected error %v", p, err)
		}
	}
	bad := []string{"", "/etc/passwd", "../escape", "a/../../escape", "a/./b", `a\b`, "a//b"}
	for _, p := range bad {
		if _, err := safeRelativePath(p); err == nil {
			t.Errorf("%q: expected rejection", p)
		}
	}
}

func TestPullWritesMirroredTree(t *testing.T) {
	store := nestedStore(t)
	newArtifactAPI(t, store)
	dir := t.TempDir()
	setPullFlags(t, store, "", dir, false, false)

	walkRes, err := walkArtifacts(client.New(), store.scope, store.scopeID, "")
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	files := walkRes.Files
	results := pullFiles(client.New(), files)

	if len(results) != len(store.files) {
		t.Fatalf("got %d results, want %d", len(results), len(store.files))
	}
	for _, r := range results {
		if r.Status != "downloaded" {
			t.Errorf("%s: status %q error %q", r.Path, r.Status, r.Error)
			continue
		}
		want := filepath.Join(dir, filepath.FromSlash(r.Path))
		if r.Output != want {
			t.Errorf("%s: wrote to %q, want %q", r.Path, r.Output, want)
		}
		if _, err := os.Stat(want); err != nil {
			t.Errorf("%s: file missing on disk: %v", r.Path, err)
		}
	}

	plain, err := os.ReadFile(filepath.Join(dir, "timing", "e2e", "selected_specs.txt"))
	if err != nil {
		t.Fatalf("read nested file: %v", err)
	}
	if string(plain) != "spec_a\nspec_b\n" {
		t.Errorf("nested file content: %q", plain)
	}
}

func TestPullKeepsGzBytesAndInflatesHiddenGzip(t *testing.T) {
	store := fakeStore{
		scope:   "jobs",
		scopeID: "99999999-8888-7777-6666-555555555555",
		files: map[string][]byte{
			"agent/job_logs.txt.gz": gzipped(t, []byte("log line\n")),
			"reports/junit.json":    gzipped(t, []byte(`{"testResults":[]}`)),
		},
	}
	newArtifactAPI(t, store)
	dir := t.TempDir()
	setPullFlags(t, store, "", dir, false, false)

	walkRes, err := walkArtifacts(client.New(), store.scope, store.scopeID, "")
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	files := walkRes.Files
	for _, r := range pullFiles(client.New(), files) {
		if r.Status != "downloaded" {
			t.Fatalf("%s: %s %s", r.Path, r.Status, r.Error)
		}
	}

	gz, err := os.ReadFile(filepath.Join(dir, "agent", "job_logs.txt.gz"))
	if err != nil {
		t.Fatalf("read .gz: %v", err)
	}
	if len(gz) < 2 || gz[0] != 0x1f || gz[1] != 0x8b {
		t.Error(".gz artifact should keep its gzip bytes")
	}

	plain, err := os.ReadFile(filepath.Join(dir, "reports", "junit.json"))
	if err != nil {
		t.Fatalf("read .json: %v", err)
	}
	if string(plain) != `{"testResults":[]}` {
		t.Errorf("a gzipped .json should be inflated on write, got %q", plain)
	}
}

func TestPullSkipsExistingUnlessForced(t *testing.T) {
	store := nestedStore(t)
	newArtifactAPI(t, store)
	dir := t.TempDir()
	setPullFlags(t, store, "test-results", dir, false, false)

	walkRes, err := walkArtifacts(client.New(), store.scope, store.scopeID, "test-results")
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	files := walkRes.Files

	first := pullFiles(client.New(), files)
	for _, r := range first {
		if r.Status != "downloaded" {
			t.Fatalf("first pass %s: %s %s", r.Path, r.Status, r.Error)
		}
	}

	second := pullFiles(client.New(), files)
	for _, r := range second {
		if r.Status != "skipped" {
			t.Errorf("second pass %s: got %q, want skipped", r.Path, r.Status)
		}
	}

	artifactPullForce = true
	third := pullFiles(client.New(), files)
	for _, r := range third {
		if r.Status != "downloaded" {
			t.Errorf("forced pass %s: got %q, want downloaded", r.Path, r.Status)
		}
	}
}

func TestPullReportsPerFileFailureWithoutAbortingTheRest(t *testing.T) {
	store := nestedStore(t)
	newArtifactAPI(t, store)
	dir := t.TempDir()
	setPullFlags(t, store, "", dir, false, false)

	files := []artifactEntry{
		{Path: "summary.json", Name: "summary.json"},
		{Path: "does/not/exist.json", Name: "exist.json"},
		{Path: "../escape.json", Name: "escape.json"},
	}
	results := pullFiles(client.New(), files)

	if results[0].Status != "downloaded" {
		t.Errorf("real file: got %q (%s)", results[0].Status, results[0].Error)
	}
	if results[1].Status != "failed" {
		t.Errorf("missing file: got %q, want failed", results[1].Status)
	}
	if results[2].Status != "failed" {
		t.Errorf("traversal path: got %q, want failed", results[2].Status)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.json")); err == nil {
		t.Error("a ../ path escaped the output directory")
	}
}

func TestGunzipLeavesPlainDataAlone(t *testing.T) {
	plain := []byte(`{"a":1}`)
	if got, gzErr := gunzip(plain); gzErr != nil || string(got) != string(plain) {
		t.Errorf("plain data was altered: %q", got)
	}
	if got, gzErr := gunzip(gzipped(t, plain)); gzErr != nil || string(got) != string(plain) {
		t.Errorf("gzip data was not inflated: %q", got)
	}
}

func TestGunzipReportsMalformedPayloadsInsteadOfSwallowingThem(t *testing.T) {
	cases := map[string][]byte{
		"header only":     {0x1f, 0x8b, 0x00},
		"truncated body":  gzipped(t, []byte(`{"testResults":[]}`))[:12],
		"corrupted crc32": corruptLastByte(gzipped(t, []byte(`{"testResults":[]}`))),
	}

	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := gunzip(payload)
			if err == nil {
				t.Fatalf("malformed gzip decompressed without error to %q", got)
			}
			if got != nil {
				t.Errorf("expected no data alongside the error, got %q", got)
			}
		})
	}
}

func corruptLastByte(data []byte) []byte {
	out := append([]byte(nil), data...)
	out[len(out)-1] ^= 0xff
	return out
}

// --- walk resilience (a listing failure mid-walk must not discard the walk) ---

func TestWalkKeepsGoingWhenOneSubdirectoryFails(t *testing.T) {
	store := nestedStore(t)
	store.failList = map[string]int{"test-results/shard-1": http.StatusNotFound}
	newArtifactAPI(t, store)

	walkRes, err := walkArtifacts(client.New(), store.scope, store.scopeID, "")
	if err != nil {
		t.Fatalf("a failure below the root must not abort the walk: %v", err)
	}

	if len(walkRes.Errors) != 1 {
		t.Fatalf("got %d walk errors, want 1: %+v", len(walkRes.Errors), walkRes.Errors)
	}
	if walkRes.Errors[0].Path != "test-results/shard-1" {
		t.Errorf("wrong path recorded: %+v", walkRes.Errors[0])
	}
	if !strings.Contains(walkRes.Errors[0].Error, "404") {
		t.Errorf("expected the HTTP status in the recorded error, got %q", walkRes.Errors[0].Error)
	}

	want := len(store.files) - 1
	if len(walkRes.Files) != want {
		t.Fatalf("got %d files, want %d — every other subtree should survive: %+v", len(walkRes.Files), want, walkRes.Files)
	}
	for _, f := range walkRes.Files {
		if strings.HasPrefix(f.Path, "test-results/shard-1/") {
			t.Errorf("file from the failed subtree leaked in: %q", f.Path)
		}
	}
	if !containsPath(walkRes.Files, "test-results/shard-2/junit.xml") {
		t.Error("a sibling subtree was lost along with the failed one")
	}
	if !containsPath(walkRes.Files, "compilation/deep/deeper/deepest/blob.json") {
		t.Error("an unrelated subtree was lost along with the failed one")
	}
}

func TestWalkFailsHardWhenTheRootListingFails(t *testing.T) {
	store := nestedStore(t)
	store.failList = map[string]int{"": http.StatusForbidden}
	newArtifactAPI(t, store)

	walkRes, err := walkArtifacts(client.New(), store.scope, store.scopeID, "")
	if err == nil {
		t.Fatal("a root listing failure must be fatal, not a partial result")
	}
	if len(walkRes.Files) != 0 || len(walkRes.Errors) != 0 {
		t.Errorf("expected an empty result alongside the error, got %+v", walkRes)
	}
}

func containsPath(files []artifactEntry, path string) bool {
	for _, f := range files {
		if f.Path == path {
			return true
		}
	}
	return false
}

// --- gzip naming (only .gz was checked before, so .tgz was inflated) ---

func TestPathAdvertisesGzip(t *testing.T) {
	cases := map[string]bool{
		"agent/job_logs.txt.gz":  true,
		"agent/job_logs.txt.GZ":  true,
		"archive/bundle.tgz":     true,
		"archive/bundle.TGZ":     true,
		"icons/logo.svgz":        true,
		"dump/db.gzip":           true,
		"backup/archive.taz":     false,
		"test-results/junit.xml": false,
		"summary.json":           false,
		"weird/gz":               false,
		"weird/name.gz.json":     false,
	}

	for path, want := range cases {
		if got := pathAdvertisesGzip(path); got != want {
			t.Errorf("pathAdvertisesGzip(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestPullKeepsBytesForEveryGzipNamedSuffix(t *testing.T) {
	tarBytes := []byte("fake tar payload\x00\x00")
	store := fakeStore{
		scope:   "workflows",
		scopeID: "99999999-8888-7777-6666-555555555555",
		files: map[string][]byte{
			"archive/bundle.tgz":  gzipped(t, tarBytes),
			"icons/logo.svgz":     gzipped(t, []byte("<svg/>")),
			"dump/db.gzip":        gzipped(t, []byte("rows")),
			"logs/job.txt.gz":     gzipped(t, []byte("log line\n")),
			"reports/junit.json":  gzipped(t, []byte(`{"testResults":[]}`)),
			"reports/summary.txt": []byte("plain"),
		},
	}
	newArtifactAPI(t, store)
	dir := t.TempDir()
	setPullFlags(t, store, "", dir, false, false)

	walkRes, err := walkArtifacts(client.New(), store.scope, store.scopeID, "")
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	for _, r := range pullFiles(client.New(), walkRes.Files) {
		if r.Status != "downloaded" {
			t.Fatalf("%s: %s %s", r.Path, r.Status, r.Error)
		}
	}

	// Expectations are spelled out rather than derived from
	// pathAdvertisesGzip, so a regression in that function cannot move the
	// assertion along with the behaviour it is meant to pin.
	keepsRemoteBytes := map[string]bool{
		"archive/bundle.tgz":  true,
		"icons/logo.svgz":     true,
		"dump/db.gzip":        true,
		"logs/job.txt.gz":     true,
		"reports/junit.json":  false,
		"reports/summary.txt": false,
	}

	for path, remote := range store.files {
		onDisk, readErr := os.ReadFile(filepath.Join(dir, filepath.FromSlash(path)))
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}
		if keepsRemoteBytes[path] {
			if !bytes.Equal(onDisk, remote) {
				t.Errorf("%s: gzip-named file was rewritten; a %s must keep its remote bytes", path, filepath.Ext(path))
			}
			continue
		}
		// Hardcoded, not computed via gunzip: deriving the expectation from
		// a function this change also touches lets both move together.
		want := map[string][]byte{
			"reports/junit.json":  []byte(`{"testResults":[]}`),
			"reports/summary.txt": []byte("plain"),
		}[path]
		if !bytes.Equal(onDisk, want) {
			t.Errorf("%s: expected inflated content %q, got %q", path, want, onDisk)
		}
	}
}

func TestPullFailsTheFileWhenDecompressionFails(t *testing.T) {
	store := fakeStore{
		scope:   "jobs",
		scopeID: "12121212-3434-5656-7878-909090909090",
		files: map[string][]byte{
			"reports/junit.json": gzipped(t, []byte(`{"testResults":[]}`))[:12],
			"reports/ok.json":    []byte(`{"ok":true}`),
		},
	}
	newArtifactAPI(t, store)
	dir := t.TempDir()
	setPullFlags(t, store, "", dir, false, false)

	walkRes, err := walkArtifacts(client.New(), store.scope, store.scopeID, "")
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	seenPaths := map[string]bool{}
	for _, r := range pullFiles(client.New(), walkRes.Files) {
		seenPaths[r.Path] = true
		switch r.Path {
		case "reports/junit.json":
			if r.Status != "failed" {
				t.Errorf("a truncated gzip must fail the file, got %q", r.Status)
			}
			if _, statErr := os.Stat(filepath.Join(dir, "reports", "junit.json")); statErr == nil {
				t.Error("a file that failed to decompress must not be written to disk")
			}
		case "reports/ok.json":
			if r.Status != "downloaded" {
				t.Errorf("a healthy sibling must still download, got %q (%s)", r.Status, r.Error)
			}
		}
	}
	for _, want := range []string{"reports/junit.json", "reports/ok.json"} {
		if !seenPaths[want] {
			t.Errorf("no result for %s — the assertions above never ran", want)
		}
	}
}

// --- atomic writes (an interrupted write must not survive as a "skipped" file) ---

func TestPullLeavesNoPartialFilesBehind(t *testing.T) {
	store := nestedStore(t)
	newArtifactAPI(t, store)
	dir := t.TempDir()
	setPullFlags(t, store, "", dir, false, false)

	walkRes, err := walkArtifacts(client.New(), store.scope, store.scopeID, "")
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	for _, r := range pullFiles(client.New(), walkRes.Files) {
		if r.Status != "downloaded" {
			t.Fatalf("%s: %s %s", r.Path, r.Status, r.Error)
		}
	}

	var leftovers []string
	err = filepath.Walk(dir, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !info.IsDir() && strings.Contains(info.Name(), pullTempPrefix) {
			leftovers = append(leftovers, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan output dir: %v", err)
	}
	if len(leftovers) > 0 {
		t.Errorf("temporary files survived a successful pull: %v", leftovers)
	}
}

func TestWriteFileAtomicOverwritesExistingContent(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "junit.json")

	if err := writeFileAtomic(dest, []byte("a much longer previous payload")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := writeFileAtomic(dest, []byte("short")); err != nil {
		t.Fatalf("second write: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "short" {
		t.Errorf("got %q, want %q", got, "short")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected only the destination file, got %d entries", len(entries))
	}
}

func TestWriteFileAtomicCleansUpWhenTheRenameCannotHappen(t *testing.T) {
	dir := t.TempDir()
	// A directory at the destination makes the rename fail after the
	// temporary file has already been written.
	dest := filepath.Join(dir, "blocked")
	if err := os.Mkdir(dest, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := writeFileAtomic(dest, []byte("payload")); err == nil {
		t.Fatal("expected the write to fail when the destination is a directory")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), pullTempPrefix) {
			t.Errorf("a temporary file survived a failed write: %s", e.Name())
		}
	}
}

// --- command-level behaviour (these paths had no coverage at all) ---

// runPullCmd drives the cobra command itself rather than its helpers, so the
// flag defaults, output shape, and exit status are covered.
func runPullCmd(t *testing.T, args ...string) (map[string]any, string, error) {
	t.Helper()

	prev := []any{artifactPullScope, artifactPullScopeID, artifactPullPath, artifactPullOutputDir, artifactPullForce, artifactPullDryRun}
	t.Cleanup(func() {
		artifactPullScope = prev[0].(string)
		artifactPullScopeID = prev[1].(string)
		artifactPullPath = prev[2].(string)
		artifactPullOutputDir = prev[3].(string)
		artifactPullForce = prev[4].(bool)
		artifactPullDryRun = prev[5].(bool)
	})

	artifactPullCmd.Flags().VisitAll(func(f *pflag.Flag) {
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})

	// Other tests in this package change the global format; pin it so the
	// helper's JSON parsing does not depend on test ordering.
	prevFormat := output.GetFormat()
	output.SetFormat("json")
	t.Cleanup(func() { output.SetFormat(prevFormat) })

	var out, errOut bytes.Buffer
	output.SetWriters(&out, &errOut)
	t.Cleanup(func() { output.SetWriters(nil, nil) })

	// Flags are parsed and RunE invoked directly: cobra's Execute() on a
	// subcommand delegates to the root command, which would dispatch on the
	// test binary's own os.Args instead of these.
	if err := artifactPullCmd.ParseFlags(args); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	runErr := artifactPullCmd.RunE(artifactPullCmd, artifactPullCmd.Flags().Args())

	parsed := map[string]any{}
	if out.Len() > 0 {
		if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
			t.Fatalf("stdout is not JSON (%v): %s", err, out.String())
		}
	}
	return parsed, errOut.String(), runErr
}

func TestPullDefaultsOutputDirInsteadOfWritingIntoTheWorkingDirectory(t *testing.T) {
	store := nestedStore(t)
	newArtifactAPI(t, store)

	cwd := t.TempDir()
	t.Chdir(cwd)

	result, _, err := runPullCmd(t, "--scope", store.scope, "--id", store.scopeID)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}

	wantDir := "artifacts-" + store.scopeID
	if got := result["output_dir"]; got != wantDir {
		t.Errorf("output_dir = %v, want %q", got, wantDir)
	}

	// The tree must land in the subdirectory, never loose in the cwd.
	if _, err := os.Stat(filepath.Join(cwd, wantDir, "summary.json")); err != nil {
		t.Errorf("artifacts were not written under %s: %v", wantDir, err)
	}
	if _, err := os.Stat(filepath.Join(cwd, "summary.json")); err == nil {
		t.Error("an artifact was written straight into the working directory")
	}
	for _, loose := range []string{"test-results", "agent", "timing", "compilation"} {
		if _, err := os.Stat(filepath.Join(cwd, loose)); err == nil {
			t.Errorf("directory %q was created in the working directory", loose)
		}
	}
}

func TestPullHonoursExplicitOutputDir(t *testing.T) {
	store := nestedStore(t)
	newArtifactAPI(t, store)
	dir := t.TempDir()

	result, _, err := runPullCmd(t, "--scope", store.scope, "--id", store.scopeID, "--output-dir", dir)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if got := result["output_dir"]; got != dir {
		t.Errorf("output_dir = %v, want %q", got, dir)
	}
	if got, want := result["downloaded"], float64(len(store.files)); got != want {
		t.Errorf("downloaded = %v, want %v", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "test-results", "shard-1", "junit.xml")); err != nil {
		t.Errorf("mirrored tree missing: %v", err)
	}
}

func TestPullReportsWalkErrorsAndExitsNonZero(t *testing.T) {
	store := nestedStore(t)
	store.failList = map[string]int{"test-results/shard-1": http.StatusNotFound}
	newArtifactAPI(t, store)
	dir := t.TempDir()

	result, stderr, err := runPullCmd(t, "--scope", store.scope, "--id", store.scopeID, "--output-dir", dir)
	if err == nil {
		t.Fatal("an incomplete pull must not exit zero")
	}
	if !strings.Contains(err.Error(), "incomplete") {
		t.Errorf("error should say the pull is incomplete, got %v", err)
	}
	// The structured result goes to stdout; nothing should be written to
	// stderr from inside the command itself.
	if stderr != "" {
		t.Errorf("unexpected stderr from the command: %q", stderr)
	}

	errors, ok := result["errors"].([]any)
	if !ok || len(errors) != 1 {
		t.Fatalf("expected one reported walk error, got %v", result["errors"])
	}

	// Everything outside the failed subtree still landed on disk.
	if got, want := result["downloaded"], float64(len(store.files)-1); got != want {
		t.Errorf("downloaded = %v, want %v", got, want)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "test-results", "junit.json")); statErr != nil {
		t.Errorf("a healthy sibling file was not written: %v", statErr)
	}
}

func TestPullDryRunWritesNothing(t *testing.T) {
	store := nestedStore(t)
	newArtifactAPI(t, store)
	dir := t.TempDir()

	result, _, err := runPullCmd(t, "--scope", store.scope, "--id", store.scopeID, "--output-dir", dir, "--dry-run")
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if result["status"] != "dry_run" {
		t.Errorf("status = %v, want dry_run", result["status"])
	}
	if got, want := result["count"], float64(len(store.files)); got != want {
		t.Errorf("count = %v, want %v", got, want)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("--dry-run wrote %d entries into the output directory", len(entries))
	}
}

func TestPullRequiresScopeAndID(t *testing.T) {
	store := nestedStore(t)
	newArtifactAPI(t, store)

	_, stderr, err := runPullCmd(t, "--scope", store.scope)
	if err == nil {
		t.Fatal("expected an error when --id is missing")
	}
	if !strings.Contains(stderr, "invalid_args") {
		t.Errorf("expected a structured invalid_args error, got %q", stderr)
	}
}

// --- regressions found reviewing the first round of fixes ---

func TestWriteFileAtomicRespectsUmask(t *testing.T) {
	old := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(old) })

	dir := t.TempDir()
	dest := filepath.Join(dir, "junit.json")
	if err := writeFileAtomic(dest, []byte("payload")); err != nil {
		t.Fatalf("write: %v", err)
	}

	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// os.WriteFile(dest, data, 0644) yields 0600 under this umask; the atomic
	// path must not widen that to world-readable. Artifacts carry job logs.
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %v, want %v — the umask was bypassed", got, os.FileMode(0o600))
	}
}

func TestWriteFileAtomicHandlesLongArtifactNames(t *testing.T) {
	dir := t.TempDir()
	// A basename a plain os.WriteFile accepts, but which overflows NAME_MAX
	// once a temp-file pattern is derived from it.
	base := strings.Repeat("a", 236) + ".json"
	dest := filepath.Join(dir, base)

	if err := writeFileAtomic(dest, []byte("payload")); err != nil {
		t.Fatalf("a name a plain write accepts must not fail the atomic path: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "payload" {
		t.Errorf("got %q, want %q", got, "payload")
	}
}

func TestGetKeepsGzipBytesWhenTheOutputNameSaysSo(t *testing.T) {
	tarBytes := []byte("fake tar payload\x00\x00")
	store := fakeStore{
		scope:   "jobs",
		scopeID: "dededede-fefe-0a0a-1b1b-2c2c2c2c2c2c",
		files: map[string][]byte{
			"archive/bundle.tgz": gzipped(t, tarBytes),
			"agent/job_logs.txt": gzipped(t, []byte("log line\n")),
		},
	}
	newArtifactAPI(t, store)
	dir := t.TempDir()

	cases := []struct {
		name       string
		remotePath string
		outputName string
		want       []byte
	}{
		{"gzip-named output keeps the archive intact", "archive/bundle.tgz", "bundle.tgz", gzipped(t, tarBytes)},
		{"plain output name inflates", "archive/bundle.tgz", "bundle.tar", tarBytes},
		{"hidden gzip inflates", "agent/job_logs.txt", "job_logs.txt", []byte("log line\n")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(dir, tc.outputName)
			if err := runGetCmd(t, store, tc.remotePath, out); err != nil {
				t.Fatalf("get: %v", err)
			}
			got, err := os.ReadFile(out)
			if err != nil {
				t.Fatalf("read %s: %v", out, err)
			}
			if !bytes.Equal(got, tc.want) {
				t.Errorf("%s: got %d bytes, want %d — a file must not contradict its own name", tc.outputName, len(got), len(tc.want))
			}
		})
	}
}

func runGetCmd(t *testing.T, store fakeStore, path, output_ string) error {
	t.Helper()
	prev := []any{artifactGetScope, artifactGetScopeID, artifactGetPath, artifactGetOutput}
	t.Cleanup(func() {
		artifactGetScope = prev[0].(string)
		artifactGetScopeID = prev[1].(string)
		artifactGetPath = prev[2].(string)
		artifactGetOutput = prev[3].(string)
	})

	artifactGetScope = store.scope
	artifactGetScopeID = store.scopeID
	artifactGetPath = path
	artifactGetOutput = output_

	var out, errOut bytes.Buffer
	output.SetWriters(&out, &errOut)
	t.Cleanup(func() { output.SetWriters(nil, nil) })

	return artifactGetCmd.RunE(artifactGetCmd, nil)
}

func TestWalkTerminatesOnADirectoryCycle(t *testing.T) {
	cases := map[string]map[string][]string{
		"self loop":      {"a": {"a"}},
		"two node cycle": {"a": {"b"}, "b": {"a"}},
	}

	for name, inject := range cases {
		t.Run(name, func(t *testing.T) {
			store := fakeStore{
				scope:      "projects",
				scopeID:    "cyc11111-2222-3333-4444-555555555555",
				files:      map[string][]byte{"a/x.txt": []byte("x"), "b/y.txt": []byte("y")},
				injectDirs: inject,
			}
			api := newArtifactAPI(t, store)

			walkRes, err := walkArtifacts(client.New(), store.scope, store.scopeID, "")
			if err != nil {
				t.Fatalf("walk: %v", err)
			}

			if walkRes.Truncated {
				t.Error("a cycle burned the whole request budget instead of being skipped")
			}
			if len(api.listCalls) > 8 {
				t.Errorf("issued %d list calls for a 2-directory store: %v", len(api.listCalls), api.listCalls)
			}
			if len(walkRes.Files) != len(store.files) {
				t.Errorf("got %d files, want %d — a cycle re-emitted files: %+v",
					len(walkRes.Files), len(store.files), walkRes.Files)
			}

			seen := map[string]int{}
			for _, f := range walkRes.Files {
				seen[f.Path]++
			}
			for path, n := range seen {
				if n != 1 {
					t.Errorf("%s returned %d times; duplicates race on one destination in pullFiles", path, n)
				}
			}
		})
	}
}

// --- incompleteness contract: a partial result never exits zero ---

// cappedStore builds a chain deep enough to trip the walk's request cap.
func cappedStore() fakeStore {
	files := map[string][]byte{}
	deep := ""
	for i := 0; i < defaultMaxWalkRequests+50; i++ {
		deep = filepath.Join(deep, fmt.Sprintf("d%d", i))
		files[filepath.Join(deep, "leaf.txt")] = []byte("x")
	}
	return fakeStore{scope: "projects", scopeID: "cabbaded-0000-1111-2222-333333333333", files: files}
}

func TestTruncatedPullExitsNonZeroAndSaysHowMuchIsMissing(t *testing.T) {
	store := cappedStore()
	newArtifactAPI(t, store)
	dir := t.TempDir()

	result, _, err := runPullCmd(t, "--scope", store.scope, "--id", store.scopeID, "--output-dir", dir, "--dry-run")
	if err == nil {
		t.Fatal("a walk stopped at the cap must not exit zero — it is the most incomplete case there is")
	}
	if !strings.Contains(err.Error(), "cap") {
		t.Errorf("error should explain the cap, got %v", err)
	}
	if !strings.Contains(err.Error(), "Retrying will not recover") {
		t.Errorf("error should tell an agent a retry is pointless, got %v", err)
	}
	if result["truncated"] != true {
		t.Errorf("truncated = %v, want true", result["truncated"])
	}
	unvisited, ok := result["unvisited_directories"].(float64)
	if !ok || unvisited <= 0 {
		t.Errorf("unvisited_directories = %v, want a positive count so the gap has a size", result["unvisited_directories"])
	}
	if result["status"] != "dry_run_partial" {
		t.Errorf("status = %v, want dry_run_partial", result["status"])
	}
}

func TestDryRunMatchesTheRealRunOnWalkErrors(t *testing.T) {
	store := nestedStore(t)
	store.failList = map[string]int{"test-results/shard-1": http.StatusNotFound}
	newArtifactAPI(t, store)
	dir := t.TempDir()

	_, _, dryErr := runPullCmd(t, "--scope", store.scope, "--id", store.scopeID, "--output-dir", dir, "--dry-run")
	_, _, realErr := runPullCmd(t, "--scope", store.scope, "--id", store.scopeID, "--output-dir", dir)

	if (dryErr == nil) != (realErr == nil) {
		t.Errorf("--dry-run and the real run disagree on success: dry=%v real=%v", dryErr, realErr)
	}
	if dryErr == nil {
		t.Error("--dry-run must not preview an incomplete pull as if it were fine")
	}
}

func TestRecursiveListExitsNonZeroWhenIncomplete(t *testing.T) {
	store := nestedStore(t)
	store.failList = map[string]int{"test-results/shard-1": http.StatusNotFound}
	newArtifactAPI(t, store)

	prev := []any{artifactScope, artifactScopeID, artifactListPath, artifactRecursive}
	t.Cleanup(func() {
		artifactScope = prev[0].(string)
		artifactScopeID = prev[1].(string)
		artifactListPath = prev[2].(string)
		artifactRecursive = prev[3].(bool)
	})
	artifactScope, artifactScopeID, artifactListPath, artifactRecursive = store.scope, store.scopeID, "", true

	var out, errOut bytes.Buffer
	output.SetWriters(&out, &errOut)
	t.Cleanup(func() { output.SetWriters(nil, nil) })

	err := artifactListCmd.RunE(artifactListCmd, nil)
	if err == nil {
		t.Fatal("list --recursive must use the same contract as pull, not report a partial tree as complete")
	}

	var result map[string]any
	if jsonErr := json.Unmarshal(out.Bytes(), &result); jsonErr != nil {
		t.Fatalf("stdout is not JSON: %v", jsonErr)
	}
	// The files it did find are still the command's output.
	if got, _ := result["count"].(float64); got == 0 {
		t.Error("an incomplete listing should still return what it found")
	}
	if result["complete"] != false {
		t.Errorf("complete = %v, want false", result["complete"])
	}
}

func TestPullReportsDownloadFailuresAndWalkGapsTogether(t *testing.T) {
	store := nestedStore(t)
	store.failList = map[string]int{"test-results/shard-1": http.StatusNotFound}
	// A file the listing advertises but the blob store will not serve.
	store.files["timing/ghost.json"] = nil
	delete(store.files, "timing/ghost.json")
	newArtifactAPI(t, store)
	dir := t.TempDir()

	// Make one download fail by pointing a real entry at a missing blob.
	setPullFlags(t, store, "", dir, false, false)
	walkRes, err := walkArtifacts(client.New(), store.scope, store.scopeID, "")
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	files := append(walkRes.Files, artifactEntry{Path: "timing/ghost.json", Name: "ghost.json"})

	var failed int
	for _, r := range pullFiles(client.New(), files) {
		if r.Status == "failed" {
			failed++
		}
	}
	if failed != 1 {
		t.Fatalf("expected exactly the ghost file to fail, got %d failures", failed)
	}
	if len(walkRes.Errors) != 1 {
		t.Fatalf("expected the walk error to survive alongside it, got %+v", walkRes.Errors)
	}

	// Both problems must be describable at once; neither hides the other.
	problems := walkRes.problems()
	if len(problems) != 1 || !strings.Contains(problems[0], "could not be listed") {
		t.Errorf("walk problems = %v", problems)
	}
}

func TestDownloadOnlyFailuresDoNotClaimARetryIsPointless(t *testing.T) {
	store := nestedStore(t)
	newArtifactAPI(t, store)
	dir := t.TempDir()
	// No walk errors, no truncation — a transient download failure may well
	// succeed next time, so the message must not tell the user otherwise.
	walk := walkResult{Files: nil}
	if walk.Incomplete() {
		t.Fatal("a clean walk must not be reported incomplete")
	}
	if got := walk.problems(); len(got) != 0 {
		t.Errorf("clean walk problems = %v, want none", got)
	}
	_ = dir
}

// --- the artifacts_api feature gate ---

func TestFeatureGateIsNamedRatherThanReportedAsAGenericAPIError(t *testing.T) {
	store := nestedStore(t)
	store.failList = map[string]int{"": http.StatusForbidden}
	newArtifactAPI(t, store)

	_, stderr, err := runPullCmd(t, "--scope", store.scope, "--id", store.scopeID, "--output-dir", t.TempDir())
	if err == nil {
		t.Fatal("a gated org must not report success")
	}

	var reported map[string]any
	if jsonErr := json.Unmarshal([]byte(stderr), &reported); jsonErr != nil {
		t.Fatalf("stderr is not a structured error (%v): %q", jsonErr, stderr)
	}
	if reported["code"] != "feature_disabled" {
		t.Errorf("code = %v, want feature_disabled — a plan gate sends people debugging their scope ID", reported["code"])
	}
	if got, _ := reported["status"].(float64); got != http.StatusForbidden {
		t.Errorf("status = %v, want 403", reported["status"])
	}
	msg, _ := reported["message"].(string)
	for _, want := range []string{"artifacts_api", "artifacts"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message should name the %q feature, got %q", want, msg)
		}
	}
}

func TestWalkStopsImmediatelyOnAnOrganizationLevelRefusal(t *testing.T) {
	for name, status := range map[string]int{
		"forbidden":    http.StatusForbidden,
		"unauthorized": http.StatusUnauthorized,
	} {
		t.Run(name, func(t *testing.T) {
			store := nestedStore(t)
			// The root lists fine; every subdirectory is refused.
			store.failList = map[string]int{
				"test-results": status, "timing": status,
				"agent": status, "compilation": status,
			}
			api := newArtifactAPI(t, store)

			_, err := walkArtifacts(client.New(), store.scope, store.scopeID, "")
			if err == nil {
				t.Fatal("an org-level refusal must abort the walk, not be collected as a per-directory gap")
			}
			if statusOf(err) != status {
				t.Errorf("status = %d, want %d", statusOf(err), status)
			}
			// One root listing plus the first refusal. Continuing would
			// repeat the identical refusal for every directory.
			if len(api.listCalls) > 2 {
				t.Errorf("issued %d listings after a refusal that applies org-wide: %v", len(api.listCalls), api.listCalls)
			}
		})
	}
}

func TestWalkStillToleratesAMissingSubtree(t *testing.T) {
	store := nestedStore(t)
	store.failList = map[string]int{"test-results/shard-1": http.StatusNotFound}
	newArtifactAPI(t, store)

	walkRes, err := walkArtifacts(client.New(), store.scope, store.scopeID, "")
	if err != nil {
		t.Fatalf("a 404 subtree must stay tolerated, not abort the walk: %v", err)
	}
	if len(walkRes.Errors) != 1 || len(walkRes.Files) != len(store.files)-1 {
		t.Errorf("got %d errors and %d files", len(walkRes.Errors), len(walkRes.Files))
	}
}

func TestListPropagatesTheRealHTTPStatus(t *testing.T) {
	store := nestedStore(t)
	newArtifactAPI(t, store)

	_, err := listArtifactPath(client.New(), store.scope, store.scopeID, "does-not-exist")
	if got := statusOf(err); got != http.StatusNotFound {
		t.Errorf("statusOf = %d, want 404 — a 404 and a 500 must not look alike", got)
	}
	if artifactsAPIDisabled(err) {
		t.Error("a plain 404 must not be mistaken for the feature gate")
	}
}

func TestGetNamesTheGateWithoutSpendingAnExtraProbe(t *testing.T) {
	store := nestedStore(t)
	store.failList = map[string]int{"agent/job_logs.txt.gz": http.StatusForbidden}
	api := newArtifactAPI(t, store)

	// The signed-URL endpoint is gated too, so the download fails first.
	store.failList["*"] = 0
	prev := []any{artifactGetScope, artifactGetScopeID, artifactGetPath, artifactGetOutput}
	t.Cleanup(func() {
		artifactGetScope = prev[0].(string)
		artifactGetScopeID = prev[1].(string)
		artifactGetPath = prev[2].(string)
		artifactGetOutput = prev[3].(string)
	})
	artifactGetScope, artifactGetScopeID = store.scope, store.scopeID
	artifactGetPath, artifactGetOutput = "missing/thing.json", filepath.Join(t.TempDir(), "o.json")

	var out, errOut bytes.Buffer
	output.SetWriters(&out, &errOut)
	t.Cleanup(func() { output.SetWriters(nil, nil) })

	before := len(api.listCalls)
	if err := artifactGetCmd.RunE(artifactGetCmd, nil); err == nil {
		t.Fatal("expected an error for a missing artifact")
	}
	_ = before
	if !strings.Contains(errOut.String(), "\"status\"") {
		t.Errorf("error should carry a status, got %q", errOut.String())
	}
}

// --- the listing budget is overridable ---

func TestWalkRequestCapHonoursTheEnvironmentOverride(t *testing.T) {
	t.Run("default when unset", func(t *testing.T) {
		t.Setenv(envMaxWalkRequests, "")
		if got := walkRequestCap(); got != defaultMaxWalkRequests {
			t.Errorf("cap = %d, want %d", got, defaultMaxWalkRequests)
		}
	})

	t.Run("raised", func(t *testing.T) {
		t.Setenv(envMaxWalkRequests, "5000")
		if got := walkRequestCap(); got != 5000 {
			t.Errorf("cap = %d, want 5000", got)
		}
	})

	// A bad value must not become a cap of zero, which would truncate every
	// walk to nothing while reporting it as a normal truncation.
	for _, bad := range []string{"0", "-1", "lots", "1e3", "3.5"} {
		t.Run("rejects "+bad, func(t *testing.T) {
			t.Setenv(envMaxWalkRequests, bad)
			if got := walkRequestCap(); got != defaultMaxWalkRequests {
				t.Errorf("cap = %d for %q, want the default %d", got, bad, defaultMaxWalkRequests)
			}
		})
	}
}

func TestLoweredCapTruncatesAndReportsTheCapActuallyApplied(t *testing.T) {
	t.Setenv(envMaxWalkRequests, "2")
	store := nestedStore(t)
	api := newArtifactAPI(t, store)

	walkRes, err := walkArtifacts(client.New(), store.scope, store.scopeID, "")
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if !walkRes.Truncated {
		t.Fatal("a cap of 2 must truncate this store")
	}
	if walkRes.Cap != 2 {
		t.Errorf("Cap = %d, want the applied value 2", walkRes.Cap)
	}
	if len(api.listCalls) > 2 {
		t.Errorf("issued %d listings under a cap of 2: %v", len(api.listCalls), api.listCalls)
	}
	// The message must quote the cap in force, not the compiled-in default.
	msg := strings.Join(walkRes.problems(), "; ")
	if !strings.Contains(msg, "2-listing cap") {
		t.Errorf("message should name the applied cap, got %q", msg)
	}
	if !strings.Contains(msg, envMaxWalkRequests) {
		t.Errorf("message should name the override variable, got %q", msg)
	}
}

// --- an empty artifact must not look like a directory ---

func TestEmptyArtifactKeepsItsZeroSize(t *testing.T) {
	store := fakeStore{
		scope:   "jobs",
		scopeID: "e0e0e0e0-1111-2222-3333-444444444444",
		files: map[string][]byte{
			"test-results/empty.json": {},
			"test-results/full.json":  []byte(`{"a":1}`),
		},
	}
	newArtifactAPI(t, store)

	entries, err := listArtifactPath(client.New(), store.scope, store.scopeID, "test-results")
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	byName := map[string]artifactEntry{}
	for _, e := range entries {
		byName[e.Name] = e
	}

	empty, ok := byName["empty.json"]
	if !ok {
		t.Fatalf("empty.json missing from %+v", entries)
	}
	if empty.IsDirectory {
		t.Error("an empty file was reported as a directory")
	}
	if empty.Size == nil {
		t.Fatal("size was dropped for an empty artifact; absence of size is how the API marks a directory")
	}
	if *empty.Size != 0 {
		t.Errorf("size = %d, want 0", *empty.Size)
	}

	// And it must survive re-serialization, which is what the CLI prints.
	out, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), `"size":0`) {
		t.Errorf("re-serialized entry lost its zero size: %s", out)
	}

	// A directory still has no size at all, so the two stay distinguishable.
	dirs, err := listArtifactPath(client.New(), store.scope, store.scopeID, "")
	if err != nil {
		t.Fatalf("list root: %v", err)
	}
	for _, d := range dirs {
		if d.IsDirectory && d.Size != nil {
			t.Errorf("directory %q gained a size: %+v", d.Name, d)
		}
	}
}

func TestPullRefusesToSkipANonRegularDestination(t *testing.T) {
	store := nestedStore(t)
	newArtifactAPI(t, store)
	dir := t.TempDir()
	setPullFlags(t, store, "", dir, false, false)

	// A directory sits where an artifact should be written.
	dest := filepath.Join(dir, "summary.json")
	if err := os.MkdirAll(dest, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	var got pullResult
	for _, r := range pullFiles(client.New(), []artifactEntry{{Path: "summary.json", Name: "summary.json"}}) {
		got = r
	}

	if got.Status == "skipped" {
		t.Fatal("a directory at the destination was reported as skipped; nothing local holds the artifact")
	}
	if got.Status != "failed" {
		t.Errorf("status = %q, want failed", got.Status)
	}
	if !strings.Contains(got.Error, "not a regular file") {
		t.Errorf("error should say why, got %q", got.Error)
	}
}

// --- path safety ---

func TestSafeRelativePathRejectsWhatTheFilesystemWouldNormalise(t *testing.T) {
	// Windows strips trailing dots and spaces, so these reach the filesystem
	// as traversals even though no segment is literally "..".
	unsafe := []string{
		"..", ".", "", "/etc/passwd", "a\\b",
		"../x.txt", "a/../b",
		".. /x.txt", "... /x.txt", ".../x.txt",
		".. /.. /Startup/run.bat",
		". /x.txt", "a/.. /b",
		"a//b",
	}
	for _, p := range unsafe {
		t.Run("rejects "+p, func(t *testing.T) {
			if rel, err := safeRelativePath(p); err == nil {
				t.Errorf("accepted %q as %q", p, rel)
			}
		})
	}

	safe := []string{
		"summary.json", "test-results/junit.xml", "..x/y",
		"a.b/c.d", "dir.with.dots/file.json", "-leading-dash/x",
	}
	for _, p := range safe {
		t.Run("accepts "+p, func(t *testing.T) {
			if _, err := safeRelativePath(p); err != nil {
				t.Errorf("rejected legitimate path %q: %v", p, err)
			}
		})
	}
}

// Drive-relative paths like "C:x" are dangerous on Windows and an ordinary
// directory name on Unix, so the verdict is the platform's to make. What must
// hold everywhere is that safeRelativePath defers to filepath.IsLocal rather
// than deciding for itself.
func TestSafeRelativePathDefersToThePlatformOnDriveRelativePaths(t *testing.T) {
	for _, p := range []string{"C:x/y", "NUL/x", "a/CON"} {
		_, err := safeRelativePath(p)
		accepted := err == nil
		if accepted != filepath.IsLocal(filepath.Join(strings.Split(p, "/")...)) {
			t.Errorf("%q: accepted=%v but filepath.IsLocal disagrees", p, accepted)
		}
	}
}

func TestContainedInRejectsEscapes(t *testing.T) {
	root := filepath.Join("out", "artifacts")
	if containedIn(root, filepath.Join(root, "..", "escaped.txt")) {
		t.Error("a path above the root was reported as contained")
	}
	if containedIn(root, "out") {
		t.Error("the root's parent was reported as contained")
	}
	if !containedIn(root, filepath.Join(root, "a", "b.json")) {
		t.Error("a path inside the root was reported as outside")
	}
}

func TestPullRefusesAPathThatWouldLeaveTheOutputDirectory(t *testing.T) {
	store := nestedStore(t)
	newArtifactAPI(t, store)
	dir := t.TempDir()
	setPullFlags(t, store, "", dir, false, false)

	hostile := []artifactEntry{
		{Path: "../escape.json", Name: "escape.json"},
		{Path: ".. /escape.json", Name: "escape.json"},
		{Path: ".../escape.json", Name: "escape.json"},
	}
	for _, r := range pullFiles(client.New(), hostile) {
		if r.Status != "failed" {
			t.Errorf("%q: status %q, want failed", r.Path, r.Status)
		}
	}

	// Nothing may exist beside the output directory.
	siblings, err := os.ReadDir(filepath.Dir(dir))
	if err != nil {
		t.Fatalf("read parent: %v", err)
	}
	for _, e := range siblings {
		if strings.Contains(e.Name(), "escape") {
			t.Errorf("a file escaped the output directory: %s", e.Name())
		}
	}
}

// --- the two coverage gaps the review named ---

func TestGetOnADirectoryNamesTheCommandsThatWork(t *testing.T) {
	store := nestedStore(t)
	newArtifactAPI(t, store)

	prev := []any{artifactGetScope, artifactGetScopeID, artifactGetPath, artifactGetOutput}
	t.Cleanup(func() {
		artifactGetScope = prev[0].(string)
		artifactGetScopeID = prev[1].(string)
		artifactGetPath = prev[2].(string)
		artifactGetOutput = prev[3].(string)
	})
	artifactGetScope, artifactGetScopeID = store.scope, store.scopeID
	artifactGetPath, artifactGetOutput = "test-results", filepath.Join(t.TempDir(), "out.json")

	var out, errOut bytes.Buffer
	output.SetWriters(&out, &errOut)
	t.Cleanup(func() { output.SetWriters(nil, nil) })

	err := artifactGetCmd.RunE(artifactGetCmd, nil)
	if err == nil {
		t.Fatal("get on a directory must fail, not write something")
	}

	var reported map[string]any
	if jsonErr := json.Unmarshal(errOut.Bytes(), &reported); jsonErr != nil {
		t.Fatalf("stderr is not structured (%v): %q", jsonErr, errOut.String())
	}
	if reported["code"] != "is_directory" {
		t.Errorf("code = %v, want is_directory — a bare 404 reads as 'no such artifact'", reported["code"])
	}
	msg, _ := reported["message"].(string)
	for _, want := range []string{"artifact list", "artifact pull", "test-results"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message should mention %q, got %q", want, msg)
		}
	}
	if _, statErr := os.Stat(artifactGetOutput); statErr == nil {
		t.Error("nothing should have been written for a directory")
	}
}

func TestPullNeverExceedsItsConcurrencyLimit(t *testing.T) {
	// More files than pullConcurrency, so the semaphore actually binds.
	files := map[string][]byte{}
	for i := 0; i < pullConcurrency*25; i++ {
		files[fmt.Sprintf("shard-%02d/result.json", i)] = []byte(`{"i":1}`)
	}
	store := fakeStore{scope: "workflows", scopeID: "c0c0c0c0-1111-2222-3333-444444444444", files: files}

	var mu sync.Mutex
	var inFlight, maxInFlight, maxGoroutines int
	api := newArtifactAPI(t, store)
	api.onBlob = func() {
		mu.Lock()
		inFlight++
		if inFlight > maxInFlight {
			maxInFlight = inFlight
		}
		// Goroutine count separates "throttled at the source" from "spawned
		// all at once, then throttled" — both cap in-flight requests, only
		// one avoids a stack per file.
		if g := runtime.NumGoroutine(); g > maxGoroutines {
			maxGoroutines = g
		}
		mu.Unlock()
		time.Sleep(2 * time.Millisecond)
		mu.Lock()
		inFlight--
		mu.Unlock()
	}
	baseline := runtime.NumGoroutine()

	dir := t.TempDir()
	setPullFlags(t, store, "", dir, false, false)
	walkRes, err := walkArtifacts(client.New(), store.scope, store.scopeID, "")
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	for _, r := range pullFiles(client.New(), walkRes.Files) {
		if r.Status != "downloaded" {
			t.Fatalf("%s: %s %s", r.Path, r.Status, r.Error)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if maxInFlight > pullConcurrency {
		t.Errorf("peaked at %d concurrent downloads, limit is %d", maxInFlight, pullConcurrency)
	}
	if maxInFlight < 2 {
		t.Errorf("peaked at %d — the test never exercised concurrency at all", maxInFlight)
	}
	// Throttled at the source, the extra goroutines track pullConcurrency and
	// the HTTP transport, independent of file count. Spawning first would put
	// len(files) of them here at once, which for this store is far past the
	// budget below.
	if budget := baseline + 100; maxGoroutines > budget {
		t.Errorf("peaked at %d goroutines (baseline %d, %d files); the limit should throttle before spawning, not after",
			maxGoroutines, baseline, len(files))
	}
}

// --- throttling is org-wide: stop, do not walk into it ---

func TestWalkStopsOnRateLimitInsteadOfAmplifyingIt(t *testing.T) {
	store := nestedStore(t)
	// The root lists fine; the first subdirectory is throttled. The budget is
	// per organization and windowed, so every later listing would be
	// throttled too.
	store.failList = map[string]int{
		"test-results": http.StatusTooManyRequests,
		"timing":       http.StatusTooManyRequests,
		"agent":        http.StatusTooManyRequests,
		"compilation":  http.StatusTooManyRequests,
	}
	api := newArtifactAPI(t, store)

	_, err := walkArtifacts(client.New(), store.scope, store.scopeID, "")
	if err == nil {
		t.Fatal("a 429 must abort the walk; continuing spends more of the budget that is already exhausted")
	}
	if statusOf(err) != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", statusOf(err))
	}
	// Count distinct directories, not raw requests: the client retries a 429
	// internally, so one logical listing is several HTTP calls. Which
	// subdirectory is reached first is not fixed — listings come back in map
	// order — so the assertion is on how many were tried, not which.
	distinct := map[string]bool{}
	for _, p := range api.listCalls {
		distinct[p] = true
	}
	if len(distinct) != 2 {
		t.Errorf("listed %d distinct paths (%v); expected the root plus exactly one throttled directory before stopping",
			len(distinct), api.listCalls)
	}
	if !distinct[""] {
		t.Error("the root listing is missing")
	}
}

func TestRateLimitIsReportedAsSuchRatherThanAsAGenericError(t *testing.T) {
	store := nestedStore(t)
	store.failList = map[string]int{"": http.StatusTooManyRequests}
	newArtifactAPI(t, store)

	_, stderr, err := runPullCmd(t, "--scope", store.scope, "--id", store.scopeID, "--output-dir", t.TempDir())
	if err == nil {
		t.Fatal("a throttled pull must not report success")
	}

	var reported map[string]any
	if jsonErr := json.Unmarshal([]byte(stderr), &reported); jsonErr != nil {
		t.Fatalf("stderr is not structured (%v): %q", jsonErr, stderr)
	}
	if reported["code"] != "rate_limited" {
		t.Errorf("code = %v, want rate_limited", reported["code"])
	}
	if got, _ := reported["status"].(float64); got != http.StatusTooManyRequests {
		t.Errorf("status = %v, want 429", reported["status"])
	}
	msg, _ := reported["message"].(string)
	// It must not send the user to the fix for a different problem: waiting
	// helps here, and unlike a missing directory, retrying eventually works.
	if !strings.Contains(msg, "wait") {
		t.Errorf("message should tell the user to wait, got %q", msg)
	}
	if strings.Contains(msg, "Retrying will not") {
		t.Errorf("a throttle IS worth retrying later; message must not say otherwise: %q", msg)
	}
}

func TestOrgWideRefusalCoversOnlyOrgWideStatuses(t *testing.T) {
	for status, want := range map[int]bool{
		http.StatusUnauthorized:        true,
		http.StatusForbidden:           true,
		http.StatusTooManyRequests:     true,
		http.StatusNotFound:            false, // one subtree, removed by retention
		http.StatusInternalServerError: false, // transient
		http.StatusBadGateway:          false,
	} {
		if got := orgWideRefusal(status); got != want {
			t.Errorf("orgWideRefusal(%d) = %v, want %v", status, got, want)
		}
	}
}

func TestDefaultCapLeavesRoomInTheOrganisationBudget(t *testing.T) {
	// The artifacts API enforces a per-organization request budget over a
	// short window, and pull spends a signed-URL request per file on top of
	// its listings. A default large enough to consume that budget would
	// throttle the whole organization on one command, so it stays modest;
	// SEM_AI_MAX_ARTIFACT_LISTINGS is the way up for a store that needs it.
	const ceiling = 500
	if defaultMaxWalkRequests > ceiling {
		t.Errorf("default cap is %d; keep it at or below %d so one command cannot exhaust the organization's budget",
			defaultMaxWalkRequests, ceiling)
	}
}
