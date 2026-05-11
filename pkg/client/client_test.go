package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// newTestClient creates a Client that points to the given test server host.
func newTestClient(host, token string) *Client {
	// Strip the scheme — Client always prepends https://, but httptest uses http.
	// We need to override the httpClient to handle this. Easier: inject a
	// transport that rewrites https → http for tests.
	c := &Client{
		token:      token,
		host:       host,
		apiVersion: "v1alpha",
		httpClient: &http.Client{
			Transport: &httpToHTTPSRewriter{inner: http.DefaultTransport, target: host},
		},
	}
	return c
}

// httpToHTTPSRewriter rewrites https://target/… → http://target/… so httptest
// servers (which are http) accept the requests.
type httpToHTTPSRewriter struct {
	inner  http.RoundTripper
	target string
}

func (t *httpToHTTPSRewriter) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme == "https" && req.URL.Host == t.target {
		// Clone the request with http scheme
		cloned := req.Clone(req.Context())
		cloned.URL.Scheme = "http"
		return t.inner.RoundTrip(cloned)
	}
	return t.inner.RoundTrip(req)
}

// ---- Auth header tests -------------------------------------------------------

func TestAuthHeaderSet(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	c := newTestClient(host, "mytoken")
	_, err := c.Get("projects", "abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "Token mytoken"
	if gotAuth != want {
		t.Errorf("Authorization header = %q, want %q", gotAuth, want)
	}
}

func TestUserAgentHeader(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	c := newTestClient(host, "tok")
	_, _ = c.Get("things", "id")
	if gotUA == "" {
		t.Error("User-Agent header not set")
	}
}

// ---- OrgID header tests -------------------------------------------------------

func TestOrgIDHeaderSentWhenSet(t *testing.T) {
	var gotOrgID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotOrgID = r.Header.Get("x-semaphore-org-id")
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	c := newTestClient(host, "tok")
	c.SetOrgID("org-123")

	_, err := c.Get("projects", "p1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotOrgID != "org-123" {
		t.Errorf("x-semaphore-org-id = %q, want %q", gotOrgID, "org-123")
	}
}

func TestOrgIDHeaderNotSentWhenEmpty(t *testing.T) {
	var gotOrgID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotOrgID = r.Header.Get("x-semaphore-org-id")
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	c := newTestClient(host, "tok")
	// orgID not set

	_, err := c.Get("projects", "p1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotOrgID != "" {
		t.Errorf("x-semaphore-org-id should not be set, got %q", gotOrgID)
	}
}

// ---- GetExternal does NOT send auth headers ------------------------------------

func TestGetExternalNoAuthHeader(t *testing.T) {
	var gotAuth, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(200)
		w.Write([]byte("external content"))
	}))
	defer srv.Close()

	c := &Client{
		token:      "secret",
		host:       "example.com",
		httpClient: srv.Client(),
	}
	resp, err := c.GetExternal(srv.URL + "/some/path")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if gotAuth != "" {
		t.Errorf("Authorization header should NOT be sent to external URLs, got %q", gotAuth)
	}
	// User-Agent comes from http.DefaultTransport, not from our code — acceptable
	_ = gotUA
}

// ---- Retry logic ---------------------------------------------------------------

func TestRetryOn5xx(t *testing.T) {
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&callCount, 1)
		if n <= 3 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	c := newTestClient(host, "tok")

	resp, err := c.Get("projects", "p1")
	if err != nil {
		t.Fatalf("unexpected error after retries: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if callCount < 4 {
		t.Errorf("expected at least 4 calls (3 failures + 1 success), got %d", callCount)
	}
}

func TestNoRetryOn4xx(t *testing.T) {
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.WriteHeader(404)
		w.Write([]byte(`{"error":"not found"}`))
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	c := newTestClient(host, "tok")

	resp, err := c.Get("projects", "unknown")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
	if callCount != 1 {
		t.Errorf("expected exactly 1 call for 404, got %d", callCount)
	}
}

func TestRetryOn429(t *testing.T) {
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&callCount, 1)
		if n == 1 {
			w.WriteHeader(429)
			return
		}
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	c := newTestClient(host, "tok")

	resp, err := c.Get("items", "x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("expected 200 after retry, got %d", resp.StatusCode)
	}
	if callCount < 2 {
		t.Errorf("expected at least 2 calls, got %d", callCount)
	}
}

func TestRetryExhausted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`internal error`))
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	c := newTestClient(host, "tok")

	_, err := c.Get("projects", "p1")
	if err == nil {
		t.Error("expected error after retries exhausted, got nil")
	}
	if !strings.Contains(err.Error(), "retries") {
		t.Errorf("error message should mention retries, got: %v", err)
	}
}

// ---- ListAll pagination ---------------------------------------------------------

