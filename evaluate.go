package githubstatus

import (
	"errors"
	"fmt"
	"maps"
	"slices"
)

// Thresholds define when GitHub should be considered unhealthy.
type Thresholds struct {
	// Overall is the minimum overall indicator that counts as unhealthy,
	// e.g. IndicatorMajor. Leave empty to disable the overall check.
	Overall Indicator
	// Components maps a component name (see the Component* constants) to the
	// minimum status that counts as unhealthy, e.g. PartialOutage.
	Components map[string]ComponentStatus
	// FailOnMaintenance treats planned maintenance as unhealthy. It applies
	// to the overall status (whether or not Overall is set) and to every
	// component listed in Components.
	FailOnMaintenance bool
}

// Validate reports an error if any threshold can never be exceeded, such as
// a misspelled value, "none" or "operational". Without this check, such a
// threshold would match every non-healthy status.
//
// The returned error wraps one *ParseError per invalid threshold.
func (t Thresholds) Validate() error {
	var errs []error
	if t.Overall != "" && t.Overall.Severity() == 0 {
		errs = append(errs, &ParseError{Kind: "indicator", Value: string(t.Overall)})
	}

	for _, name := range slices.Sorted(maps.Keys(t.Components)) {
		if st := t.Components[name]; st.Severity() == 0 {
			errs = append(errs, &ParseError{Kind: "component status", Value: string(st), Component: name})
		}
	}

	return errors.Join(errs...)
}

// Reason explains why a Violation was reported.
type Reason string

const (
	// ReasonSeverity means the observed status met or exceeded the threshold.
	ReasonSeverity Reason = "severity"
	// ReasonMaintenance means planned maintenance was in progress and
	// Thresholds.FailOnMaintenance was set.
	ReasonMaintenance Reason = "maintenance"
)

// Violation describes a single threshold that was met or exceeded.
type Violation struct {
	// Component is the component name, or empty for the overall status.
	Component string
	// Reason is why the violation was reported.
	Reason Reason

	// Indicator is the observed overall indicator. Set only when Component
	// is empty.
	Indicator Indicator
	// IndicatorThreshold is the configured overall threshold. It may be
	// empty for maintenance violations when no Overall threshold was set.
	IndicatorThreshold Indicator

	// Status is the observed component status. Set only when Component is
	// not empty.
	Status ComponentStatus
	// StatusThreshold is the configured threshold for the component.
	StatusThreshold ComponentStatus
}

// IsOverall reports whether the violation concerns the overall status rather
// than a single component.
func (v Violation) IsOverall() bool {
	return v.Component == ""
}

func (v Violation) String() string {
	name := v.Component
	if v.IsOverall() {
		name = "Overall"
	}

	switch {
	case v.Reason == ReasonMaintenance:
		return name + " (under maintenance)"
	case v.IsOverall():
		return fmt.Sprintf("%s (%s >= %s)", name, v.Indicator, v.IndicatorThreshold)
	default:
		return fmt.Sprintf("%s (%s >= %s)", name, v.Status, v.StatusThreshold)
	}
}

// Result is the outcome of evaluating a Summary against Thresholds.
type Result struct {
	// Violations lists every threshold that was met or exceeded: the
	// overall status first, then components in the order the API returns
	// them.
	Violations []Violation
	// MissingComponents lists, in sorted order, names in
	// Thresholds.Components that did not appear in the summary. This
	// usually means a typo or a component that GitHub has renamed, so the
	// threshold was never checked. Missing components do not affect
	// Healthy; decide for yourself whether to treat them as failures.
	MissingComponents []string
}

// Healthy reports whether no thresholds were violated.
func (r Result) Healthy() bool {
	return len(r.Violations) == 0
}

// Evaluate checks the summary against the thresholds.
//
// It returns an error, and no result, if t fails Validate.
func (s *Summary) Evaluate(t Thresholds) (Result, error) {
	if err := t.Validate(); err != nil {
		return Result{}, err
	}

	var r Result

	ind := s.Status.Indicator
	if t.Overall != "" && ind.Severity() >= t.Overall.Severity() {
		r.Violations = append(r.Violations, Violation{
			Reason: ReasonSeverity, Indicator: ind, IndicatorThreshold: t.Overall,
		})
	} else if t.FailOnMaintenance && ind.IsMaintenance() {
		r.Violations = append(r.Violations, Violation{
			Reason: ReasonMaintenance, Indicator: ind, IndicatorThreshold: t.Overall,
		})
	}

	found := make(map[string]bool, len(t.Components))
	for _, c := range s.Components {
		th, ok := t.Components[c.Name]
		if !ok {
			continue
		}

		found[c.Name] = true
		if c.Status.Severity() >= th.Severity() {
			r.Violations = append(r.Violations, Violation{
				Component: c.Name, Reason: ReasonSeverity, Status: c.Status, StatusThreshold: th,
			})
		} else if t.FailOnMaintenance && c.Status.IsMaintenance() {
			r.Violations = append(r.Violations, Violation{
				Component: c.Name, Reason: ReasonMaintenance, Status: c.Status, StatusThreshold: th,
			})
		}
	}

	for _, name := range slices.Sorted(maps.Keys(t.Components)) {
		if !found[name] {
			r.MissingComponents = append(r.MissingComponents, name)
		}
	}

	return r, nil
}

// Component returns the component with the given name, if present.
func (s *Summary) Component(name string) (Component, bool) {
	for _, c := range s.Components {
		if c.Name == name {
			return c, true
		}
	}

	return Component{}, false
}
