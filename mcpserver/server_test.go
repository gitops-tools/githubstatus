package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bigkevmcd/githubstatus"
)

// testNow is the fixed "current time" for tests. The newest incident in
// data-incidents.json started at 09:49:55 that morning and is still open.
var testNow = time.Date(2026, 11, 4, 11, 0, 0, 0, time.UTC)

// fakeAPI serves fixture files for the Statuspage endpoints and counts
// requests to each path.
type fakeAPI struct {
	t      *testing.T
	mu     sync.Mutex
	routes map[string][]byte
	counts map[string]int
}

func newFakeAPI(t *testing.T, fixtures map[string]string) *fakeAPI {
	t.Helper()
	api := &fakeAPI{t: t, routes: map[string][]byte{}, counts: map[string]int{}}

	for path, file := range fixtures {
		b, err := os.ReadFile(filepath.Join("..", "testdata", file))
		if err != nil {
			t.Fatal(err)
		}

		api.routes[path] = b
	}

	return api
}

func (a *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	a.counts[r.URL.Path]++
	b, ok := a.routes[r.URL.Path]
	a.mu.Unlock()

	if !ok {
		http.NotFound(w, r)
		return
	}

	_, _ = w.Write(b)
}

func (a *fakeAPI) set(path string, body []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.routes[path] = body
}

func (a *fakeAPI) count(path string) int {
	a.mu.Lock()
	defer a.mu.Unlock()

	return a.counts[path]
}

func (a *fakeAPI) total() int {
	a.mu.Lock()
	defer a.mu.Unlock()

	n := 0
	for _, c := range a.counts {
		n += c
	}

	return n
}

var defaultFixtures = map[string]string{
	"/summary.json":   "data-summary.json",
	"/incidents.json": "data-incidents.json",
}

// connect starts a server backed by api and returns a connected client
// session. If opts is nil, caching is disabled and the clock is testNow.
func connect(t *testing.T, api *fakeAPI, opts *Options) *mcp.ClientSession {
	t.Helper()

	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)

	if opts == nil {
		opts = &Options{CacheTTL: -1}
	}

	if opts.now == nil {
		opts.now = func() time.Time { return testNow }
	}

	ctx := context.Background()
	server := New(&githubstatus.Client{BaseURL: srv.URL}, opts)
	st, ct := mcp.NewInMemoryTransports()

	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = ss.Close() })

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = cs.Close() })

	return cs
}

// call invokes a tool, fails the test on a tool error, and decodes the
// structured output into out.
func call(t *testing.T, cs *mcp.ClientSession, name string, args any, out any) {
	t.Helper()

	res := callRaw(t, cs, name, args)
	if res.IsError {
		t.Fatalf("%s: tool error: %s", name, textContent(res.Content))
	}

	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}

	if err := json.Unmarshal(b, out); err != nil {
		t.Fatal(err)
	}
}

func callRaw(t *testing.T, cs *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}

	return res
}

// toolError calls a tool that is expected to fail and returns the error
// text, whether it was reported as a protocol error or a tool error.
func toolError(t *testing.T, cs *mcp.ClientSession, name string, args any) string {
	t.Helper()

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	switch {
	case err != nil:
		return err.Error()
	case res.IsError:
		return textContent(res.Content)
	default:
		t.Fatalf("%s: expected an error, got %+v", name, res.StructuredContent)
		return ""
	}
}

func textContent(content []mcp.Content) string {
	var parts []string

	for _, c := range content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}

	return strings.Join(parts, "\n")
}

func TestListTools(t *testing.T) {
	cs := connect(t, newFakeAPI(t, defaultFixtures), nil)

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		got[tool.Name] = tool
	}

	for _, name := range []string{"get_github_status", "check_github_health", "get_github_incident_history"} {
		tool, ok := got[name]
		if !ok {
			t.Fatalf("tool %q not listed", name)
		}

		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("tool %q is not marked read-only", name)
		}

		if tool.OutputSchema == nil {
			t.Errorf("tool %q has no output schema", name)
		}
	}

	// Enums and formats should be advertised so clients know valid inputs.
	for name, wants := range map[string][]string{
		"check_github_health":         {`"critical"`, `"partial_outage"`},
		"get_github_incident_history": {`"date-time"`, `"major"`},
	} {
		schema, err := json.Marshal(got[name].InputSchema)
		if err != nil {
			t.Fatal(err)
		}

		for _, want := range wants {
			if !strings.Contains(string(schema), want) {
				t.Errorf("%s input schema missing %s: %s", name, want, schema)
			}
		}
	}
}

