package githubstatus

import (
	"slices"
	"time"
)

// IncidentList is the response from the incidents.json endpoint.
type IncidentList struct {
	Page      Page       `json:"page"`
	Incidents []Incident `json:"incidents"`
}

// Incident is an incident or a scheduled maintenance.
type Incident struct {
	ID             string           `json:"id"`
	Name           string           `json:"name"`
	Status         IncidentStatus   `json:"status"`
	Impact         Impact           `json:"impact"`
	Shortlink      string           `json:"shortlink"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
	StartedAt      *time.Time       `json:"started_at"`
	MonitoringAt   *time.Time       `json:"monitoring_at"`
	ResolvedAt     *time.Time       `json:"resolved_at"`
	ScheduledFor   *time.Time       `json:"scheduled_for"`
	ScheduledUntil *time.Time       `json:"scheduled_until"`
	Updates        []IncidentUpdate `json:"incident_updates"`
	// Components lists the components affected by the incident, with their
	// statuses at the time the incident data was fetched.
	Components []Component `json:"components"`
}

// Start returns when the incident started, falling back to when it was
// created if the start time is not set.
func (i Incident) Start() time.Time {
	if i.StartedAt != nil {
		return *i.StartedAt
	}

	return i.CreatedAt
}

// LatestUpdate returns the most recently displayed update, if any.
func (i Incident) LatestUpdate() (IncidentUpdate, bool) {
	if len(i.Updates) == 0 {
		return IncidentUpdate{}, false
	}

	return slices.MaxFunc(i.Updates, func(a, b IncidentUpdate) int {
		return a.DisplayAt.Compare(b.DisplayAt)
	}), true
}

// IncidentUpdate is a single update posted to an incident.
type IncidentUpdate struct {
	ID        string         `json:"id"`
	Status    IncidentStatus `json:"status"`
	Body      string         `json:"body"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DisplayAt time.Time      `json:"display_at"`
}
