package cmd

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/semaphoreio/sem-ai/pkg/client"
	"github.com/semaphoreio/sem-ai/pkg/config"
	"github.com/semaphoreio/sem-ai/pkg/output"
	"github.com/spf13/cobra"
)

var artifactCmd = &cobra.Command{
	Use:   "artifact",
	Short: "Artifact operations: list and download build artifacts",
}

const (
	// defaultMaxWalkRequests bounds the directory listings one walk may issue.
	// The artifacts API enforces a per-organization request budget over a
	// short window, shared by every consumer in that organization, so one
	// command should not be able to spend most of it — and `pull` spends a
	// signed-URL request per file on top of its listings. Raise it with
	// SEM_AI_MAX_ARTIFACT_LISTINGS for a store that genuinely needs more.
	//
	// It is not a cost control — artifact billing meters storage and egress,
	// never request counts. The operations are absorbed server-side.
	defaultMaxWalkRequests = 500
	envMaxWalkRequests     = "SEM_AI_MAX_ARTIFACT_LISTINGS"

	pullConcurrency = 8

	// pullTempPrefix names the in-progress files writeFileAtomic renames from.
	// Tests assert on it, so it lives here rather than inline.
	pullTempPrefix = ".sem-ai-pull-"
)

// walkRequestCap returns the listing budget for one walk. A project-scope
// store can legitimately exceed the default, so it is overridable; a value
// that is not a positive integer is reported and ignored rather than silently
// turning into a cap of zero.
func walkRequestCap() int {
	raw := strings.TrimSpace(os.Getenv(envMaxWalkRequests))
	if raw == "" {
		return defaultMaxWalkRequests
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		output.Warn(fmt.Sprintf("%s=%q is not a positive integer; using the default of %d",
			envMaxWalkRequests, raw, defaultMaxWalkRequests))
		return defaultMaxWalkRequests
	}
	return n
}

// apiError carries the HTTP status alongside the message so callers can tell
// a missing path from a permission gate. Most of cmd/ passes the real status
// to output.Error; the artifact commands do the same through this.
type apiError struct {
	Status int
	Body   string
}

func (e *apiError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("HTTP %d", e.Status)
	}
	return fmt.Sprintf("HTTP %d: %s", e.Status, e.Body)
}

func statusOf(err error) int {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		return apiErr.Status
	}
	// Retries exhausted inside the client: the status survives on its error
	// type, and losing it here would turn a rate limit into a generic
	// failure the walk then treats as one bad directory.
	var httpErr *client.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode
	}
	return 0
}

// orgWideRefusal reports whether a status describes a refusal that applies to
// the whole organization rather than to one path, and so cannot be improved by
// asking about a different directory.
func orgWideRefusal(status int) bool {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
		return true
	}
	return false
}

// artifactsAPIDisabled reports whether err is the v1alpha gate refusing the
// artifacts API for this organization. The gate requires BOTH the `artifacts`
// and `artifacts_api` features; some plans enable the first and not the
// second, so "artifacts work in the UI" is not evidence that this will.
func artifactsAPIDisabled(err error) bool {
	if statusOf(err) != http.StatusForbidden {
		return false
	}
	var body string
	var apiErr *apiError
	var httpErr *client.HTTPError
	switch {
	case errors.As(err, &apiErr):
		body = apiErr.Body
	case errors.As(err, &httpErr):
		body = httpErr.Body
	}
	return strings.Contains(strings.ToLower(body), "artifacts api feature is not enabled")
}