func TestGetStatus(t *testing.T) {
	cs := connect(t, newFakeAPI(t, defaultFixtures), nil)

	var out StatusOutput
	call(t, cs, "get_github_status", map[string]any{}, &out)

	if out.Indicator != "minor" || out.Description != "Minor Service Outage" {
		t.Errorf("overall = %q / %q", out.Indicator, out.Description)
	}

	if out.URL != "https://www.githubstatus.com" {
		t.Errorf("URL = %q", out.URL)
	}

	if len(out.Components) != 9 {
		t.Errorf("len(Components) = %d, want 9", len(out.Components))
	}

	want := Component{
		Name: "Git Operations", Status: "operational",
		Description: "Performance of git clones, pulls, pushes, and associated operations",
	}
	if !reflect.DeepEqual(out.Components[0], want) {
		t.Errorf("Components[0] = %+v, want %+v", out.Components[0], want)
	}

	if len(out.Incidents) != 1 {
		t.Fatalf("len(Incidents) = %d, want 1", len(out.Incidents))
	}

	inc := out.Incidents[0]
	if inc.Status != "investigating" || inc.Impact != "minor" || inc.URL != "http://stspg.io/g1l6nqnlv29b" || inc.LatestUpdate == "" || inc.StartedAt.IsZero() {
		t.Errorf("Incidents[0] = %+v", inc)
	}

	if len(out.ScheduledMaintenances) != 2 {
		t.Fatalf("len(ScheduledMaintenances) = %d, want 2", len(out.ScheduledMaintenances))
	}

	if m := out.ScheduledMaintenances[0]; m.Status != "scheduled" || m.ScheduledFor == nil || m.ScheduledUntil == nil {
		t.Errorf("ScheduledMaintenances[0] = %+v", m)
	}
}

// groupSummary has a group ("Collaboration") containing two components, one
// of which has a partial outage, plus an ungrouped component.
const groupSummary = `{
  "page": {"name": "GitHub", "url": "https://www.githubstatus.com"},
  "status": {"indicator": "minor", "description": "Partially Degraded Service"},
  "components": [
    {"id": "g1", "name": "Collaboration", "status": "partial_outage", "group": true, "group_id": null, "components": ["c1", "c2"]},
    {"id": "c1", "name": "Issues", "status": "partial_outage", "group": false, "group_id": "g1"},
    {"id": "c2", "name": "Pull Requests", "status": "operational", "group": false, "group_id": "g1"},
    {"id": "c3", "name": "Actions", "status": "operational", "group": false, "group_id": null}
  ],
  "incidents": [],
  "scheduled_maintenances": []
}`

func TestGroups(t *testing.T) {
	api := newFakeAPI(t, nil)
	api.set("/summary.json", []byte(groupSummary))
	cs := connect(t, api, nil)

	var status StatusOutput
	call(t, cs, "get_github_status", map[string]any{}, &status)

	want := []Component{
		{Name: "Collaboration", Status: "partial_outage", IsGroup: true, Members: []string{"Issues", "Pull Requests"}},
		{Name: "Issues", Status: "partial_outage", Group: "Collaboration"},
		{Name: "Pull Requests", Status: "operational", Group: "Collaboration"},
		{Name: "Actions", Status: "operational"},
	}
	if !reflect.DeepEqual(status.Components, want) {
		t.Errorf("Components = %+v\nwant %+v", status.Components, want)
	}

	// Every component get_github_status lists, groups included, can be
	// used as a threshold.
	var health CheckHealthOutput
	call(t, cs, "check_github_health", map[string]any{
		"components": map[string]any{"Collaboration": "partial_outage", "Actions": "degraded_performance"},
	}, &health)

	if health.Healthy || len(health.MissingComponents) != 0 || len(health.Violations) != 1 || health.Violations[0].Component != "Collaboration" {
		t.Errorf("check_github_health = %+v", health)
	}
}

