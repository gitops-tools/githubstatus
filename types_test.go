package githubstatus

import (
	"slices"
	"testing"
	"time"
)

func TestIncidentStart(t *testing.T) {
	created := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

	started := created.Add(-time.Hour)
	if got := (Incident{CreatedAt: created}).Start(); !got.Equal(created) {
		t.Errorf("Start() without StartedAt = %v, want %v", got, created)
	}

	if got := (Incident{CreatedAt: created, StartedAt: &started}).Start(); !got.Equal(started) {
		t.Errorf("Start() = %v, want %v", got, started)
	}
}

func TestIncidentLatestUpdate(t *testing.T) {
	if _, ok := (Incident{}).LatestUpdate(); ok {
		t.Error("LatestUpdate() on an incident with no updates returned ok")
	}
	// Updates are not guaranteed to be ordered; the latest display time wins.
	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

	i := Incident{Updates: []IncidentUpdate{
		{Body: "middle", DisplayAt: base.Add(time.Minute)},
		{Body: "latest", DisplayAt: base.Add(time.Hour)},
		{Body: "first", DisplayAt: base},
	}}
	for range 2 {
		u, ok := i.LatestUpdate()
		if !ok || u.Body != "latest" {
			t.Errorf("LatestUpdate() = %q, %v, want \"latest\"", u.Body, ok)
		}

		slices.Reverse(i.Updates)
	}
}
