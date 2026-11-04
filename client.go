// Package githubstatus queries the public githubstatus.com (Atlassian
// Statuspage) API and evaluates GitHub's health against thresholds.
package githubstatus

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// DefaultBaseURL is the base URL of the GitHub Statuspage API.
const DefaultBaseURL = "https://www.githubstatus.com/api/v2"

const defaultUserAgent = "github.com/bigkevmcd/githubstatus"

// Client fetches data from a Statuspage v2 API. The zero value is ready to
// use and talks to githubstatus.com.
type Client struct {
	// HTTPClient is used for requests. Defaults to a client with a 10s
	// timeout.
	HTTPClient *http.Client
	// BaseURL defaults to DefaultBaseURL. Any Statuspage-hosted page works,
	// e.g. "https://status.npmjs.org/api/v2".
	BaseURL string
	// UserAgent is sent with each request.
	UserAgent string
}

var defaultHTTPClient = &http.Client{Timeout: 10 * time.Second}

const (
	// maxResponseBytes caps how much of a successful response is decoded.
	// GitHub's summary.json is typically well under 100 KiB.
	maxResponseBytes = 10 << 20
	// maxErrorBodyBytes caps how much of an error response is kept in
	// HTTPError.Body.
	maxErrorBodyBytes = 1 << 10
	// maxDrainBytes caps how much of an error response is read and discarded
	// so the connection can be reused.
	maxDrainBytes = 64 << 10
)

// HTTPError is returned when the API responds with a non-200 status.
type HTTPError struct {
	URL        string
	StatusCode int
	// RetryAfter is the delay requested by the Retry-After header (as
	// seconds or an HTTP date), or zero if the header is absent or invalid.
	// It is typically set on 429 and 503 responses.
	RetryAfter time.Duration
	// Body holds up to the first 1 KiB of the response body, for debugging.
	Body string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("githubstatus: GET %s: unexpected status %d", e.URL, e.StatusCode)
}

// Status fetches the overall status (status.json).
func (c *Client) Status(ctx context.Context) (*Status, error) {
	var s Status
	if err := c.get(ctx, "status.json", &s); err != nil {
		return nil, err
	}

	return &s, nil
}

// Summary fetches the overall status, components, unresolved incidents and
// scheduled maintenances (summary.json).
func (c *Client) Summary(ctx context.Context) (*Summary, error) {
	var s Summary
	if err := c.get(ctx, "summary.json", &s); err != nil {
		return nil, err
	}

	return &s, nil
}

// Incidents fetches the 50 most recent incidents, resolved or not
// (incidents.json). Incidents are ordered newest first.
func (c *Client) Incidents(ctx context.Context) (*IncidentList, error) {
	var l IncidentList
	if err := c.get(ctx, "incidents.json", &l); err != nil {
		return nil, err
	}

	return &l, nil
}

func (c *Client) get(ctx context.Context, path string, v any) error {
	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}

	u, err := url.JoinPath(base, path)
	if err != nil {
		return fmt.Errorf("githubstatus: invalid base URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("githubstatus: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	ua := c.UserAgent
	if ua == "" {
		ua = defaultUserAgent
	}

	req.Header.Set("User-Agent", ua)

	hc := c.HTTPClient
	if hc == nil {
		hc = defaultHTTPClient
	}

	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("githubstatus: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainBytes))

		return &HTTPError{
			URL:        u,
			StatusCode: resp.StatusCode,
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
			Body:       string(body),
		}
	}

	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(v); err != nil {
		return fmt.Errorf("githubstatus: decoding %s: %w", path, err)
	}

	return nil
}

// parseRetryAfter parses a Retry-After header value, which is either a
// number of seconds or an HTTP date. It returns zero for missing, invalid or
// past values.
func parseRetryAfter(v string, now time.Time) time.Duration {
	if v == "" {
		return 0
	}

	if secs, err := strconv.Atoi(v); err == nil {
		return time.Duration(max(secs, 0)) * time.Second
	}

	if t, err := http.ParseTime(v); err == nil {
		return max(t.Sub(now), 0)
	}

	return 0
}