func TestCheckHealth(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		args    map[string]any
		want    CheckHealthOutput
	}{
		{
			name:    "defaults to overall major",
			fixture: "data-summary.json",
			args:    map[string]any{},
			want:    CheckHealthOutput{Healthy: true, Description: "Minor Service Outage"},
		},
		{
			name:    "overall minor",
			fixture: "data-summary.json",
			args:    map[string]any{"overall": "minor"},
			want: CheckHealthOutput{
				Description: "Minor Service Outage",
				Violations: []Violation{{
					Reason: "severity", Status: "minor", Threshold: "minor", Message: "Overall (minor >= minor)",
				}},
			},
		},
		{
			name:    "components only skips the default overall check",
			fixture: "data-summary.json",
			args:    map[string]any{"components": map[string]any{"Git Operations": "degraded_performance", "Actionz": "major_outage"}},
			want: CheckHealthOutput{
				Healthy:           true,
				Description:       "Minor Service Outage",
				MissingComponents: []string{"Actionz"},
			},
		},
		{
			name:    "maintenance",
			fixture: "data-summary-2.json",
			args:    map[string]any{"components": map[string]any{"Actions": "major_outage"}, "fail_on_maintenance": true},
			want: CheckHealthOutput{
				Description: "Service Under Maintenance",
				Violations: []Violation{
					{Reason: "maintenance", Status: "maintenance", Message: "Overall (under maintenance)"},
					{Component: "Actions", Reason: "maintenance", Status: "under_maintenance", Threshold: "major_outage", Message: "Actions (under maintenance)"},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := connect(t, newFakeAPI(t, map[string]string{"/summary.json": tt.fixture}), nil)

			var out CheckHealthOutput
			call(t, cs, "check_github_health", tt.args, &out)

			if !reflect.DeepEqual(out, tt.want) {
				t.Errorf("output = %+v\nwant     %+v", out, tt.want)
			}
		})
	}
}

func TestInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		tool string
		args map[string]any
		want string
	}{
		{"bad overall", "check_github_health", map[string]any{"overall": "Major"}, "overall"},
		{"bad component status", "check_github_health", map[string]any{"components": map[string]any{"Actions": "operational"}}, "components"},
		{"unknown field", "check_github_health", map[string]any{"bogus": true}, "bogus"},
		{"bad min_impact", "get_github_incident_history", map[string]any{"min_impact": "none"}, "min_impact"},
		{"bad since", "get_github_incident_history", map[string]any{"since": "yesterday"}, "since"},
		{"until before since", "get_github_incident_history", map[string]any{
			"since": "2026-11-04T00:00:00Z", "until": "2026-11-03T00:00:00Z",
		}, "before since"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newFakeAPI(t, defaultFixtures)

			cs := connect(t, api, nil)
			if msg := toolError(t, cs, tt.tool, tt.args); !strings.Contains(msg, tt.want) {
				t.Errorf("error %q does not mention %q", msg, tt.want)
			}

			if n := api.total(); n != 0 {
				t.Errorf("status API called %d times for invalid input", n)
			}
		})
	}
}

func TestFetchError(t *testing.T) {
	cs := connect(t, newFakeAPI(t, nil), nil)
	for _, name := range []string{"get_github_status", "check_github_health", "get_github_incident_history"} {
		res := callRaw(t, cs, name, map[string]any{})
		if !res.IsError {
			t.Errorf("%s: expected a tool error", name)
		}

		if got := textContent(res.Content); !strings.Contains(got, "unexpected status 404") {
			t.Errorf("%s: error = %q", name, got)
		}
	}
}

