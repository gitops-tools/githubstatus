package mcpserver

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bigkevmcd/githubstatus"
)

const (
	// defaultHistoryLimit is how many incidents get_github_incident_history
	// returns when no limit is given.
	defaultHistoryLimit = 10
	// maxHistoryLimit is the most incidents the API returns.
	maxHistoryLimit = 50
	// summaryRunes is roughly how much of each incident's latest update is
	// returned unless details are requested. Resolved incidents' updates are
	// often multi-paragraph root-cause write-ups, which would otherwise
	// dominate the output.
	summaryRunes = 300
)

// HistoryInput is the input to get_github_incident_history.
type HistoryInput struct {
	Since     time.Time `json:"since,omitzero" jsonschema:"only include incidents still ongoing at or after this time (RFC 3339)"`
	Until     time.Time `json:"until,omitzero" jsonschema:"only include incidents that started at or before this time (RFC 3339)"`
	Component string    `json:"component,omitempty" jsonschema:"only include incidents affecting this component, e.g. Actions (case-insensitive)"`
	MinImpact string    `json:"min_impact,omitempty" jsonschema:"only include incidents with at least this impact"`
	Limit     int       `json:"limit,omitempty" jsonschema:"maximum number of incidents to return, newest first (default 10)"`
	// IncludeDetails returns each latest update in full.
	IncludeDetails bool `json:"include_details,omitempty" jsonschema:"return the full text of each incident's latest update (often a long root-cause analysis) instead of the first few sentences"`
}

// HistoryOutput is the result of get_github_incident_history.
type HistoryOutput struct {
	Incidents       []HistoricalIncident `json:"incidents" jsonschema:"matching incidents, newest first, up to limit"`
	TotalMatching   int                  `json:"total_matching" jsonschema:"how many incidents matched the filters; if more than were returned, raise limit or narrow the filters to see the rest"`
	HistoryStartsAt *time.Time           `json:"history_starts_at,omitempty" jsonschema:"start of the oldest incident available; nothing earlier can be reported, so absence of incidents before this time means unknown, not healthy"`
}

// HistoricalIncident is an incident from the recent history.
type HistoricalIncident struct {
	Name         string     `json:"name"`
	Status       string     `json:"status" jsonschema:"investigating, identified, monitoring, resolved or postmortem"`
	Impact       string     `json:"impact" jsonschema:"none, minor, major or critical"`
	URL          string     `json:"url,omitempty" jsonschema:"link to the incident"`
	StartedAt    time.Time  `json:"started_at"`
	ResolvedAt   *time.Time `json:"resolved_at,omitempty" jsonschema:"when the incident was resolved; absent if ongoing"`
	Duration     string     `json:"duration,omitempty" jsonschema:"how long the incident lasted, or has lasted so far if ongoing, e.g. 1h16m"`
	Components   []string   `json:"components,omitempty" jsonschema:"names of the affected components, when recorded"`
	LatestUpdate string     `json:"latest_update,omitempty" jsonschema:"the most recent update; shortened (ending in …) unless include_details is set, see url for the full text"`
}

func historyInputSchema() *jsonschema.Schema {
	s, err := jsonschema.For[HistoryInput](nil)
	if err != nil {
		panic(err)
	}

	s.Properties["since"].Format = "date-time"
	s.Properties["until"].Format = "date-time"
	s.Properties["min_impact"].Enum = indicatorThresholds
	s.Properties["limit"].Minimum = new(float64(1))
	s.Properties["limit"].Maximum = new(float64(maxHistoryLimit))

	return s
}

func (h *handlers) getHistory(ctx context.Context, _ *mcp.CallToolRequest, in HistoryInput) (*mcp.CallToolResult, HistoryOutput, error) {
	if !in.Since.IsZero() && !in.Until.IsZero() && in.Until.Before(in.Since) {
		return nil, HistoryOutput{}, fmt.Errorf("until (%s) is before since (%s)", in.Until.Format(time.RFC3339), in.Since.Format(time.RFC3339))
	}

	if in.MinImpact != "" {
		if _, err := githubstatus.ParseIndicator(in.MinImpact); err != nil {
			return nil, HistoryOutput{}, err
		}
	}

	out, err := h.fetchHistory(ctx, in)

	return nil, out, err
}

// fetchHistory builds the get_github_incident_history output, used by both
// the tool and the resource.
func (h *handlers) fetchHistory(ctx context.Context, in HistoryInput) (HistoryOutput, error) {
	list, err := h.incidents.get(ctx)
	if err != nil {
		return HistoryOutput{}, fetchError(err)
	}

	now := h.now()
	minImpact := githubstatus.Impact(in.MinImpact).Severity()

	limit := in.Limit
	if limit <= 0 {
		limit = defaultHistoryLimit
	}

	out := HistoryOutput{Incidents: []HistoricalIncident{}}
	for _, i := range list.Incidents {
		start := i.Start()
		if out.HistoryStartsAt == nil || start.Before(*out.HistoryStartsAt) {
			out.HistoryStartsAt = &start
		}

		end := now
		if i.ResolvedAt != nil {
			end = *i.ResolvedAt
		}

		if !in.Since.IsZero() && end.Before(in.Since) {
			continue
		}

		if !in.Until.IsZero() && start.After(in.Until) {
			continue
		}

		if i.Impact.Severity() < minImpact {
			continue
		}

		names := componentNames(i.Components)
		if in.Component != "" && !slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(n, in.Component) }) {
			continue
		}

		out.TotalMatching++
		if len(out.Incidents) >= limit {
			continue // keep counting matches, and scanning for HistoryStartsAt
		}

		update := latestUpdate(i)
		if !in.IncludeDetails {
			update = shorten(update, summaryRunes)
		}

		out.Incidents = append(out.Incidents, HistoricalIncident{
			Name: i.Name, Status: string(i.Status), Impact: string(i.Impact), URL: i.Shortlink,
			StartedAt: start, ResolvedAt: i.ResolvedAt, Duration: formatDuration(end.Sub(start)),
			Components: names, LatestUpdate: update,
		})
	}

	return out, nil
}

// formatDuration rounds to the minute and formats compactly, e.g. "45m",
// "2h" or "1h16m".
func formatDuration(d time.Duration) string {
	d = max(d.Round(time.Minute), 0)

	h, m := int(d.Hours()), int(d.Minutes())%60
	switch {
	case h == 0:
		return fmt.Sprintf("%dm", m)
	case m == 0:
		return fmt.Sprintf("%dh", h)
	default:
		return fmt.Sprintf("%dh%dm", h, m)
	}
}

// shorten returns s cut to at most about n runes, preferring to end at a
// sentence or word boundary, with "…" appended if anything was removed.
func shorten(s string, n int) string {
	s = strings.TrimSpace(s)

	r := []rune(s)
	if len(r) <= n {
		return s
	}

	cut := string(r[:n])
	// Prefer the last sentence end, then the last space, if either keeps at
	// least half the text.
	if i := strings.LastIndexAny(cut, ".!?\n"); i >= len(cut)/2 {
		return strings.TrimSpace(cut[:i+1]) + " …"
	}

	if i := strings.LastIndexAny(cut, " \t"); i >= len(cut)/2 {
		cut = cut[:i]
	}

	return strings.TrimRight(cut, " \t,;:-") + "…"
}
