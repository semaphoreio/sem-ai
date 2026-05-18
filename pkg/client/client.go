package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/semaphoreio/sem-ai/pkg/config"
)

var UserAgent = "sem-ai/dev"

const (
	maxRetries     = 5
	baseDelay      = 100 * time.Millisecond
	maxDelay       = 2 * time.Second
	defaultTimeout = 30 * time.Second
)

type Client struct {
	token      string
	host       string
	apiVersion string
	httpClient *http.Client
	orgID      string
}

func New() *Client {
	return &Client{
		token:      config.GetToken(),
		host:       config.GetHost(),
		apiVersion: "v1alpha",
		httpClient: &http.Client{Timeout: defaultTimeout},
	}
}

func NewWithConfig(token, host string) *Client {
	return &Client{
		token:      token,
		host:       host,
		apiVersion: "v1alpha",
		httpClient: &http.Client{Timeout: defaultTimeout},
	}
}

// Response wraps API response data.
type Response struct {
	Body       []byte
	StatusCode int
	Headers    http.Header
}

// Get fetches a single resource: GET /api/{version}/{kind}/{id}
func (c *Client) Get(kind, id string) (*Response, error) {
	u := fmt.Sprintf("https://%s/api/%s/%s/%s", c.host, c.apiVersion, kind, id)
	return c.doWithRetry("GET", u, nil)
}

// List fetches a collection: GET /api/{version}/{kind}
func (c *Client) List(kind string) (*Response, error) {
	u := fmt.Sprintf("https://%s/api/%s/%s", c.host, c.apiVersion, kind)
	return c.doWithRetry("GET", u, nil)
}

// ListWithParams fetches with query params, returns headers for pagination.
func (c *Client) ListWithParams(kind string, params url.Values) (*Response, error) {
	u := fmt.Sprintf("https://%s/api/%s/%s?%s", c.host, c.apiVersion, kind, params.Encode())
	return c.doWithRetry("GET", u, nil)
}

// ListAll auto-paginates using link header (rel="next") or x-has-more.
// An optional StopFunc can be passed to halt pagination early — it receives
// each page of raw items and returns true to stop fetching more pages.
func (c *Client) ListAll(kind string, params url.Values, stopFn ...func([]json.RawMessage) bool) ([]json.RawMessage, error) {
	var stop func([]json.RawMessage) bool
	if len(stopFn) > 0 {
		stop = stopFn[0]
	}

	var all []json.RawMessage
	page := 1

	for {
		p := url.Values{}
		for k, v := range params {
			p[k] = v
		}
		p.Set("page", fmt.Sprintf("%d", page))

		resp, err := c.ListWithParams(kind, p)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, string(resp.Body))
		}

		var items []json.RawMessage
		if err := json.Unmarshal(resp.Body, &items); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}
		if len(items) == 0 {
			break
		}
		all = append(all, items...)

		if stop != nil && stop(items) {
			break
		}

		if resp.Headers.Get("x-has-more") == "true" {
			page++
			continue
		}

		if hasNextPage(resp.Headers.Get("Link")) {
			page++
			continue
		}

		break
	}
	return all, nil
}

// hasNextPage checks if a Link header contains rel="next".
func hasNextPage(link string) bool {
	if link == "" {
		return false
	}
	// Simple check — link header format: <url>; rel="next", ...
	for _, part := range strings.Split(link, ",") {
		if strings.Contains(part, `rel="next"`) {
			return true
		}
	}
	return false
}

// Post sends a POST request.
func (c *Client) Post(kind string, body []byte) (*Response, error) {
	u := fmt.Sprintf("https://%s/api/%s/%s", c.host, c.apiVersion, kind)
	return c.doWithRetry("POST", u, body)
}

// PostAction sends POST to /api/{version}/{kind}/{id}/{action}
func (c *Client) PostAction(kind, id, action string, body []byte) (*Response, error) {
	u := fmt.Sprintf("https://%s/api/%s/%s/%s/%s", c.host, c.apiVersion, kind, id, action)
	return c.doWithRetry("POST", u, body)
}

// Patch sends a PATCH request.
func (c *Client) Patch(kind, id string, body []byte) (*Response, error) {
	u := fmt.Sprintf("https://%s/api/%s/%s/%s", c.host, c.apiVersion, kind, id)
	return c.doWithRetry("PATCH", u, body)
}

// Delete sends a DELETE request.
func (c *Client) Delete(kind, id string) (*Response, error) {
	u := fmt.Sprintf("https://%s/api/%s/%s/%s", c.host, c.apiVersion, kind, id)
	return c.doWithRetry("DELETE", u, nil)
}

// DeleteWithParams sends a DELETE request with query parameters.
func (c *Client) DeleteWithParams(kind, id string, params url.Values) (*Response, error) {
	u := fmt.Sprintf("https://%s/api/%s/%s/%s?%s", c.host, c.apiVersion, kind, id, params.Encode())
	return c.doWithRetry("DELETE", u, nil)
}