func TestCache(t *testing.T) {
	var now atomic.Pointer[time.Time]

	setNow := func(t time.Time) { now.Store(&t) }
	setNow(testNow)

	api := newFakeAPI(t, defaultFixtures)
	cs := connect(t, api, &Options{CacheTTL: time.Minute, now: func() time.Time { return *now.Load() }})

	var (
		status  StatusOutput
		health  CheckHealthOutput
		history HistoryOutput
	)

	call(t, cs, "get_github_status", map[string]any{}, &status)
	call(t, cs, "check_github_health", map[string]any{}, &health)
	call(t, cs, "get_github_incident_history", map[string]any{}, &history)
	call(t, cs, "get_github_incident_history", map[string]any{"min_impact": "major"}, &history)

	if n := api.count("/summary.json"); n != 1 {
		t.Errorf("summary.json fetched %d times within the TTL, want 1", n)
	}

	if n := api.count("/incidents.json"); n != 1 {
		t.Errorf("incidents.json fetched %d times within the TTL, want 1", n)
	}

	setNow(testNow.Add(time.Minute))
	call(t, cs, "get_github_status", map[string]any{}, &status)

	if n := api.count("/summary.json"); n != 2 {
		t.Errorf("summary.json fetched %d times after the TTL expired, want 2", n)
	}
}

func TestCacheDoesNotCacheErrors(t *testing.T) {
	api := newFakeAPI(t, nil)
	cs := connect(t, api, &Options{CacheTTL: time.Hour})

	if res := callRaw(t, cs, "get_github_status", map[string]any{}); !res.IsError {
		t.Fatal("expected a tool error while the API is failing")
	}

	b, err := os.ReadFile(filepath.Join("..", "testdata", "data-summary.json"))
	if err != nil {
		t.Fatal(err)
	}

	api.set("/summary.json", b)

	var out StatusOutput
	call(t, cs, "get_github_status", map[string]any{}, &out)

	if out.Indicator != "minor" {
		t.Errorf("Indicator = %q after recovery", out.Indicator)
	}
}

func TestCacheDisabled(t *testing.T) {
	api := newFakeAPI(t, defaultFixtures)
	cs := connect(t, api, &Options{CacheTTL: -1})

	var out StatusOutput
	for range 3 {
		call(t, cs, "get_github_status", map[string]any{}, &out)
	}

	if n := api.count("/summary.json"); n != 3 {
		t.Errorf("summary.json fetched %d times with caching disabled, want 3", n)
	}
}

