package githubstatus

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func loadSummary(t *testing.T, file string) *Summary {
	t.Helper()

	b, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		t.Fatal(err)
	}

	var s Summary
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}

	return &s
}

func summaryWith(ind Indicator, comps map[string]ComponentStatus) *Summary {
	s := &Summary{Status: OverallStatus{Indicator: ind}}
	for name, st := range comps {
		s.Components = append(s.Components, Component{Name: name, Status: st})
	}

	return s
}

func overallViolation(ind, th Indicator) Violation {
	return Violation{Reason: ReasonSeverity, Indicator: ind, IndicatorThreshold: th}
}

func componentViolation(name string, st, th ComponentStatus) Violation {
	return Violation{Component: name, Reason: ReasonSeverity, Status: st, StatusThreshold: th}
}

func TestEvaluate(t *testing.T) {
	tests := []struct {
		name    string
		summary *Summary
		th      Thresholds
		want    Result
	}{
		{
			name:    "no thresholds",
			summary: summaryWith(IndicatorCritical, map[string]ComponentStatus{ComponentGit: MajorOutage}),
			th:      Thresholds{},
		},
		{
			name:    "overall below threshold",
			summary: summaryWith(IndicatorMinor, nil),
			th:      Thresholds{Overall: IndicatorMajor},
		},
		{
			name:    "overall at threshold",
			summary: summaryWith(IndicatorMajor, nil),
			th:      Thresholds{Overall: IndicatorMajor},
			want:    Result{Violations: []Violation{overallViolation(IndicatorMajor, IndicatorMajor)}},
		},
		{
			name:    "overall above threshold",
			summary: summaryWith(IndicatorCritical, nil),
			th:      Thresholds{Overall: IndicatorMinor},
			want:    Result{Violations: []Violation{overallViolation(IndicatorCritical, IndicatorMinor)}},
		},
		{
			name:    "overall none never violates",
			summary: summaryWith(IndicatorNone, nil),
			th:      Thresholds{Overall: IndicatorMinor},
		},
		{
			name:    "overall maintenance ignored by default",
			summary: summaryWith(IndicatorMaintenance, nil),
			th:      Thresholds{Overall: IndicatorCritical},
		},
		{
			name:    "overall maintenance with FailOnMaintenance",
			summary: summaryWith(IndicatorMaintenance, nil),
			th:      Thresholds{Overall: IndicatorCritical, FailOnMaintenance: true},
			want: Result{Violations: []Violation{
				{Reason: ReasonMaintenance, Indicator: IndicatorMaintenance, IndicatorThreshold: IndicatorCritical},
			}},
		},
		{
			name:    "overall maintenance with FailOnMaintenance but no Overall threshold",
			summary: summaryWith(IndicatorMaintenance, nil),
			th:      Thresholds{FailOnMaintenance: true},
			want: Result{Violations: []Violation{
				{Reason: ReasonMaintenance, Indicator: IndicatorMaintenance},
			}},
		},
		{
			name: "component at and below threshold",
			summary: summaryWith(IndicatorMinor, map[string]ComponentStatus{
				ComponentActions: PartialOutage,
				ComponentGit:     DegradedPerformance,
			}),
			th: Thresholds{Components: map[string]ComponentStatus{
				ComponentActions: PartialOutage,
				ComponentGit:     PartialOutage,
			}},
			want: Result{Violations: []Violation{componentViolation(ComponentActions, PartialOutage, PartialOutage)}},
		},
		{
			name:    "component without threshold ignored",
			summary: summaryWith(IndicatorMajor, map[string]ComponentStatus{ComponentPages: MajorOutage}),
			th:      Thresholds{Components: map[string]ComponentStatus{ComponentPages: MajorOutage, ComponentGit: DegradedPerformance}},
			want: Result{
				Violations:        []Violation{componentViolation(ComponentPages, MajorOutage, MajorOutage)},
				MissingComponents: []string{ComponentGit},
			},
		},
		{
			name:    "unknown component status ignored",
			summary: summaryWith(IndicatorMinor, map[string]ComponentStatus{ComponentGit: "on_fire"}),
			th:      Thresholds{Components: map[string]ComponentStatus{ComponentGit: DegradedPerformance}},
		},
		{
			name:    "missing components are reported sorted",
			summary: summaryWith(IndicatorNone, map[string]ComponentStatus{ComponentGit: Operational}),
			th: Thresholds{Components: map[string]ComponentStatus{
				"Webhookz":   PartialOutage,
				ComponentGit: PartialOutage,
				"Actionz":    PartialOutage,
			}},
			want: Result{MissingComponents: []string{"Actionz", "Webhookz"}},
		},
		{
			name:    "fixture: maintenance ignored by default",
			summary: loadSummary(t, "data-summary-2.json"),
			th: Thresholds{
				Overall:    IndicatorMinor,
				Components: map[string]ComponentStatus{ComponentActions: DegradedPerformance},
			},
		},
		{
			name:    "fixture: maintenance with FailOnMaintenance",
			summary: loadSummary(t, "data-summary-2.json"),
			th: Thresholds{
				Overall:           IndicatorMinor,
				Components:        map[string]ComponentStatus{ComponentActions: MajorOutage},
				FailOnMaintenance: true,
			},
			want: Result{Violations: []Violation{
				{Reason: ReasonMaintenance, Indicator: IndicatorMaintenance, IndicatorThreshold: IndicatorMinor},
				{Component: ComponentActions, Reason: ReasonMaintenance, Status: UnderMaintenance, StatusThreshold: MajorOutage},
			}},
		},
		{
			// A component outage is reported even when the overall indicator
			// says everything is fine and there are no open incidents.
			name:    "component outage without incidents",
			summary: summaryWith(IndicatorNone, map[string]ComponentStatus{ComponentAPI: MajorOutage}),
			th:      Thresholds{Components: map[string]ComponentStatus{ComponentAPI: MajorOutage}},
			want:    Result{Violations: []Violation{componentViolation(ComponentAPI, MajorOutage, MajorOutage)}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.summary.Evaluate(tt.th)
			if err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Evaluate() = %#v, want %#v", got, tt.want)
			}

			if got.Healthy() != (len(tt.want.Violations) == 0) {
				t.Errorf("Healthy() = %v with %d violations", got.Healthy(), len(got.Violations))
			}
		})
	}
}

