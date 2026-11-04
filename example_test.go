package githubstatus_test

import (
	"context"
	"fmt"
	"log"

	"github.com/bigkevmcd/githubstatus"
)

// This example fetches the current status from githubstatus.com and checks
// it against thresholds. It is compiled by go test but not run, because the
// output depends on GitHub's live status.
func Example() {
	summary, err := (&githubstatus.Client{}).Summary(context.Background())
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("GitHub:", summary.Status.Description)

	result, err := summary.Evaluate(githubstatus.Thresholds{
		Overall: githubstatus.IndicatorMajor,
		Components: map[string]githubstatus.ComponentStatus{
			githubstatus.ComponentActions: githubstatus.PartialOutage,
			githubstatus.ComponentGit:     githubstatus.DegradedPerformance,
		},
	})
	if err != nil {
		log.Fatal(err) // invalid thresholds
	}

	for _, name := range result.MissingComponents {
		fmt.Println("warning: no such component:", name)
	}

	for _, v := range result.Violations {
		fmt.Println("unhealthy:", v)
	}

	if result.Healthy() {
		fmt.Println("healthy")
	}
}

// This example looks up the status of a single component.
func ExampleSummary_Component() {
	summary, err := (&githubstatus.Client{}).Summary(context.Background())
	if err != nil {
		log.Fatal(err)
	}

	if c, ok := summary.Component(githubstatus.ComponentActions); ok {
		fmt.Printf("%s: %s\n", c.Name, c.Status)
	}
}