func TestHistory(t *testing.T) {
	// Incidents in data-incidents.json, newest first:
	//   0  Copilot Mission    11-04 08:45 - 10:01  minor  (no components)
	//   1  PRs, Git, Issues   11-04 09:49 - open   major  Git, API, Issues, PRs, Actions
	//   2  Git Operations     11-03 20:57 - 22:41  minor  Git Operations
	//   3  API, Git, Copilot  11-03 13:25 - 13:56  minor  API, Issues, Copilot
	//   4  GPT-5.4 Nano       11-02 17:46 - 18:24  minor  Copilot AI Model Providers
	//   5  Dependabot         10-30 20:59 - 23:29  minor  (no components)
	//   6  Disruption         08-26 15:09 - 16:07  minor  (no components)
	names := []string{
		"Increased task-related errors for Copilot Mission Control",
		"Incident with Pull Requests, Git Operations and Issues",
		"Incident with Git Operations",
		"Incident with API, Git, and Copilot requests",
		"Elevated errors on GPT-5.4 Nano",
		"Incident with Dependabot",
		"Disruption with some GitHub services",
	}
	tests := []struct {
		name string
		args map[string]any
		want []int
	}{
		{"no filters", map[string]any{}, []int{0, 1, 2, 3, 4, 5, 6}},
		{"since today", map[string]any{"since": "2026-11-04T00:00:00Z"}, []int{0, 1}},
		{"window catches an incident in progress", map[string]any{
			"since": "2026-11-03T21:00:00Z", "until": "2026-11-03T21:30:00Z",
		}, []int{2}},
		{"until excludes later incidents", map[string]any{"until": "2026-10-31T00:00:00Z"}, []int{5, 6}},
		{"component, case-insensitive", map[string]any{"component": "git operations"}, []int{1, 2}},
		{"min impact", map[string]any{"min_impact": "major"}, []int{1}},
		{"no matches", map[string]any{"since": "2026-09-01T00:00:00Z", "until": "2026-10-01T00:00:00Z"}, []int{}},
	}

	cs := connect(t, newFakeAPI(t, defaultFixtures), nil)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out HistoryOutput
			call(t, cs, "get_github_incident_history", tt.args, &out)

			var got []string
			for _, i := range out.Incidents {
				got = append(got, i.Name)
			}

			var want []string
			for _, i := range tt.want {
				want = append(want, names[i])
			}

			if !slices.Equal(got, want) {
				t.Errorf("incidents = %q\nwant %q", got, want)
			}

			wantStart := time.Date(2026, 8, 26, 15, 9, 3, 568000000, time.UTC)
			if out.HistoryStartsAt == nil || !out.HistoryStartsAt.Equal(wantStart) {
				t.Errorf("HistoryStartsAt = %v, want %v regardless of filters", out.HistoryStartsAt, wantStart)
			}
		})
	}

	t.Run("incident details", func(t *testing.T) {
		var out HistoryOutput
		call(t, cs, "get_github_incident_history", map[string]any{"since": "2026-11-03T00:00:00Z"}, &out)
		open, resolved := out.Incidents[1], out.Incidents[2]
		// 09:49:55 until testNow (11:00) is ongoing for 1h10m.
		if open.ResolvedAt != nil || open.Status != "monitoring" || open.Duration != "1h10m" || open.Impact != "major" || len(open.Components) != 5 || open.URL == "" || open.LatestUpdate == "" {
			t.Errorf("open incident = %+v", open)
		}
		// 20:57:14 to 22:41:03 rounds to 1h44m.
		if resolved.ResolvedAt == nil || resolved.Duration != "1h44m" || resolved.Status != "resolved" ||
			!slices.Equal(resolved.Components, []string{"Git Operations"}) {
			t.Errorf("resolved incident = %+v", resolved)
		}
	})
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "0m"},
		{-time.Minute, "0m"},
		{29 * time.Second, "0m"},
		{45 * time.Minute, "45m"},
		{2 * time.Hour, "2h"},
		{2*time.Hour + 20*time.Second, "2h"},
		{time.Hour + 16*time.Minute, "1h16m"},
		{50*time.Hour + 5*time.Minute, "50h5m"},
	}
	for _, tt := range tests {
		if got := formatDuration(tt.d); got != tt.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestResources(t *testing.T) {
	cs := connect(t, newFakeAPI(t, defaultFixtures), nil)
	ctx := context.Background()

	list, err := cs.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	var uris []string
	for _, r := range list.Resources {
		uris = append(uris, r.URI)
		if r.MIMEType != "application/json" {
			t.Errorf("%s MIMEType = %q", r.URI, r.MIMEType)
		}
	}

	slices.Sort(uris)

	if want := []string{IncidentsURI, StatusURI}; !slices.Equal(uris, want) {
		t.Errorf("resources = %v, want %v", uris, want)
	}

	read := func(uri string, v any) {
		t.Helper()

		res, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
		if err != nil {
			t.Fatalf("ReadResource(%s): %v", uri, err)
		}

		if len(res.Contents) != 1 || res.Contents[0].URI != uri || res.Contents[0].MIMEType != "application/json" {
			t.Fatalf("ReadResource(%s) contents = %+v", uri, res.Contents)
		}

		if err := json.Unmarshal([]byte(res.Contents[0].Text), v); err != nil {
			t.Fatalf("ReadResource(%s): %v", uri, err)
		}
	}

	var status StatusOutput
	read(StatusURI, &status)

	if status.Indicator != "minor" || len(status.Components) != 9 {
		t.Errorf("status resource = %+v", status)
	}

	var history HistoryOutput
	read(IncidentsURI, &history)

	if len(history.Incidents) != 7 || history.HistoryStartsAt == nil {
		t.Errorf("incidents resource has %d incidents", len(history.Incidents))
	}

	if _, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "githubstatus://nope"}); err == nil {
		t.Error("expected an error reading an unknown resource")
	}
}

func TestResourceFetchError(t *testing.T) {
	cs := connect(t, newFakeAPI(t, nil), nil)

	_, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: StatusURI})
	if err == nil || !strings.Contains(err.Error(), "unexpected status 404") {
		t.Errorf("err = %v, want the fetch error", err)
	}
}

