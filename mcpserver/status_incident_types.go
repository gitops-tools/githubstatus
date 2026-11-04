package mcpserver

import "time"

// Incident is an unresolved incident.
type Incident struct {
	Name         string    `json:"name"`
	Status       string    `json:"status" jsonschema:"investigating, identified, monitoring, resolved or postmortem"`
	Impact       string    `json:"impact" jsonschema:"none, minor, major or critical"`
	URL          string    `json:"url,omitempty" jsonschema:"link to the incident"`
	StartedAt    time.Time `json:"started_at"`
	Components   []string  `json:"components,omitempty" jsonschema:"names of the affected components"`
	LatestUpdate string    `json:"latest_update,omitempty" jsonschema:"text of the most recent update"`
}

// Maintenance is a scheduled maintenance window.
type Maintenance struct {
	Name           string     `json:"name"`
	Status         string     `json:"status" jsonschema:"scheduled, in_progress, verifying or completed"`
	URL            string     `json:"url,omitempty" jsonschema:"link to the maintenance"`
	ScheduledFor   *time.Time `json:"scheduled_for,omitempty" jsonschema:"when the maintenance starts"`
	ScheduledUntil *time.Time `json:"scheduled_until,omitempty" jsonschema:"when the maintenance ends"`
	Components     []string   `json:"components,omitempty" jsonschema:"names of the affected components"`
	LatestUpdate   string     `json:"latest_update,omitempty" jsonschema:"text of the most recent update"`
}
