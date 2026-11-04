package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Resource URIs.
const (
	StatusURI    = "githubstatus://status"
	IncidentsURI = "githubstatus://incidents"
)

func (h *handlers) registerResources(s *mcp.Server) {
	s.AddResource(&mcp.Resource{
		URI:         StatusURI,
		Name:        "status",
		Title:       "GitHub status",
		Description: "GitHub's current overall status, components, unresolved incidents and scheduled maintenance (same as the get_github_status tool).",
		MIMEType:    "application/json",
	}, jsonResource(func(ctx context.Context) (any, error) { return h.fetchStatus(ctx) }))

	s.AddResource(&mcp.Resource{
		URI:         IncidentsURI,
		Name:        "incidents",
		Title:       "GitHub incident history",
		Description: "The 50 most recent GitHub incidents, newest first, with shortened updates (same as get_github_incident_history with limit 50).",
		MIMEType:    "application/json",
	}, jsonResource(func(ctx context.Context) (any, error) {
		return h.fetchHistory(ctx, HistoryInput{Limit: maxHistoryLimit})
	}))
}

func jsonResource(fetch func(context.Context) (any, error)) mcp.ResourceHandler {
	return func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		v, err := fetch(ctx)
		if err != nil {
			return nil, err
		}

		b, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("encoding %s: %w", req.Params.URI, err)
		}

		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI: req.Params.URI, MIMEType: "application/json", Text: string(b),
		}}}, nil
	}
}

func registerPrompts(s *mcp.Server) {
	s.AddPrompt(&mcp.Prompt{
		Name:  "diagnose_github_problem",
		Title: "Diagnose a GitHub problem",
		Description: "Work out whether GitHub's current or recent status explains a problem, " +
			"such as a failed push, CI run or API call.",
		Arguments: []*mcp.PromptArgument{
			{Name: "problem", Title: "Problem", Description: "What went wrong, e.g. \"git push timed out\" or \"Actions job stuck queued\""},
			{Name: "when", Title: "When", Description: "When it happened, if not just now, e.g. \"around 3am UTC today\""},
		},
	}, diagnosePrompt)
}

func diagnosePrompt(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	problem := strings.TrimSpace(req.Params.Arguments["problem"])
	when := strings.TrimSpace(req.Params.Arguments["when"])

	var b strings.Builder
	if problem != "" {
		fmt.Fprintf(&b, "I ran into a problem with GitHub: %s\n", problem)
	} else {
		_, _ = b.WriteString("I ran into a problem with GitHub.\n")
	}

	if when != "" {
		fmt.Fprintf(&b, "It happened %s.\n", when)
	}

	_, _ = b.WriteString(`
Please work out whether GitHub's status explains it:
1. Use get_github_status to check for current incidents, degraded components and maintenance.
2. If it didn't happen just now, use get_github_incident_history with a time window around when it happened. If the time is before history_starts_at, say the history doesn't go back that far rather than concluding there was no incident.
3. Match the problem to the relevant components (e.g. pushes and clones are Git Operations, CI is Actions).

Then tell me whether a GitHub incident is a likely cause, quoting the relevant incident names, times and links. If GitHub looks healthy, say so and suggest the problem is probably on my side.`)

	return &mcp.GetPromptResult{
		Description: "Diagnose whether GitHub's status explains a problem",
		Messages: []*mcp.PromptMessage{{
			Role:    "user",
			Content: &mcp.TextContent{Text: b.String()},
		}},
	}, nil
}