func TestPrompts(t *testing.T) {
	cs := connect(t, newFakeAPI(t, nil), nil)
	ctx := context.Background()

	list, err := cs.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(list.Prompts) != 1 || list.Prompts[0].Name != "diagnose_github_problem" {
		t.Fatalf("prompts = %+v", list.Prompts)
	}

	get := func(args map[string]string) string {
		t.Helper()

		res, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "diagnose_github_problem", Arguments: args})
		if err != nil {
			t.Fatal(err)
		}

		if len(res.Messages) != 1 || res.Messages[0].Role != "user" {
			t.Fatalf("messages = %+v", res.Messages)
		}

		return textContent([]mcp.Content{res.Messages[0].Content})
	}

	text := get(map[string]string{"problem": "git push timed out", "when": "around 3am UTC"})
	for _, want := range []string{"git push timed out", "around 3am UTC", "get_github_status", "get_github_incident_history", "history_starts_at"} {
		if !strings.Contains(text, want) {
			t.Errorf("prompt missing %q:\n%s", want, text)
		}
	}

	if text := get(nil); !strings.HasPrefix(text, "I ran into a problem with GitHub.\n") || strings.Contains(text, "It happened") {
		t.Errorf("prompt without arguments:\n%s", text)
	}
}

func TestHistoryLimit(t *testing.T) {
	cs := connect(t, newFakeAPI(t, defaultFixtures), nil)

	tests := []struct {
		name      string
		args      map[string]any
		wantCount int
		wantTotal int
	}{
		{"default limit covers the fixture", map[string]any{}, 7, 7},
		{"limit", map[string]any{"limit": 3}, 3, 7},
		{"limit with filter", map[string]any{"min_impact": "minor", "limit": 2}, 2, 7},
		{"limit above matches", map[string]any{"component": "git operations", "limit": 50}, 2, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out HistoryOutput
			call(t, cs, "get_github_incident_history", tt.args, &out)

			if len(out.Incidents) != tt.wantCount || out.TotalMatching != tt.wantTotal {
				t.Errorf("got %d incidents of %d matching, want %d of %d", len(out.Incidents), out.TotalMatching, tt.wantCount, tt.wantTotal)
			}
			// The limit must not hide how far back the history goes.
			if out.HistoryStartsAt == nil || out.HistoryStartsAt.Year() != 2026 || out.HistoryStartsAt.Month() != time.August {
				t.Errorf("HistoryStartsAt = %v", out.HistoryStartsAt)
			}
		})
	}

	t.Run("newest first", func(t *testing.T) {
		var out HistoryOutput
		call(t, cs, "get_github_incident_history", map[string]any{"limit": 1}, &out)

		if len(out.Incidents) != 1 || !strings.HasPrefix(out.Incidents[0].Name, "Increased task-related errors") {
			t.Errorf("limit 1 returned %+v", out.Incidents)
		}
	})

	for _, limit := range []int{0, -1, 51} {
		if msg := toolError(t, cs, "get_github_incident_history", map[string]any{"limit": limit}); !strings.Contains(msg, "limit") {
			t.Errorf("limit %d: error %q does not mention limit", limit, msg)
		}
	}
}

