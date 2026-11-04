package githubstatus

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// newFixtureServer serves files from testdata, mapping request paths to
// fixture files.
func newFixtureServer(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}

		if got := r.Header.Get("User-Agent"); got != defaultUserAgent {
			t.Errorf("User-Agent = %q, want %q", got, defaultUserAgent)
		}

		b, err := os.ReadFile(filepath.Join("testdata", file))
		if err != nil {
			// t.Fatal must not be called outside the test goroutine.
			t.Error(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)

	return srv
}

func TestClientStatus(t *testing.T) {
	srv := newFixtureServer(t, map[string]string{"/api/v2/status.json": "data-status.json"})
	c := &Client{BaseURL: srv.URL + "/api/v2"}

	s, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if s.Status.Indicator != IndicatorMinor {
		t.Errorf("Indicator = %q, want %q", s.Status.Indicator, IndicatorMinor)
	}

	if s.Status.Description != "Minor Service Outage" {
		t.Errorf("Description = %q", s.Status.Description)
	}

	if s.Page.Name != "GitHub" {
		t.Errorf("Page.Name = %q", s.Page.Name)
	}

	want := time.Date(2020, 5, 22, 20, 50, 20, 457000000, time.UTC)
	if !s.Page.UpdatedAt.Equal(want) {
		t.Errorf("Page.UpdatedAt = %v, want %v", s.Page.UpdatedAt, want)
	}
}

func TestClientSummary(t *testing.T) {
	srv := newFixtureServer(t, map[string]string{"/summary.json": "data-summary.json"})
	c := &Client{BaseURL: srv.URL + "/"} // trailing slash is tolerated

	s, err := c.Summary(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if s.Status.Indicator != IndicatorMinor {
		t.Errorf("Indicator = %q", s.Status.Indicator)
	}

	if len(s.Components) != 9 {
		t.Errorf("len(Components) = %d, want 9", len(s.Components))
	}

	if c, ok := s.Component(ComponentGit); !ok || c.Status != Operational {
		t.Errorf("Component(%q) = %+v, %v", ComponentGit, c, ok)
	}

	if len(s.Incidents) != 1 {
		t.Fatalf("len(Incidents) = %d, want 1", len(s.Incidents))
	}

	inc := s.Incidents[0]
	if inc.Impact != ImpactMinor || inc.Status != IncidentInvestigating || inc.Shortlink != "http://stspg.io/g1l6nqnlv29b" || len(inc.Updates) != 6 {
		t.Errorf("unexpected incident: %+v", inc)
	}

	if len(s.ScheduledMaintenances) != 2 {
		t.Errorf("len(ScheduledMaintenances) = %d, want 2", len(s.ScheduledMaintenances))
	}

	sm := s.ScheduledMaintenances[0]
	if sm.ScheduledFor == nil {
		t.Error("ScheduledFor is nil for a scheduled maintenance")
	}

	if sm.Status != MaintenanceScheduled || sm.Impact != ImpactMaintenance {
		t.Errorf("scheduled maintenance Status = %q, Impact = %q", sm.Status, sm.Impact)
	}

	if got := sm.Updates[0].Status; got != MaintenanceScheduled {
		t.Errorf("update Status = %q, want %q", got, MaintenanceScheduled)
	}
}

func TestClientHTTPError(t *testing.T) {
	srv := newFixtureServer(t, nil)
	c := &Client{BaseURL: srv.URL}

	_, err := c.Summary(context.Background())

	httpErr, ok := errors.AsType[*HTTPError](err)
	if !ok {
		t.Fatalf("err = %v, want *HTTPError", err)
	}

	if httpErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d", httpErr.StatusCode)
	}

	if httpErr.URL != srv.URL+"/summary.json" {
		t.Errorf("URL = %q", httpErr.URL)
	}

	if httpErr.RetryAfter != 0 {
		t.Errorf("RetryAfter = %v, want 0", httpErr.RetryAfter)
	}

	if !strings.Contains(httpErr.Body, "404") {
		t.Errorf("Body = %q, want the 404 page", httpErr.Body)
	}
}

func TestClientHTTPErrorRetryAfterAndBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(strings.Repeat("x", 4096)))
	}))
	t.Cleanup(srv.Close)

	_, err := (&Client{BaseURL: srv.URL}).Status(context.Background())

	httpErr, ok := errors.AsType[*HTTPError](err)
	if !ok {
		t.Fatalf("err = %v, want *HTTPError", err)
	}

	if httpErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("StatusCode = %d", httpErr.StatusCode)
	}

	if httpErr.RetryAfter != 2*time.Minute {
		t.Errorf("RetryAfter = %v, want 2m", httpErr.RetryAfter)
	}

	if len(httpErr.Body) != maxErrorBodyBytes {
		t.Errorf("len(Body) = %d, want %d", len(httpErr.Body), maxErrorBodyBytes)
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"0", 0},
		{"30", 30 * time.Second},
		{"-5", 0},
		{"soon", 0},
		{now.Add(90 * time.Second).Format(http.TimeFormat), 90 * time.Second},
		{now.Add(-time.Hour).Format(http.TimeFormat), 0},
	}
	for _, tt := range tests {
		if got := parseRetryAfter(tt.in, now); got != tt.want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestClientResponseTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A syntactically valid prefix padded past the limit, so decoding
		// only fails because the body is truncated.
		_, _ = w.Write([]byte(`{"page":`))
		_, _ = w.Write([]byte(strings.Repeat(" ", maxResponseBytes)))
		_, _ = w.Write([]byte(`{}}`))
	}))
	t.Cleanup(srv.Close)

	_, err := (&Client{BaseURL: srv.URL}).Status(context.Background())
	if err == nil {
		t.Fatal("expected an error for a response over the size limit")
	}
}

func TestClientInvalidBaseURL(t *testing.T) {
	_, err := (&Client{BaseURL: "://bad"}).Status(context.Background())
	if err == nil || !strings.Contains(err.Error(), "invalid base URL") {
		t.Fatalf("err = %v, want invalid base URL error", err)
	}
}

func TestClientInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>not json</html>"))
	}))
	t.Cleanup(srv.Close)

	_, err := (&Client{BaseURL: srv.URL}).Summary(context.Background())
	if err == nil {
		t.Fatal("expected an error decoding invalid JSON")
	}
}

func TestClientContextCancelled(t *testing.T) {
	srv := newFixtureServer(t, map[string]string{"/summary.json": "data-summary.json"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := (&Client{BaseURL: srv.URL}).Summary(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestClientIncidents(t *testing.T) {
	srv := newFixtureServer(t, map[string]string{"/incidents.json": "data-incidents.json"})

	l, err := (&Client{BaseURL: srv.URL}).Incidents(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(l.Incidents) != 7 {
		t.Fatalf("len(Incidents) = %d, want 7", len(l.Incidents))
	}

	open := l.Incidents[1]
	if open.ResolvedAt != nil || open.Impact != ImpactMajor {
		t.Errorf("Incidents[1] ResolvedAt = %v, Impact = %q", open.ResolvedAt, open.Impact)
	}

	var names []string
	for _, c := range open.Components {
		names = append(names, c.Name)
	}

	want := []string{ComponentGit, ComponentAPI, ComponentIssues, ComponentPullRequests, ComponentActions}
	if !slices.Equal(names, want) {
		t.Errorf("Components = %v, want %v", names, want)
	}

	if resolved := l.Incidents[2]; resolved.ResolvedAt == nil || resolved.Status != IncidentResolved {
		t.Errorf("Incidents[2] = %+v, want resolved", resolved)
	}
}