// reportArtifactError names the failure the user actually hit. A feature gate
// reported as a generic api_error sends people debugging their scope ID.
func reportArtifactError(err error) {
	if statusOf(err) == http.StatusTooManyRequests {
		wait := "a few minutes"
		var httpErr *client.HTTPError
		if errors.As(err, &httpErr) && httpErr.RetryAfter > 0 {
			wait = httpErr.RetryAfter.String()
		}
		output.Error("rate_limited",
			fmt.Sprintf("the artifacts API rate limit for this organization was exceeded. The budget is per organization and windowed, so it is shared with everything else in the org and retrying immediately makes it worse — wait %s, or narrow the request with --path", wait),
			http.StatusTooManyRequests)
		return
	}
	if artifactsAPIDisabled(err) {
		output.Error("feature_disabled",
			"the artifacts API is not enabled for this organization: both the `artifacts` and `artifacts_api` features are required, and a plan may enable one without the other. Contact support to enable it",
			http.StatusForbidden)
		return
	}
	status := statusOf(err)
	if status == 0 {
		status = 1
	}
	output.Error("api_error", err.Error(), status)
}

type artifactEntry struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	IsDirectory bool   `json:"is_directory"`
	// Size is a pointer because the API omits it for directories and only for
	// directories: absence is how a directory is marked. A plain int64 with
	// omitempty erased "size": 0 for genuinely empty artifacts, making an
	// empty file indistinguishable from a directory on the way back out.
	Size *int64 `json:"size,omitempty"`
}

func listArtifactPath(c *client.Client, scope, scopeID, path string) ([]artifactEntry, error) {
	params := url.Values{}
	params.Set("scope", scope)
	params.Set("scope_id", scopeID)
	if path != "" {
		params.Set("path", path)
	}

	resp, err := c.ListWithParams("artifacts", params)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, &apiError{Status: resp.StatusCode, Body: string(resp.Body)}
	}

	var parsed struct {
		Artifacts []artifactEntry `json:"artifacts"`
	}
	if err := json.Unmarshal(resp.Body, &parsed); err != nil {
		return nil, err
	}
	return parsed.Artifacts, nil
}

type walkError struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

type walkResult struct {
	Files     []artifactEntry
	Truncated bool
	// Unvisited counts directories still queued when the cap was hit. It is a
	// floor, not a total: each of those may hold more below it.
	Unvisited int
	// Cap is the listing budget this walk ran under, so the message a caller
	// prints matches the limit actually applied.
	Cap    int
	Errors []walkError
}

// Incomplete reports whether the walk failed to see the whole tree.
func (w walkResult) Incomplete() bool { return w.Truncated || len(w.Errors) > 0 }

// problems describes each reason the walk fell short, for the error a caller
// returns. A partial result that exits zero cannot be told apart from a
// complete one, so every caller of walkArtifacts reports these.
func (w walkResult) problems() []string {
	var out []string
	if n := len(w.Errors); n > 0 {
		out = append(out, fmt.Sprintf("%d director%s could not be listed (likely removed by retention)",
			n, map[bool]string{true: "y", false: "ies"}[n == 1]))
	}
	if w.Truncated {
		out = append(out, fmt.Sprintf("the walk stopped at the %d-listing cap with at least %d director%s unvisited (raise %s if the store is genuinely this large)",
			w.Cap, w.Unvisited, map[bool]string{true: "y", false: "ies"}[w.Unvisited == 1], envMaxWalkRequests))
	}
	return out
}