// PostYAML sends a POST to the YAML validation endpoint.
// The Semaphore v1alpha /yaml endpoint expects a form-encoded body with a
// yaml_definition parameter, not a raw YAML body.
func (c *Client) PostYAML(kind string, yamlBody []byte) (*Response, error) {
	u := fmt.Sprintf("https://%s/api/%s/%s", c.host, c.apiVersion, kind)
	log.Printf("POST (yaml) %s", u)
	form := url.Values{"yaml_definition": {string(yamlBody)}}
	req, err := http.NewRequest("POST", u, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", fmt.Sprintf("Token %s", c.token))
	req.Header.Set("User-Agent", UserAgent)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return &Response{Body: body, StatusCode: resp.StatusCode, Headers: resp.Header}, nil
}

// Versioned methods — for APIs that use different versions (v1beta, v1, v2)

// ResolveOrgID fetches org ID from the projects endpoint and caches it.
// v2 API requires x-semaphore-org-id header.
func (c *Client) ResolveOrgID() error {
	if c.orgID != "" {
		return nil
	}
	resp, err := c.List("projects")
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("failed to list projects: HTTP %d", resp.StatusCode)
	}
	var projects []struct {
		Metadata struct {
			OrgID string `json:"org_id"`
		} `json:"metadata"`
		Spec struct {
			OrgID string `json:"organization_id"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(resp.Body, &projects); err == nil && len(projects) > 0 {
		orgID := projects[0].Metadata.OrgID
		if orgID == "" {
			orgID = projects[0].Spec.OrgID
		}
		c.orgID = orgID
	}
	return nil
}

// SetOrgID manually sets the org ID for v2 API requests.
func (c *Client) SetOrgID(id string) {
	c.orgID = id
}

func (c *Client) ListVersioned(version, kind string) (*Response, error) {
	u := fmt.Sprintf("https://%s/api/%s/%s", c.host, version, kind)
	return c.doWithRetry("GET", u, nil)
}

func (c *Client) ListVersionedWithParams(version, kind string, params url.Values) (*Response, error) {
	u := fmt.Sprintf("https://%s/api/%s/%s?%s", c.host, version, kind, params.Encode())
	return c.doWithRetry("GET", u, nil)
}

func (c *Client) GetVersioned(version, kind, id string) (*Response, error) {
	u := fmt.Sprintf("https://%s/api/%s/%s/%s", c.host, version, kind, id)
	return c.doWithRetry("GET", u, nil)
}

func (c *Client) PostVersioned(version, kind string, body []byte) (*Response, error) {
	u := fmt.Sprintf("https://%s/api/%s/%s", c.host, version, kind)
	return c.doWithRetry("POST", u, body)
}

func (c *Client) PatchVersioned(version, kind, id string, body []byte) (*Response, error) {
	u := fmt.Sprintf("https://%s/api/%s/%s/%s", c.host, version, kind, id)
	return c.doWithRetry("PATCH", u, body)
}

func (c *Client) DeleteVersioned(version, kind, id string) (*Response, error) {
	u := fmt.Sprintf("https://%s/api/%s/%s/%s", c.host, version, kind, id)
	return c.doWithRetry("DELETE", u, nil)
}

// GetRaw fetches a raw URL (for job logs etc).
func (c *Client) GetRaw(rawURL string) (*Response, error) {
	return c.doWithRetry("GET", rawURL, nil)
}

// GetExternal fetches an external URL without auth headers (e.g. signed GCS URLs).
func (c *Client) GetExternal(rawURL string) (*Response, error) {
	log.Printf("GET (external) %s", rawURL)
	resp, err := c.httpClient.Get(rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return &Response{Body: body, StatusCode: resp.StatusCode, Headers: resp.Header}, nil
}

func (c *Client) doWithRetry(method, u string, body []byte) (*Response, error) {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := time.Duration(math.Min(
				float64(baseDelay)*math.Pow(2, float64(attempt-1)),
				float64(maxDelay),
			))
			log.Printf("retry %d/%d after %v", attempt, maxRetries, delay)
			time.Sleep(delay)
		}

		resp, err := c.do(method, u, body)
		if err != nil {
			lastErr = err
			continue
		}

		// Retry on 5xx and 429
		if resp.StatusCode >= 500 || resp.StatusCode == 429 {
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(resp.Body))
			continue
		}

		return resp, nil
	}
	return nil, fmt.Errorf("request failed after %d retries: %w", maxRetries, lastErr)
}

func (c *Client) do(method, u string, body []byte) (*Response, error) {
	log.Printf("%s %s", method, u)

	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, u, bodyReader)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Token %s", c.token))
	req.Header.Set("User-Agent", UserAgent)
	if c.orgID != "" {
		req.Header.Set("x-semaphore-org-id", c.orgID)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	log.Printf("response: %d", resp.StatusCode)

	return &Response{
		Body:       respBody,
		StatusCode: resp.StatusCode,
		Headers:    resp.Header,
	}, nil
}
