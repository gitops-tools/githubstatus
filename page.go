package githubstatus

import "time"

// Page describes the status page itself.
type Page struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	TimeZone  string    `json:"time_zone"`
	UpdatedAt time.Time `json:"updated_at"`
}

// OverallStatus is the rollup status of the page.
type OverallStatus struct {
	Indicator   Indicator `json:"indicator"`
	Description string    `json:"description"`
}
