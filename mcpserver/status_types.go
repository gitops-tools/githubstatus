package mcpserver

import "time"

// StatusInput is the (empty) input to get_github_status.
type StatusInput struct{}

// StatusOutput is the result of get_github_status.
type StatusOutput struct {
	Indicator             string        `json:"indicator" jsonschema:"overall indicator: none, minor, major, critical or maintenance"`
	Description           string        `json:"description" jsonschema:"human-readable overall status, e.g. All Systems Operational"`
	UpdatedAt             time.Time     `json:"updated_at" jsonschema:"when the status page was last updated"`
	URL                   string        `json:"url" jsonschema:"URL of the status page"`
	Components            []Component   `json:"components" jsonschema:"status of each GitHub component, including groups"`
	Incidents             []Incident    `json:"incidents,omitempty" jsonschema:"unresolved incidents"`
	ScheduledMaintenances []Maintenance `json:"scheduled_maintenances,omitempty" jsonschema:"upcoming or in-progress planned maintenance"`
}

// Component is a GitHub component and its status.
type Component struct {
	Name        string   `json:"name"`
	Status      string   `json:"status" jsonschema:"operational, degraded_performance, partial_outage, major_outage or under_maintenance"`
	Description string   `json:"description,omitempty"`
	IsGroup     bool     `json:"is_group,omitempty" jsonschema:"true if this component is a group whose status summarises its members"`
	Members     []string `json:"members,omitempty" jsonschema:"for groups, the names of the member components"`
	Group       string   `json:"group,omitempty" jsonschema:"name of the group this component belongs to, if any"`
}