func TestDefaultHistoryLimit(t *testing.T) {
	// Build 15 incidents so the default limit of 10 applies.
	var incidents []map[string]any

	start := testNow.Add(-24 * time.Hour)
	for i := range 15 {
		s := start.Add(-time.Duration(i) * time.Hour)
		r := s.Add(30 * time.Minute)
		incidents = append(incidents, map[string]any{
			"name": "incident", "status": "resolved", "impact": "minor",
			"created_at": s, "started_at": s, "resolved_at": r,
		})
	}

	b, err := json.Marshal(map[string]any{"incidents": incidents})
	if err != nil {
		t.Fatal(err)
	}

	api := newFakeAPI(t, nil)
	api.set("/incidents.json", b)
	cs := connect(t, api, nil)

	var out HistoryOutput
	call(t, cs, "get_github_incident_history", map[string]any{}, &out)

	if len(out.Incidents) != defaultHistoryLimit || out.TotalMatching != 15 {
		t.Errorf("got %d incidents of %d matching, want %d of 15", len(out.Incidents), out.TotalMatching, defaultHistoryLimit)
	}

	wantStart := start.Add(-14 * time.Hour)
	if out.HistoryStartsAt == nil || !out.HistoryStartsAt.Equal(wantStart) {
		t.Errorf("HistoryStartsAt = %v, want %v (the oldest incident, beyond the limit)", out.HistoryStartsAt, wantStart)
	}

	// The resource returns everything the API has.
	res, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: IncidentsURI})
	if err != nil {
		t.Fatal(err)
	}

	var all HistoryOutput
	if err := json.Unmarshal([]byte(res.Contents[0].Text), &all); err != nil {
		t.Fatal(err)
	}

	if len(all.Incidents) != 15 {
		t.Errorf("incidents resource has %d incidents, want 15", len(all.Incidents))
	}

	if strings.Contains(res.Contents[0].Text, "\n") {
		t.Error("resource JSON is indented; want compact")
	}
}

// longUpdate is a multi-paragraph root-cause write-up, like the ones GitHub
// posts when resolving incidents.
var longUpdate = strings.Repeat("On November 3, GitHub experienced degraded performance for git operations. ", 6) +
	"\n\nThe root cause was a configuration change that increased load on a storage cluster. " +
	strings.Repeat("We have since added safeguards and improved our monitoring and alerting. ", 8)

func TestHistoryDetails(t *testing.T) {
	s := testNow.Add(-2 * time.Hour)

	b, err := json.Marshal(map[string]any{"incidents": []map[string]any{{
		"name": "Incident with Git Operations", "status": "resolved", "impact": "minor",
		"created_at": s, "started_at": s, "resolved_at": s.Add(time.Hour),
		"incident_updates": []map[string]any{{"status": "resolved", "body": longUpdate, "display_at": s.Add(time.Hour)}},
	}}})
	if err != nil {
		t.Fatal(err)
	}

	api := newFakeAPI(t, nil)
	api.set("/incidents.json", b)
	cs := connect(t, api, nil)

	var short HistoryOutput
	call(t, cs, "get_github_incident_history", map[string]any{}, &short)

	got := short.Incidents[0].LatestUpdate
	if n := len([]rune(got)); n > summaryRunes+2 || !strings.HasSuffix(got, "…") {
		t.Errorf("shortened update has %d runes, ends %q", n, got[max(0, len(got)-20):])
	}

	if !strings.HasPrefix(longUpdate, strings.TrimSuffix(strings.TrimSuffix(got, "…"), " ")) {
		t.Errorf("shortened update is not a prefix of the original: %q", got)
	}

	var full HistoryOutput
	call(t, cs, "get_github_incident_history", map[string]any{"include_details": true}, &full)

	if got := full.Incidents[0].LatestUpdate; got != strings.TrimSpace(longUpdate) && got != longUpdate {
		t.Errorf("include_details returned %d bytes, want the full %d", len(got), len(longUpdate))
	}
}

func TestShorten(t *testing.T) {
	tests := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"short text unchanged", "All good.", 20, "All good."},
		{"trims whitespace", "  All good.\n", 20, "All good."},
		{"exact length unchanged", "abcde", 5, "abcde"},
		{"ends at sentence", "First sentence here. Second sentence is long.", 30, "First sentence here. …"},
		{"ends at paragraph", "First paragraph text\n\nSecond paragraph", 28, "First paragraph text …"},
		{"falls back to word", "one two three four five six seven", 20, "one two three four…"},
		{"strips trailing punctuation", "alpha beta gamma, delta epsilon", 18, "alpha beta gamma…"},
		{"no boundary", "abcdefghijklmnopqrstuvwxyz", 10, "abcdefghij…"},
		{"early boundary ignored", "Hi. abcdefghijklmnopqrstuvwxyz", 12, "Hi. abcdefgh…"},
		{"multibyte safe", "ééééééééééééé", 5, "ééééé…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shorten(tt.in, tt.n); got != tt.want {
				t.Errorf("shorten(%q, %d) = %q, want %q", tt.in, tt.n, got, tt.want)
			}
		})
	}
}