func TestListAllPagination(t *testing.T) {
	pageData := map[int][][]byte{
		1: {[]byte(`{"id":"a"}`), []byte(`{"id":"b"}`)},
		2: {[]byte(`{"id":"c"}`), []byte(`{"id":"d"}`)},
		3: {[]byte(`{"id":"e"}`)},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pageStr := r.URL.Query().Get("page")
		page := 1
		fmt.Sscanf(pageStr, "%d", &page)

		items := pageData[page]
		if items == nil {
			w.WriteHeader(404)
			return
		}

		// Marshal as JSON array
		result := "["
		for i, item := range items {
			if i > 0 {
				result += ","
			}
			result += string(item)
		}
		result += "]"

		if page < len(pageData) {
			w.Header().Set("x-has-more", "true")
		}
		w.WriteHeader(200)
		w.Write([]byte(result))
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	c := newTestClient(host, "tok")

	all, err := c.ListAll("items", url.Values{})
	if err != nil {
		t.Fatalf("ListAll error: %v", err)
	}

	totalExpected := 5 // 2 + 2 + 1
	if len(all) != totalExpected {
		t.Errorf("expected %d items, got %d", totalExpected, len(all))
	}
}

func TestListAllSinglePage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No x-has-more header → single page
		w.WriteHeader(200)
		w.Write([]byte(`[{"id":"x"},{"id":"y"}]`))
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	c := newTestClient(host, "tok")

	all, err := c.ListAll("things", url.Values{})
	if err != nil {
		t.Fatalf("ListAll error: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("expected 2, got %d", len(all))
	}
}

func TestListAllAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	c := newTestClient(host, "tok")

	_, err := c.ListAll("things", url.Values{})
	if err == nil {
		t.Error("expected error for non-200 response")
	}
}

// ---- ResolveOrgID ----------------------------------------------------------------

func TestResolveOrgIDFromMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "projects") {
			w.WriteHeader(200)
			resp := `[{"metadata":{"org_id":"org-meta-456","id":"p1"},"spec":{}}]`
			w.Write([]byte(resp))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	c := newTestClient(host, "tok")

	err := c.ResolveOrgID()
	if err != nil {
		t.Fatalf("ResolveOrgID error: %v", err)
	}
	if c.orgID != "org-meta-456" {
		t.Errorf("orgID = %q, want %q", c.orgID, "org-meta-456")
	}
}

func TestResolveOrgIDFromSpec(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "projects") {
			w.WriteHeader(200)
			// metadata.org_id empty → fall back to spec.organization_id
			resp := `[{"metadata":{"org_id":"","id":"p1"},"spec":{"organization_id":"org-spec-789"}}]`
			w.Write([]byte(resp))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	c := newTestClient(host, "tok")

	if err := c.ResolveOrgID(); err != nil {
		t.Fatalf("ResolveOrgID error: %v", err)
	}
	if c.orgID != "org-spec-789" {
		t.Errorf("orgID = %q, want %q", c.orgID, "org-spec-789")
	}
}

func TestResolveOrgIDCached(t *testing.T) {
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.WriteHeader(200)
		w.Write([]byte(`[{"metadata":{"org_id":"org-abc"},"spec":{}}]`))
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	c := newTestClient(host, "tok")

	_ = c.ResolveOrgID()
	_ = c.ResolveOrgID() // second call should be a no-op

	if callCount != 1 {
		t.Errorf("expected 1 API call (cached on second), got %d", callCount)
	}
}

// ---- URL construction -----------------------------------------------------------

func TestGetURLConstruction(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	c := newTestClient(host, "tok")
	_, _ = c.Get("projects", "my-proj")

	want := "/api/v1alpha/projects/my-proj"
	if gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
}

func TestListWithParamsURL(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(200)
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	c := newTestClient(host, "tok")

	params := url.Values{}
	params.Set("project_id", "proj-abc")
	params.Set("branch_name", "main")
	_, _ = c.ListWithParams("plumber-workflows", params)

	if !strings.Contains(gotQuery, "project_id=proj-abc") {
		t.Errorf("query %q does not contain project_id=proj-abc", gotQuery)
	}
	if !strings.Contains(gotQuery, "branch_name=main") {
		t.Errorf("query %q does not contain branch_name=main", gotQuery)
	}
}

// ---- Response body capture -------------------------------------------------------

func TestResponseBodyAndStatus(t *testing.T) {
	payload := `{"id":"abc","name":"test-project"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(payload))
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	c := newTestClient(host, "tok")

	resp, err := c.Get("projects", "abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	var result map[string]string
	if err := json.Unmarshal(resp.Body, &result); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if result["id"] != "abc" {
		t.Errorf("id = %q, want %q", result["id"], "abc")
	}
}

// ---- Table-driven HTTP method tests ---------------------------------------------

func TestHTTPMethods(t *testing.T) {
	tests := []struct {
		name   string
		fn     func(c *Client, srv *httptest.Server) (*Response, error)
		wantMethod string
	}{
		{
			name: "List GET",
			fn: func(c *Client, srv *httptest.Server) (*Response, error) {
				return c.List("projects")
			},
			wantMethod: "GET",
		},
		{
			name: "Post POST",
			fn: func(c *Client, srv *httptest.Server) (*Response, error) {
				return c.Post("projects", []byte(`{}`))
			},
			wantMethod: "POST",
		},
		{
			name: "Delete DELETE",
			fn: func(c *Client, srv *httptest.Server) (*Response, error) {
				return c.Delete("projects", "p1")
			},
			wantMethod: "DELETE",
		},
		{
			name: "Patch PATCH",
			fn: func(c *Client, srv *httptest.Server) (*Response, error) {
				return c.Patch("projects", "p1", []byte(`{}`))
			},
			wantMethod: "PATCH",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotMethod string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				w.WriteHeader(200)
				w.Write([]byte(`{}`))
			}))
			defer srv.Close()

			host := strings.TrimPrefix(srv.URL, "http://")
			c := newTestClient(host, "tok")
			_, err := tc.fn(c, srv)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotMethod != tc.wantMethod {
				t.Errorf("HTTP method = %q, want %q", gotMethod, tc.wantMethod)
			}
		})
	}
}