// walkArtifacts expands the tree under root breadth-first. A failure listing
// the root is fatal — nothing was found and the scope is likely wrong. A
// failure deeper in is recorded and the walk continues: retention deletes
// subtrees mid-walk, and losing every file found so far to one expired
// directory is worse than reporting the gap.
func walkArtifacts(c *client.Client, scope, scopeID, root string) (walkResult, error) {
	var res walkResult

	// The root is listed outside the loop so that "the root failed" is a
	// property of the call rather than of a loop counter, which a later
	// `continue` could silently turn into a recoverable error.
	rootEntries, err := listArtifactPath(c, scope, scopeID, root)
	if err != nil {
		return walkResult{}, err
	}

	res.Cap = walkRequestCap()
	seen := map[string]bool{root: true}
	var queue []string
	requests := 1

	collect := func(entries []artifactEntry) {
		for _, e := range entries {
			if !e.IsDirectory {
				res.Files = append(res.Files, e)
				continue
			}
			// A store that reports a directory already walked would
			// otherwise loop until the request cap, re-emitting its files
			// on every pass.
			if seen[e.Path] {
				continue
			}
			seen[e.Path] = true
			queue = append(queue, e.Path)
		}
	}
	collect(rootEntries)

	for len(queue) > 0 {
		if requests >= res.Cap {
			res.Truncated = true
			res.Unvisited = len(queue)
			break
		}

		dir := queue[0]
		queue = queue[1:]

		entries, err := listArtifactPath(c, scope, scopeID, dir)
		requests++
		if err != nil {
			// 401, 403 and 429 apply to the organization, not to this
			// directory: every remaining listing would answer the same way,
			// so continuing multiplies one refusal into hundreds. A 429 is
			// worse than the others — the budget is windowed, so the extra
			// requests push the window out and delay recovery. Anything else
			// is per-directory (a subtree removed by retention) or transient,
			// and losing the whole walk to it is the bug this tolerates.
			if orgWideRefusal(statusOf(err)) {
				return walkResult{}, err
			}
			res.Errors = append(res.Errors, walkError{Path: dir, Error: err.Error()})
			continue
		}
		collect(entries)
	}

	sort.Slice(res.Files, func(a, b int) bool { return res.Files[a].Path < res.Files[b].Path })
	return res, nil
}

// incompleteWalk turns an incomplete walk into the error its command returns,
// so a caller that saw only part of the tree never exits zero.
func incompleteWalk(walk walkResult, what string) error {
	if !walk.Incomplete() {
		return nil
	}
	return fmt.Errorf("incomplete %s: %s. Retrying will not recover the missing directories — narrow it with --path",
		what, strings.Join(walk.problems(), "; "))
}

func safeRelativePath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty path")
	}
	if strings.HasPrefix(path, "/") || strings.Contains(path, "\\") {
		return "", fmt.Errorf("unsafe path %q", path)
	}
	segments := strings.Split(path, "/")
	for _, seg := range segments {
		// Windows strips trailing dots and spaces from path components, so
		// ".. " and "..." both reach the filesystem as "..". Each segment is
		// judged by what the OS would make of it, not by its literal text —
		// a purely lexical check (filepath.Rel, filepath.IsLocal) sees ".. "
		// as an ordinary name and misses the escape.
		if t := strings.TrimRight(seg, ". "); t == "" || t == "." || t == ".." {
			return "", fmt.Errorf("unsafe path %q", path)
		}
	}

	rel := filepath.Join(segments...)
	// IsLocal adds the platform's own rules on top: drive-relative paths
	// like "C:x" and reserved device names such as NUL or CON.
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("unsafe path %q", path)
	}
	return rel, nil
}

