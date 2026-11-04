// Command githubstatus checks githubstatus.com and exits non-zero when GitHub
// is unhealthy according to the thresholds given on the command line.
//
// Exit codes:
//
//	0  healthy
//	1  one or more thresholds were met or exceeded
//	2  invalid usage or thresholds
//	3  the status could not be fetched
//
// Example:
//
//	githubstatus --overall major \
//	    --component "Actions=partial_outage" \
//	    --component "Git Operations=degraded_performance"
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bigkevmcd/githubstatus"
)

const (
	exitHealthy   = 0
	exitUnhealthy = 1
	exitUsage     = 2
	exitFetch     = 3
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)

	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	var (
		code              = exitHealthy
		overall           = string(githubstatus.IndicatorMajor)
		components        []string
		failOnMaintenance bool
		failOnMissing     bool
		baseURL           = githubstatus.DefaultBaseURL
		timeout           = 10 * time.Second
		quiet             bool
	)

	cmd := &cobra.Command{
		Use:           "githubstatus",
		Short:         "Check GitHub's current status against thresholds",
		Long:          "Checks githubstatus.com and exits 1 if any threshold is met or exceeded, 2 on invalid usage, and 3 if the status could not be fetched.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unexpected arguments: %s", strings.Join(args, " "))
			}

			return nil
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			var th githubstatus.Thresholds

			if overall != "" {
				ind, err := githubstatus.ParseIndicator(overall)
				if err != nil {
					_, _ = fmt.Fprintln(stderr, err)
					code = exitUsage

					return nil
				}

				th.Overall = ind
			}

			for _, value := range components {
				name, status, ok := strings.Cut(value, "=")

				name = strings.TrimSpace(name)
				if !ok || name == "" {
					_, _ = fmt.Fprintln(stderr, `want "Name=status"`)
					code = exitUsage

					return nil
				}

				st, err := githubstatus.ParseComponentStatus(strings.TrimSpace(status))
				if err != nil {
					_, _ = fmt.Fprintln(stderr, err)
					code = exitUsage

					return nil
				}

				if th.Components == nil {
					th.Components = make(map[string]githubstatus.ComponentStatus)
				}

				th.Components[name] = st
			}

			th.FailOnMaintenance = failOnMaintenance
			if err := th.Validate(); err != nil {
				_, _ = fmt.Fprintln(stderr, err)
				code = exitUsage

				return nil
			}

			if quiet {
				stdout, stderr = io.Discard, io.Discard
			}

			fetchCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			summary, err := fetch(fetchCtx, &githubstatus.Client{BaseURL: baseURL}, th)
			if err != nil {
				_, _ = fmt.Fprintln(stderr, err)
				code = exitFetch

				return nil
			}

			result, err := summary.Evaluate(th)
			if err != nil {
				// Unreachable: the thresholds were validated above.
				_, _ = fmt.Fprintln(stderr, err)
				code = exitUsage

				return nil
			}

			_, _ = fmt.Fprintf(stdout, "%s: %s\n", summary.Page.Name, summary.Status.Description)

			for _, name := range result.MissingComponents {
				_, _ = fmt.Fprintf(stderr, "githubstatus: no such component: %q\n", name)
			}

			for _, v := range result.Violations {
				_, _ = fmt.Fprintln(stdout, "unhealthy:", v)
			}

			if !result.Healthy() || (failOnMissing && len(result.MissingComponents) > 0) {
				code = exitUnhealthy
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&overall, "overall", overall,
		`minimum overall indicator that counts as unhealthy: minor, major or critical ("" to disable)`)
	cmd.Flags().StringArrayVar(&components, "component", nil,
		`component threshold as "Name=status", where status is degraded_performance, partial_outage or major_outage (repeatable)`)
	cmd.Flags().BoolVar(&failOnMaintenance, "fail-on-maintenance", false, "treat planned maintenance as unhealthy")
	cmd.Flags().BoolVar(&failOnMissing, "fail-on-missing", false,
		"treat components given with --component that don't exist as unhealthy")
	cmd.Flags().StringVar(&baseURL, "base-url", baseURL, "Statuspage API base URL")
	cmd.Flags().DurationVar(&timeout, "timeout", timeout, "timeout for fetching the status")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "print nothing; only set the exit code")
	cmd.SetArgs(append([]string{}, args...))
	cmd.SetOut(stderr)
	cmd.SetErr(stderr)

	if err := cmd.ExecuteContext(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "githubstatus: %v\n", err)
		return exitUsage
	}

	return code
}

// fetch returns the data needed to evaluate th. Component thresholds need
// the full summary.json; otherwise the much smaller status.json is enough,
// since it carries the same overall indicator (including maintenance).
func fetch(ctx context.Context, c *githubstatus.Client, th githubstatus.Thresholds) (*githubstatus.Summary, error) {
	if len(th.Components) > 0 {
		return c.Summary(ctx)
	}

	st, err := c.Status(ctx)
	if err != nil {
		return nil, err
	}

	return &githubstatus.Summary{Page: st.Page, Status: st.Status}, nil
}
