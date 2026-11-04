// Package mcpserver exposes GitHub's status as a Model Context Protocol (MCP)
// server.
//
// Tools (all read-only):
//
//   - get_github_status: the overall status, every component, unresolved
//     incidents and scheduled maintenance.
//   - check_github_health: evaluates the current status against thresholds
//     and reports whether GitHub is healthy.
//   - get_github_incident_history: recent incidents, resolved or not,
//     optionally filtered by time window, component and impact.
//
// Resources: githubstatus://status and githubstatus://incidents, with the same
// JSON as the corresponding tools.
//
// Prompts: diagnose_github_problem, which asks the model to work out whether
// GitHub's status explains a problem the user is seeing.
package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bigkevmcd/githubstatus"
)

// Name is the MCP implementation name reported by the server.
const Name = "githubstatus"

// DefaultCacheTTL is how long API responses are reused when
// Options.CacheTTL is zero.
const DefaultCacheTTL = 30 * time.Second

// Options configure the server. The zero value is valid.
type Options struct {
	// Version is reported to clients as the implementation version.
	Version string
	// CacheTTL is how long API responses are reused across tool calls and
	// resource reads. Zero means DefaultCacheTTL; a negative value disables
	// caching.
	CacheTTL time.Duration

	// now is overridden in tests.
	now func() time.Time
}

// New returns an MCP server whose tools fetch data with c. If c is nil, a
// zero-value githubstatus.Client is used. opts may be nil.
func New(c *githubstatus.Client, opts *Options) *mcp.Server {
	if c == nil {
		c = &githubstatus.Client{}
	}

	var o Options
	if opts != nil {
		o = *opts
	}

	if o.CacheTTL == 0 {
		o.CacheTTL = DefaultCacheTTL
	}

	if o.now == nil {
		o.now = time.Now
	}

	s := mcp.NewServer(&mcp.Implementation{Name: Name, Title: "GitHub Status", Version: o.Version}, &mcp.ServerOptions{
		Instructions: "Use these tools to find out whether GitHub (git operations, API, Actions, " +
			"pull requests, etc.) is currently experiencing, or recently experienced, an outage, " +
			"degraded performance or planned maintenance, according to githubstatus.com.",
	})
	h := &handlers{
		now:       o.now,
		summary:   newCache(o.CacheTTL, o.now, c.Summary),
		incidents: newCache(o.CacheTTL, o.now, c.Incidents),
	}
	h.registerTools(s)
	h.registerResources(s)
	registerPrompts(s)

	return s
}

type handlers struct {
	now       func() time.Time
	summary   *cache[*githubstatus.Summary]
	incidents *cache[*githubstatus.IncidentList]
}

var readOnly = &mcp.ToolAnnotations{
	ReadOnlyHint:   true,
	IdempotentHint: true,
	OpenWorldHint:  new(true),
}

func (h *handlers) registerTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:  "get_github_status",
		Title: "Get GitHub status",
		Description: "Returns GitHub's current overall status, the status of every component " +
			"(e.g. Git Operations, API Requests, Actions), unresolved incidents and scheduled " +
			"maintenance from githubstatus.com.",
		Annotations: readOnly,
	}, h.getStatus)

	mcp.AddTool(s, &mcp.Tool{
		Name:  "check_github_health",
		Title: "Check GitHub health",
		Description: "Checks GitHub's current status against thresholds and reports whether it " +
			"is healthy, listing each threshold that was met or exceeded. Use this to decide " +
			"whether GitHub problems could explain a failure, or whether to proceed with work " +
			"that depends on GitHub.",
		InputSchema: checkHealthInputSchema(),
		Annotations: readOnly,
	}, h.checkHealth)

	mcp.AddTool(s, &mcp.Tool{
		Name:  "get_github_incident_history",
		Title: "Get GitHub incident history",
		Description: "Returns recent GitHub incidents, resolved or not, newest first. Filter by " +
			"time window to answer questions like \"was GitHub down at 3am?\". Returns 10 " +
			"incidents by default with shortened updates; use limit and include_details for more. " +
			"Only the 50 most recent incidents are available; history_starts_at says how far back " +
			"that goes.",
		InputSchema: historyInputSchema(),
		Annotations: readOnly,
	}, h.getHistory)
}

// fetchStatus builds the get_github_status output, used by both the tool and
// the resource.
func (h *handlers) fetchStatus(ctx context.Context) (StatusOutput, error) {
	sum, err := h.summary.get(ctx)
	if err != nil {
		return StatusOutput{}, fetchError(err)
	}

	return statusOutput(sum), nil
}

func fetchError(err error) error {
	return fmt.Errorf("fetching GitHub status: %w", err)
}
