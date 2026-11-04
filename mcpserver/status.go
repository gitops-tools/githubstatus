package mcpserver

import (
	"context"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bigkevmcd/githubstatus"
)

func (h *handlers) getStatus(ctx context.Context, _ *mcp.CallToolRequest, _ StatusInput) (*mcp.CallToolResult, StatusOutput, error) {
	out, err := h.fetchStatus(ctx)
	return nil, out, err
}

func statusOutput(sum *githubstatus.Summary) StatusOutput {
	out := StatusOutput{
		Indicator:   string(sum.Status.Indicator),
		Description: sum.Status.Description,
		UpdatedAt:   sum.Page.UpdatedAt,
		URL:         sum.Page.URL,
		Components:  components(sum.Components),
	}
	for _, i := range sum.Incidents {
		out.Incidents = append(out.Incidents, Incident{
			Name: i.Name, Status: string(i.Status), Impact: string(i.Impact), URL: i.Shortlink,
			StartedAt: i.Start(), Components: componentNames(i.Components), LatestUpdate: latestUpdate(i),
		})
	}

	for _, m := range sum.ScheduledMaintenances {
		out.ScheduledMaintenances = append(out.ScheduledMaintenances, Maintenance{
			Name: m.Name, Status: string(m.Status), URL: m.Shortlink,
			ScheduledFor: m.ScheduledFor, ScheduledUntil: m.ScheduledUntil,
			Components: componentNames(m.Components), LatestUpdate: latestUpdate(m),
		})
	}

	return out
}

// components converts API components, resolving group membership from IDs
// to names. Groups are included so that every name accepted by
// check_github_health is visible here.
func components(in []githubstatus.Component) []Component {
	names := make(map[string]string, len(in))
	for _, c := range in {
		names[c.ID] = c.Name
	}

	out := make([]Component, 0, len(in))
	for _, c := range in {
		oc := Component{Name: c.Name, Status: string(c.Status), Description: c.Description, IsGroup: c.Group}
		if c.GroupID != "" {
			oc.Group = names[c.GroupID]
		}

		for _, id := range c.Components {
			if n, ok := names[id]; ok {
				oc.Members = append(oc.Members, n)
			}
		}

		out = append(out, oc)
	}

	return out
}

func componentNames(cs []githubstatus.Component) []string {
	var names []string
	for _, c := range cs {
		names = append(names, c.Name)
	}

	return names
}

func latestUpdate(i githubstatus.Incident) string {
	u, _ := i.LatestUpdate()
	return u.Body
}

// CheckHealthInput is the input to check_github_health.
type CheckHealthInput struct {
	Overall           string            `json:"overall,omitempty" jsonschema:"minimum overall indicator that counts as unhealthy; omit to skip the overall check unless no component thresholds are given, in which case major is used"`
	Components        map[string]string `json:"components,omitempty" jsonschema:"map of component or group name (e.g. Actions, Git Operations, API Requests, Pull Requests) to the minimum status that counts as unhealthy; get_github_status lists the names"`
	FailOnMaintenance bool              `json:"fail_on_maintenance,omitempty" jsonschema:"treat planned maintenance as unhealthy"`
}

// CheckHealthOutput is the result of check_github_health.
type CheckHealthOutput struct {
	Healthy           bool        `json:"healthy" jsonschema:"true if no threshold was met or exceeded"`
	Description       string      `json:"description" jsonschema:"human-readable overall status"`
	Violations        []Violation `json:"violations,omitempty" jsonschema:"thresholds that were met or exceeded"`
	MissingComponents []string    `json:"missing_components,omitempty" jsonschema:"requested components that don't exist on the status page, so were not checked; check the spelling against get_github_status"`
}

// Violation is a threshold that was met or exceeded.
type Violation struct {
	Component string `json:"component,omitempty" jsonschema:"component name; omitted for the overall status"`
	Reason    string `json:"reason" jsonschema:"severity or maintenance"`
	Status    string `json:"status" jsonschema:"observed status"`
	Threshold string `json:"threshold,omitempty" jsonschema:"configured threshold"`
	Message   string `json:"message" jsonschema:"human-readable summary"`
}

func checkHealthInputSchema() *jsonschema.Schema {
	s, err := jsonschema.For[CheckHealthInput](nil)
	if err != nil {
		panic(err)
	}

	s.Properties["overall"].Enum = indicatorThresholds
	s.Properties["components"].AdditionalProperties.Enum = []any{
		string(githubstatus.DegradedPerformance), string(githubstatus.PartialOutage), string(githubstatus.MajorOutage),
	}

	return s
}

var indicatorThresholds = []any{
	string(githubstatus.IndicatorMinor), string(githubstatus.IndicatorMajor), string(githubstatus.IndicatorCritical),
}

func (h *handlers) checkHealth(ctx context.Context, _ *mcp.CallToolRequest, in CheckHealthInput) (*mcp.CallToolResult, CheckHealthOutput, error) {
	th := githubstatus.Thresholds{
		Overall:           githubstatus.Indicator(in.Overall),
		FailOnMaintenance: in.FailOnMaintenance,
	}
	if in.Overall == "" && len(in.Components) == 0 {
		th.Overall = githubstatus.IndicatorMajor
	}

	if len(in.Components) > 0 {
		th.Components = make(map[string]githubstatus.ComponentStatus, len(in.Components))
		for name, status := range in.Components {
			th.Components[name] = githubstatus.ComponentStatus(status)
		}
	}
	// Validate before fetching so bad input fails fast.
	if err := th.Validate(); err != nil {
		return nil, CheckHealthOutput{}, err
	}

	sum, err := h.summary.get(ctx)
	if err != nil {
		return nil, CheckHealthOutput{}, fetchError(err)
	}

	res, err := sum.Evaluate(th)
	if err != nil {
		return nil, CheckHealthOutput{}, err
	}

	out := CheckHealthOutput{
		Healthy:           res.Healthy(),
		Description:       sum.Status.Description,
		MissingComponents: res.MissingComponents,
	}
	for _, v := range res.Violations {
		ov := Violation{Component: v.Component, Reason: string(v.Reason), Message: v.String()}
		if v.IsOverall() {
			ov.Status, ov.Threshold = string(v.Indicator), string(v.IndicatorThreshold)
		} else {
			ov.Status, ov.Threshold = string(v.Status), string(v.StatusThreshold)
		}

		out.Violations = append(out.Violations, ov)
	}

	return nil, out, nil
}