// containedIn reports whether dest lands inside root. The segment rules in
// safeRelativePath should make this unreachable; it is kept as a second
// barrier because the cost of being wrong here is writing outside the
// directory the user named.
func containedIn(root, dest string) bool {
	rel, err := filepath.Rel(root, dest)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func gunzip(data []byte) ([]byte, error) {
	if len(data) < 2 || data[0] != 0x1f || data[1] != 0x8b {
		return data, nil
	}
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	defer r.Close()
	decompressed, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	return decompressed, nil
}

// gzipNameSuffixes are the path endings that already advertise gzip
// compression, so their bytes are written to disk untouched.
var gzipNameSuffixes = []string{".gz", ".gzip", ".tgz", ".svgz"}

func pathAdvertisesGzip(p string) bool {
	lower := strings.ToLower(p)
	for _, suffix := range gzipNameSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

func downloadArtifact(c *client.Client, scope, scopeID, path string) ([]byte, error) {
	params := url.Values{}
	params.Set("scope", scope)
	params.Set("scope_id", scopeID)
	params.Set("path", path)
	params.Set("method", "GET")

	resp, err := c.ListWithParams("artifacts/signed_url", params)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, &apiError{Status: resp.StatusCode, Body: string(resp.Body)}
	}

	var signedResp struct {
		Items []struct {
			Path string `json:"path"`
			URL  string `json:"url"`
		} `json:"items"`
	}
	if err := json.Unmarshal(resp.Body, &signedResp); err != nil || len(signedResp.Items) == 0 {
		return nil, fmt.Errorf("artifact not found")
	}

	dlResp, err := c.GetExternal(signedResp.Items[0].URL)
	if err != nil {
		return nil, err
	}
	if dlResp.StatusCode != 200 {
		return nil, &apiError{Status: dlResp.StatusCode}
	}
	return dlResp.Body, nil
}

var (
	artifactScope     string
	artifactScopeID   string
	artifactListPath  string
	artifactRecursive bool
)

var artifactListCmd = &cobra.Command{
	Use:   "list",
	Short: "List artifacts for a job, workflow, or project",
	Long: `List artifacts for a job, workflow, or project.

Without --path, lists the top level of the artifact store. Entries with
"is_directory": true are directories — pass their "path" back via --path to
list one level deeper, or use --recursive to expand the whole tree at once.
--recursive returns files only; directories are implied by the file paths.`,
	Example: `  sem-ai artifact list --scope jobs --id <job-id>
  sem-ai artifact list --scope workflows --id <workflow-id>
  sem-ai artifact list --scope workflows --id <workflow-id> --path test-results
  sem-ai artifact list --scope workflows --id <workflow-id> --recursive
  sem-ai artifact list --scope projects --id <project-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured; run 'sem-ai connect' first")
		}
		if artifactScope == "" || artifactScopeID == "" {
			output.Error("invalid_args", "--scope and --id are required", 1)
			return fmt.Errorf("--scope and --id are required")
		}

		c := client.New()

		if artifactRecursive {
			walk, err := walkArtifacts(c, artifactScope, artifactScopeID, artifactListPath)
			if err != nil {
				reportArtifactError(err)
				return err
			}
			result := map[string]any{"artifacts": walk.Files, "count": len(walk.Files)}
			if walk.Truncated {
				result["truncated"] = true
				result["unvisited_directories"] = walk.Unvisited
			}
			if len(walk.Errors) > 0 {
				result["errors"] = walk.Errors
			}
			if walk.Incomplete() {
				result["complete"] = false
				result["message"] = strings.Join(walk.problems(), "; ")
			}
			output.Result(result)
			return incompleteWalk(walk, "listing")
		}

		entries, err := listArtifactPath(c, artifactScope, artifactScopeID, artifactListPath)
		if err != nil {
			reportArtifactError(err)
			return err
		}
		output.Result(map[string]any{"artifacts": entries})
		return nil
	},
}

var (
	artifactGetScope   string
	artifactGetScopeID string
	artifactGetPath    string
	artifactGetOutput  string
)

var artifactGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Download an artifact via signed URL",
	Example: `  sem-ai artifact get --scope jobs --id <job-id> --path agent/job_logs.txt.gz
  sem-ai artifact get --scope jobs --id <job-id> --path test-results/junit.json --output ./results.json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured; run 'sem-ai connect' first")
		}
		if artifactGetScope == "" || artifactGetScopeID == "" || artifactGetPath == "" {
			output.Error("invalid_args", "--scope, --id, and --path are required", 1)
			return fmt.Errorf("--scope, --id, and --path are required")
		}

		c := client.New()

		data, err := downloadArtifact(c, artifactGetScope, artifactGetScopeID, artifactGetPath)
		if err != nil {
			// Checked before the directory probe: that probe hits the same
			// gate and would just spend a second request to learn nothing.
			if artifactsAPIDisabled(err) {
				reportArtifactError(err)
				return err
			}
			if entries, listErr := listArtifactPath(c, artifactGetScope, artifactGetScopeID, artifactGetPath); listErr == nil && len(entries) > 0 {
				msg := fmt.Sprintf("%q is a directory (%d entries); use 'artifact list --path %s' to browse it or 'artifact pull --path %s' to download it",
					artifactGetPath, len(entries), artifactGetPath, artifactGetPath)
				output.Error("is_directory", msg, 400)
				return fmt.Errorf("%s", msg)
			}
			status := statusOf(err)
			if status == 0 {
				status = 1
			}
			output.Error("download_error", err.Error(), status)
			return err
		}

		// A file on disk must not contradict its own name, so an --output
		// path advertising gzip keeps the bytes as they arrived. Streaming to
		// stdout has no name to contradict and is meant to be read, so it
		// always inflates.
		if artifactGetOutput == "" || !pathAdvertisesGzip(artifactGetOutput) {
			data, err = gunzip(data)
			if err != nil {
				output.Error("decompress_error", fmt.Sprintf("%s: %s", artifactGetPath, err), 1)
				return err
			}
		}

		if artifactGetOutput != "" {
			dir := filepath.Dir(artifactGetOutput)
			if dir != "." {
				os.MkdirAll(dir, 0755)
			}
			if err := os.WriteFile(artifactGetOutput, data, 0644); err != nil {
				output.Error("write_error", err.Error(), 1)
				return err
			}
			output.Result(map[string]any{
				"status": "downloaded",
				"path":   artifactGetPath,
				"output": artifactGetOutput,
				"size":   len(data),
			})
		} else {
			os.Stdout.Write(data)
		}
		return nil
	},
}