func TestEvaluateInvalidThresholds(t *testing.T) {
	s := summaryWith(IndicatorMinor, map[string]ComponentStatus{ComponentGit: DegradedPerformance})

	tests := []struct {
		name string
		th   Thresholds
		want []ParseError
	}{
		{
			name: "misspelled overall",
			th:   Thresholds{Overall: "Major"},
			want: []ParseError{{Kind: "indicator", Value: "Major"}},
		},
		{
			name: "overall none",
			th:   Thresholds{Overall: IndicatorNone},
			want: []ParseError{{Kind: "indicator", Value: "none"}},
		},
		{
			name: "overall maintenance",
			th:   Thresholds{Overall: IndicatorMaintenance},
			want: []ParseError{{Kind: "indicator", Value: "maintenance"}},
		},
		{
			name: "multiple component thresholds, sorted",
			th: Thresholds{Components: map[string]ComponentStatus{
				ComponentGit:     Operational,
				ComponentActions: "",
				ComponentAPI:     PartialOutage,
			}},
			want: []ParseError{
				{Kind: "component status", Value: "", Component: ComponentActions},
				{Kind: "component status", Value: "operational", Component: ComponentGit},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.Evaluate(tt.th)
			if err == nil {
				t.Fatalf("Evaluate() = %+v, want error", got)
			}

			if !reflect.DeepEqual(got, Result{}) {
				t.Errorf("Evaluate() result = %+v, want zero value on error", got)
			}

			joined, ok := err.(interface{ Unwrap() []error })
			if !ok {
				t.Fatalf("err %T does not wrap multiple errors", err)
			}

			var gotErrs []ParseError

			for _, e := range joined.Unwrap() {
				pe, ok := errors.AsType[*ParseError](e)
				if !ok {
					t.Fatalf("wrapped err = %v, want *ParseError", e)
				}

				gotErrs = append(gotErrs, *pe)
			}

			if !reflect.DeepEqual(gotErrs, tt.want) {
				t.Errorf("errors = %+v, want %+v", gotErrs, tt.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	valid := Thresholds{
		Overall:    IndicatorMajor,
		Components: map[string]ComponentStatus{ComponentGit: DegradedPerformance},
	}
	if err := valid.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}

	if err := (Thresholds{}).Validate(); err != nil {
		t.Errorf("zero Thresholds Validate() = %v, want nil", err)
	}
}

func TestViolationString(t *testing.T) {
	tests := []struct {
		v    Violation
		want string
	}{
		{overallViolation(IndicatorMajor, IndicatorMinor), "Overall (major >= minor)"},
		{componentViolation(ComponentGit, MajorOutage, PartialOutage), "Git Operations (major_outage >= partial_outage)"},
		{Violation{Reason: ReasonMaintenance, Indicator: IndicatorMaintenance}, "Overall (under maintenance)"},
		{Violation{Component: ComponentActions, Reason: ReasonMaintenance, Status: UnderMaintenance}, "Actions (under maintenance)"},
	}
	for _, tt := range tests {
		if got := tt.v.String(); got != tt.want {
			t.Errorf("String() = %q, want %q", got, tt.want)
		}
	}
}

func TestParseErrorMessage(t *testing.T) {
	tests := []struct {
		err  ParseError
		want string
	}{
		{ParseError{Kind: "indicator", Value: "bogus"}, `githubstatus: invalid indicator threshold "bogus"`},
		{ParseError{Kind: "component status", Value: "ok", Component: ComponentGit}, `githubstatus: invalid component status threshold "ok" for component "Git Operations"`},
	}
	for _, tt := range tests {
		if got := tt.err.Error(); got != tt.want {
			t.Errorf("Error() = %q, want %q", got, tt.want)
		}
	}
}

func TestParseIndicator(t *testing.T) {
	for _, s := range []string{"minor", "major", "critical"} {
		if got, err := ParseIndicator(s); err != nil || string(got) != s {
			t.Errorf("ParseIndicator(%q) = %q, %v", s, got, err)
		}
	}

	for _, s := range []string{"", "none", "maintenance", "bogus"} {
		_, err := ParseIndicator(s)

		_, ok := errors.AsType[*ParseError](err)
		if !ok {
			t.Errorf("ParseIndicator(%q) err = %v, want *ParseError", s, err)
		}
	}
}

func TestParseComponentStatus(t *testing.T) {
	for _, s := range []string{"degraded_performance", "partial_outage", "major_outage"} {
		if got, err := ParseComponentStatus(s); err != nil || string(got) != s {
			t.Errorf("ParseComponentStatus(%q) = %q, %v", s, got, err)
		}
	}

	for _, s := range []string{"", "operational", "under_maintenance", "bogus"} {
		if _, err := ParseComponentStatus(s); err == nil {
			t.Errorf("ParseComponentStatus(%q) expected error", s)
		}
	}
}

func TestImpactSeverity(t *testing.T) {
	tests := []struct {
		i     Impact
		sev   int
		maint bool
	}{
		{ImpactNone, 0, false},
		{ImpactMinor, 1, false},
		{ImpactMajor, 2, false},
		{ImpactCritical, 3, false},
		{ImpactMaintenance, 0, true},
		{"bogus", 0, false},
	}
	for _, tt := range tests {
		if got := tt.i.Severity(); got != tt.sev {
			t.Errorf("%q.Severity() = %d, want %d", tt.i, got, tt.sev)
		}

		if got := tt.i.IsMaintenance(); got != tt.maint {
			t.Errorf("%q.IsMaintenance() = %v, want %v", tt.i, got, tt.maint)
		}
	}
}
