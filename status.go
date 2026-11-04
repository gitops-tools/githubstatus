package githubstatus

import "time"

// Status is the response from the status.json endpoint.
type Status struct {
	Page   Page          `json:"page"`
	Status OverallStatus `json:"status"`
}

// Summary is the response from the summary.json endpoint.
type Summary struct {
	Page                  Page          `json:"page"`
	Status                OverallStatus `json:"status"`
	Components            []Component   `json:"components"`
	Incidents             []Incident    `json:"incidents"`
	ScheduledMaintenances []Incident    `json:"scheduled_maintenances"`
}

// Component is a single GitHub service, e.g. "Actions".
type Component struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Status      ComponentStatus `json:"status"`
	Description string          `json:"description"`
	Position    int             `json:"position"`
	GroupID     string          `json:"group_id"`
	Group       bool            `json:"group"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	// Components holds the IDs of the member components when Group is true.
	// A group's status summarises the statuses of its members.
	Components []string `json:"components"`
}