var (
	artifactPullScope     string
	artifactPullScopeID   string
	artifactPullPath      string
	artifactPullOutputDir string
	artifactPullForce     bool
	artifactPullDryRun    bool
)

type pullResult struct {
	Path   string `json:"path"`
	Output string `json:"output,omitempty"`
	Size   int64  `json:"size"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

var artifactPullCmd = &cobra.Command{
	Use:   "pull",
	Short: "Bulk download every artifact under a path, preserving the directory tree",
	Long: `Bulk download artifacts for a job, workflow, or project.

Walks the artifact store recursively from --path (the whole store by default)
and writes every file it finds under --output-dir, mirroring the remote
directory layout. Existing files are skipped unless --force is passed.

Without --output-dir the tree is written to ./artifacts-<id>, never straight
into the working directory.

Artifacts stored gzipped but not named with a gzip suffix (.gz, .tgz, .svgz,
…) are decompressed on write; gzip-named paths are written byte-for-byte.`,
	Example: `  sem-ai artifact pull --scope workflows --id <workflow-id> --output-dir ./artifacts
  sem-ai artifact pull --scope workflows --id <workflow-id> --path test-results
  sem-ai artifact pull --scope jobs --id <job-id> --output-dir ./job --force
  sem-ai artifact pull --scope workflows --id <workflow-id> --dry-run`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured; run 'sem-ai connect' first")
		}
		if artifactPullScope == "" || artifactPullScopeID == "" {
			output.Error("invalid_args", "--scope and --id are required", 1)
			return fmt.Errorf("--scope and --id are required")
		}

		if artifactPullOutputDir == "" {
			artifactPullOutputDir = "artifacts-" + artifactPullScopeID
		}

		c := client.New()

		walk, err := walkArtifacts(c, artifactPullScope, artifactPullScopeID, artifactPullPath)
		if err != nil {
			reportArtifactError(err)
			return err
		}
		files := walk.Files

		if artifactPullDryRun {
			result := map[string]any{
				"status":     "dry_run",
				"output_dir": artifactPullOutputDir,
				"count":      len(files),
				"artifacts":  files,
			}
			if walk.Truncated {
				result["truncated"] = true
				result["unvisited_directories"] = walk.Unvisited
			}
			if len(walk.Errors) > 0 {
				result["errors"] = walk.Errors
			}
			if walk.Incomplete() {
				result["complete"] = false
				result["status"] = "dry_run_partial"
				result["message"] = strings.Join(walk.problems(), "; ")
			}
			output.Result(result)
			return incompleteWalk(walk, "dry run")
		}

		results := pullFiles(c, files)

		var downloaded, skipped, failed int
		var totalBytes int64
		for _, r := range results {
			switch r.Status {
			case "downloaded":
				downloaded++
				totalBytes += r.Size
			case "skipped":
				skipped++
			default:
				failed++
			}
		}

		result := map[string]any{
			"status":     "pulled",
			"output_dir": artifactPullOutputDir,
			"downloaded": downloaded,
			"skipped":    skipped,
			"failed":     failed,
			"bytes":      totalBytes,
			"artifacts":  results,
		}
		if walk.Truncated {
			result["truncated"] = true
			result["unvisited_directories"] = walk.Unvisited
		}
		if len(walk.Errors) > 0 {
			result["errors"] = walk.Errors
		}

		problems := walk.problems()
		if failed > 0 {
			problems = append(problems, fmt.Sprintf("%d of %d artifacts failed to download", failed, len(results)))
		}
		if len(problems) > 0 {
			result["complete"] = false
			result["status"] = "pulled_partial"
			result["message"] = strings.Join(problems, "; ")
		}
		output.Result(result)

		if len(problems) == 0 {
			return nil
		}

		msg := fmt.Sprintf("incomplete pull: %s; %d file%s written to %s",
			strings.Join(problems, "; "), downloaded,
			map[bool]string{true: "", false: "s"}[downloaded == 1], artifactPullOutputDir)
		if walk.Incomplete() {
			// Said plainly so an agent does not loop: the directories are
			// gone, and asking again returns the same answer.
			msg += ". Retrying will not recover the missing directories — narrow the pull with --path"
		}
		return fmt.Errorf("%s", msg)
	},
}

func pullFiles(c *client.Client, files []artifactEntry) []pullResult {
	results := make([]pullResult, len(files))
	sem := make(chan struct{}, pullConcurrency)
	var wg sync.WaitGroup

	for i, f := range files {
		// Acquired before the goroutine is spawned: acquiring inside would
		// create one goroutine per file up front and only then throttle
		// them, which for a large tree is a lot of stacks doing nothing.
		sem <- struct{}{}
		wg.Add(1)
		go func(i int, f artifactEntry) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = pullOne(c, f)
		}(i, f)
	}
	wg.Wait()
	return results
}

func pullOne(c *client.Client, f artifactEntry) pullResult {
	rel, err := safeRelativePath(f.Path)
	if err != nil {
		return pullResult{Path: f.Path, Status: "failed", Error: err.Error()}
	}
	dest := filepath.Join(artifactPullOutputDir, rel)
	if !containedIn(artifactPullOutputDir, dest) {
		return pullResult{Path: f.Path, Status: "failed", Error: fmt.Sprintf("unsafe path %q", f.Path)}
	}

	if !artifactPullForce {
		// Lstat, not Stat: a symlink is reported as itself rather than as
		// whatever it points at. Only a real file already holding this
		// artifact's bytes is a legitimate skip — a directory or a device at
		// the destination is a conflict, and calling it "skipped" would exit
		// zero while nothing local holds the content.
		if info, statErr := os.Lstat(dest); statErr == nil {
			if !info.Mode().IsRegular() {
				return pullResult{
					Path:   f.Path,
					Status: "failed",
					Error:  fmt.Sprintf("%s exists and is not a regular file (%s)", dest, info.Mode().Type()),
				}
			}
			return pullResult{Path: f.Path, Output: dest, Status: "skipped"}
		}
	}

	data, err := downloadArtifact(c, artifactPullScope, artifactPullScopeID, f.Path)
	if err != nil {
		return pullResult{Path: f.Path, Status: "failed", Error: err.Error()}
	}
	if !pathAdvertisesGzip(f.Path) {
		data, err = gunzip(data)
		if err != nil {
			return pullResult{Path: f.Path, Status: "failed", Error: err.Error()}
		}
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return pullResult{Path: f.Path, Status: "failed", Error: err.Error()}
	}
	if err := writeFileAtomic(dest, data); err != nil {
		return pullResult{Path: f.Path, Status: "failed", Error: err.Error()}
	}
	return pullResult{Path: f.Path, Output: dest, Size: int64(len(data)), Status: "downloaded"}
}

// writeFileAtomic writes through a temporary file in the destination
// directory and renames it into place, so an interrupted pull never leaves a
// truncated file behind for the next run's skip check to accept as complete.
//
// The temporary file is opened rather than taken from os.CreateTemp for two
// reasons: CreateTemp forces 0600 and would need a Chmod that ignores the
// caller's umask, and a pattern derived from the artifact name overflows
// NAME_MAX for long names that a plain write handles fine.
func writeFileAtomic(dest string, data []byte) error {
	dir := filepath.Dir(dest)

	var tmp *os.File
	var tmpName string
	for attempt := 0; attempt < 100; attempt++ {
		tmpName = filepath.Join(dir, fmt.Sprintf("%s%d-%d", pullTempPrefix, os.Getpid(), rand.Uint64()))
		f, err := os.OpenFile(tmpName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err == nil {
			tmp = f
			break
		}
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
	}
	if tmp == nil {
		return fmt.Errorf("could not create a temporary file in %s", dir)
	}

	var renamed bool
	defer func() {
		if !renamed {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return err
	}
	renamed = true
	return nil
}

func init() {
	artifactListCmd.Flags().StringVar(&artifactScope, "scope", "", "artifact scope: jobs, workflows, or projects")
	artifactListCmd.Flags().StringVar(&artifactScopeID, "id", "", "scope ID (job/workflow/project UUID)")
	artifactListCmd.Flags().StringVar(&artifactListPath, "path", "", "directory within the scope to list (default: top level)")
	artifactListCmd.Flags().BoolVarP(&artifactRecursive, "recursive", "R", false, "expand every subdirectory and return files only")

	artifactGetCmd.Flags().StringVar(&artifactGetScope, "scope", "", "artifact scope: jobs, workflows, or projects")
	artifactGetCmd.Flags().StringVar(&artifactGetScopeID, "id", "", "scope ID (job/workflow/project UUID)")
	artifactGetCmd.Flags().StringVar(&artifactGetPath, "path", "", "artifact path within scope")
	artifactGetCmd.Flags().StringVar(&artifactGetOutput, "output", "", "output file path (default: stdout)")

	artifactPullCmd.Flags().StringVar(&artifactPullScope, "scope", "", "artifact scope: jobs, workflows, or projects")
	artifactPullCmd.Flags().StringVar(&artifactPullScopeID, "id", "", "scope ID (job/workflow/project UUID)")
	artifactPullCmd.Flags().StringVar(&artifactPullPath, "path", "", "directory within the scope to pull (default: everything)")
	artifactPullCmd.Flags().StringVarP(&artifactPullOutputDir, "output-dir", "o", "", "local directory to write artifacts into (default: ./artifacts-<id>)")
	artifactPullCmd.Flags().BoolVar(&artifactPullForce, "force", false, "overwrite existing local files instead of skipping them")
	artifactPullCmd.Flags().BoolVar(&artifactPullDryRun, "dry-run", false, "list what would be downloaded without writing files")

	artifactCmd.AddCommand(artifactListCmd)
	artifactCmd.AddCommand(artifactGetCmd)
	artifactCmd.AddCommand(artifactPullCmd)
	rootCmd.AddCommand(artifactCmd)
}
