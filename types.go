package githubstatus

import (
	"fmt"
)

// Indicator is the overall ("rollup") status of GitHub as reported by
// githubstatus.com.
//
// Values that are not recognised are preserved as-is and have a severity of
// zero, so new values introduced by the API will not break decoding.
type Indicator string

const (
	// IndicatorNone means all monitored systems are operational.
	IndicatorNone Indicator = "none"
	// IndicatorMinor means a minor service issue is affecting GitHub.
	IndicatorMinor Indicator = "minor"
	// IndicatorMajor means a major service issue is affecting GitHub.
	IndicatorMajor Indicator = "major"
	// IndicatorCritical means a critical service issue is affecting GitHub.
	IndicatorCritical Indicator = "critical"
	// IndicatorMaintenance means planned maintenance is in progress.
	IndicatorMaintenance Indicator = "maintenance"
)

// Severity returns an ordinal for comparing indicators.
//
// "none", "maintenance" and unknown values are all 0; use IsMaintenance to
// detect planned maintenance separately.
func (i Indicator) Severity() int {
	switch i {
	case IndicatorMinor:
		return 1
	case IndicatorMajor:
		return 2
	case IndicatorCritical:
		return 3
	default:
		return 0
	}
}

// IsMaintenance reports whether the indicator signals planned maintenance.
func (i Indicator) IsMaintenance() bool {
	return i == IndicatorMaintenance
}

// ParseIndicator parses a threshold value such as "major".
// Only values with a non-zero severity are accepted.
func ParseIndicator(s string) (Indicator, error) {
	i := Indicator(s)
	if i.Severity() == 0 {
		return "", &ParseError{Kind: "indicator", Value: s}
	}

	return i, nil
}

// ComponentStatus is the status of a single GitHub component.
type ComponentStatus string

const (
	// Operational means a component is functioning normally.
	Operational ComponentStatus = "operational"
	// DegradedPerformance means a component is slower or less reliable than usual.
	DegradedPerformance ComponentStatus = "degraded_performance"
	// PartialOutage means a component is partially unavailable.
	PartialOutage ComponentStatus = "partial_outage"
	// MajorOutage means a component is unavailable to most users.
	MajorOutage ComponentStatus = "major_outage"
	// UnderMaintenance means planned maintenance is in progress for a component.
	UnderMaintenance ComponentStatus = "under_maintenance"
)

// Severity returns an ordinal for comparing component statuses.
//
// "operational", "under_maintenance" and unknown values are all 0; use
// IsMaintenance to detect planned maintenance separately.
func (c ComponentStatus) Severity() int {
	switch c {
	case DegradedPerformance:
		return 1
	case PartialOutage:
		return 2
	case MajorOutage:
		return 3
	default:
		return 0
	}
}

// IsMaintenance reports whether the component is under planned maintenance.
func (c ComponentStatus) IsMaintenance() bool {
	return c == UnderMaintenance
}

// ParseComponentStatus parses a threshold value such as "partial_outage".
// Only values with a non-zero severity are accepted.
func ParseComponentStatus(s string) (ComponentStatus, error) {
	c := ComponentStatus(s)
	if c.Severity() == 0 {
		return "", &ParseError{Kind: "component status", Value: s}
	}

	return c, nil
}

// ParseError is returned when a threshold value cannot be parsed, or when
// Thresholds.Validate finds a threshold that can never be exceeded.
type ParseError struct {
	// Kind is "indicator" or "component status".
	Kind string
	// Value is the rejected value.
	Value string
	// Component is the component the threshold was configured for, if any.
	Component string
}

func (e *ParseError) Error() string {
	msg := fmt.Sprintf("githubstatus: invalid %s threshold %q", e.Kind, e.Value)
	if e.Component != "" {
		msg += fmt.Sprintf(" for component %q", e.Component)
	}

	return msg
}

// IncidentStatus is the status of an incident, scheduled maintenance or
// incident update.
type IncidentStatus string

// Statuses used by incidents.
const (
	IncidentInvestigating IncidentStatus = "investigating"
	IncidentIdentified    IncidentStatus = "identified"
	IncidentMonitoring    IncidentStatus = "monitoring"
	IncidentResolved      IncidentStatus = "resolved"
	IncidentPostmortem    IncidentStatus = "postmortem"
)

// Statuses used by scheduled maintenances.
const (
	MaintenanceScheduled  IncidentStatus = "scheduled"
	MaintenanceInProgress IncidentStatus = "in_progress"
	MaintenanceVerifying  IncidentStatus = "verifying"
	MaintenanceCompleted  IncidentStatus = "completed"
)

// Impact is the impact of an incident or scheduled maintenance.
type Impact string

const (
	// ImpactNone means the incident has no user-facing impact.
	ImpactNone Impact = "none"
	// ImpactMinor means the incident has a limited user-facing impact.
	ImpactMinor Impact = "minor"
	// ImpactMajor means the incident has a significant user-facing impact.
	ImpactMajor Impact = "major"
	// ImpactCritical means the incident has a critical user-facing impact.
	ImpactCritical Impact = "critical"
	// ImpactMaintenance means the incident represents planned maintenance.
	ImpactMaintenance Impact = "maintenance"
)

// Severity returns an ordinal for comparing impacts.
//
// "none", "maintenance" and unknown values are all 0; use IsMaintenance to
// detect planned maintenance separately.
func (i Impact) Severity() int {
	switch i {
	case ImpactMinor:
		return 1
	case ImpactMajor:
		return 2
	case ImpactCritical:
		return 3
	default:
		return 0
	}
}

// IsMaintenance reports whether the impact signals planned maintenance.
func (i Impact) IsMaintenance() bool {
	return i == ImpactMaintenance
}

// Well-known GitHub component names, as they appear in the API.
const (
	ComponentGit          = "Git Operations"
	ComponentAPI          = "API Requests"
	ComponentWebhooks     = "Webhooks"
	ComponentIssues       = "Issues"
	ComponentPullRequests = "Pull Requests"
	ComponentActions      = "Actions"
	ComponentPackages     = "Packages"
	ComponentPages        = "Pages"
	ComponentCodespaces   = "Codespaces"
	ComponentCopilot      = "Copilot"
)
