# githubstatus

[![CI](https://github.com/bigkevmcd/githubstatus/actions/workflows/ci.yml/badge.svg)](https://github.com/bigkevmcd/githubstatus/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/bigkevmcd/githubstatus.svg)](https://pkg.go.dev/github.com/bigkevmcd/githubstatus)

A small Go client for the public [githubstatus.com](https://www.githubstatus.com)
API. It also checks GitHub's health against thresholds you configure.

You can use it to stop a deploy or CI job, or to send an alert, when the GitHub
services you depend on are having problems.

## Command-line tool

```sh
go install github.com/bigkevmcd/githubstatus/cmd/githubstatus@latest
```

```sh
githubstatus --overall major \
	--component "Actions=partial_outage" \
	--component "Git Operations=degraded_performance"
```

The tool prints the current status and any violations, and sets its exit code
from the result:

| Code | Meaning                                    |
|------|--------------------------------------------|
| 0    | Healthy                                    |
| 1    | One or more thresholds were met or exceeded |
| 2    | Invalid flags or thresholds                |
| 3    | The status could not be fetched            |

| Flag                   | Default | Description                                                          |
|------------------------|---------|----------------------------------------------------------------------|
| `--overall`             | `major` | Lowest overall indicator that counts as unhealthy; `""` disables it   |
| `--component Name=status` |       | Threshold for one component; repeat the flag for more. Without any, the tool fetches only the small `status.json` |
| `--fail-on-maintenance` | `false` | Treat planned maintenance as unhealthy                               |
| `--fail-on-missing`     | `false` | Exit 1 if a `--component` name doesn't exist                         |
| `--base-url`            | githubstatus.com | Statuspage API base URL                                     |
| `--timeout`             | `10s`   | Timeout for fetching the status                                      |
| `-q`                   | `false` | Print nothing; only set the exit code                                |

For example, to skip a deploy step while GitHub Actions is having problems:

```sh
githubstatus -q --component "Actions=partial_outage" && ./deploy.sh
```

## MCP server

`githubstatus-mcp` is a [Model Context Protocol](https://modelcontextprotocol.io)
server. It lets AI assistants check whether GitHub is having problems, for
example to explain why a push or a CI run failed.

```sh
go install github.com/bigkevmcd/githubstatus/cmd/githubstatus-mcp@latest
```

By default it communicates over stdio. To add it to Claude Code:

```sh
claude mcp add githubstatus -- githubstatus-mcp
```

Other MCP clients use a config like this:

```json
{
  "mcpServers": {
    "githubstatus": { "command": "githubstatus-mcp" }
  }
}
```

To run it as a shared server instead, use `--http`. This serves the MCP
streamable HTTP transport at `/mcp`:

```sh
githubstatus-mcp --http localhost:8080   # endpoint: http://localhost:8080/mcp
```

The HTTP server rejects cross-origin browser requests and DNS-rebinding
attempts, but it has no authentication. Only expose it on a trusted network.

| Flag         | Default          | Description                                              |
|--------------|------------------|----------------------------------------------------------|
| `--http`      |                  | Serve over HTTP on this address instead of stdio         |
| `--cache-ttl` | `30s`            | How long to reuse API responses; `0` disables caching    |
| `--base-url`  | githubstatus.com | Statuspage API base URL                                  |

### Tools

All tools are read-only.

| Tool                          | Description |
|-------------------------------|-------------|
| `get_github_status`           | The overall status, every component (including groups and their members), unresolved incidents and scheduled maintenance |
| `check_github_health`         | Checks the status against optional `overall`, `components` and `fail_on_maintenance` thresholds. It returns `healthy`, the `violations`, and any `missing_components`. With no thresholds, it checks whether the overall indicator is `major` or worse |
| `get_github_incident_history` | Recent incidents, resolved or not, with their duration and affected components. You can filter by `since`/`until`, `component` and `min_impact`. Returns 10 incidents by default (`limit` raises this to 50), with each latest update cut to its first few sentences unless `include_details` is set. `total_matching` says how many incidents matched |

The incident history only covers GitHub's 50 most recent incidents, usually a
couple of months. Every response includes `history_starts_at`, so an assistant
can say when a question goes back further than the history does.

### Resources and prompts

| Name                         | Type     | Description |
|------------------------------|----------|-------------|
| `githubstatus://status`      | Resource | The same JSON as `get_github_status` |
| `githubstatus://incidents`   | Resource | The same JSON as `get_github_incident_history` with `limit` 50 |
| `diagnose_github_problem`    | Prompt   | Asks the assistant whether GitHub's status explains a problem. Takes optional `problem` and `when` arguments |

API responses are cached for 30 seconds by default and shared between tools
and resources. Repeated calls in a conversation don't each hit
githubstatus.com.

### Embedding

To embed the server in your own program, use the `mcpserver` package:

```go
server := mcpserver.New(&githubstatus.Client{}, &mcpserver.Options{Version: "v1.0.0"})
err := server.Run(ctx, &mcp.StdioTransport{})
```

## Library

```sh
go get github.com/bigkevmcd/githubstatus
```

### Usage

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/bigkevmcd/githubstatus"
)

func main() {
	summary, err := (&githubstatus.Client{}).Summary(context.Background())
	if err != nil {
		log.Fatal(err)
	}

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
	if !result.Healthy() {
		os.Exit(1)
	}
}
```

### Fetching data

The zero value of `Client` is ready to use and queries githubstatus.com.

| Method    | Endpoint       | Returns                                                         |
|-----------|----------------|-----------------------------------------------------------------|
| `Status`  | `status.json`  | Page info and the overall indicator                             |
| `Summary` | `summary.json` | The above, plus components, open incidents and scheduled maintenance |
| `Incidents` | `incidents.json` | The 50 most recent incidents, resolved or not, newest first |

You can set `HTTPClient`, `BaseURL` and `UserAgent` on the client. GitHub's
status page runs on Atlassian Statuspage, so setting `BaseURL` lets you query
other Statuspage-hosted pages too:

```go
c := &githubstatus.Client{BaseURL: "https://status.npmjs.org/api/v2"}
```

When the API returns a status other than 200, you get an `*HTTPError`. It
includes the status code, the start of the response body, and the delay from
any `Retry-After` header (`RetryAfter`), which helps when you're rate limited.

Successful responses larger than 10 MiB are rejected.

### Thresholds

`Summary.Evaluate` returns a `Result`. `Result.Violations` holds one
`Violation` for each threshold that is met or exceeded, and
`Result.Healthy()` reports whether there were none.

- **`Overall`**: the lowest overall indicator that counts as unhealthy. From
  least to most severe: `minor`, `major`, `critical`.
- **`Components`**: maps a component name to the lowest status that counts as
  unhealthy. From least to most severe: `degraded_performance`,
  `partial_outage`, `major_outage`. The package defines constants for common
  names, such as `ComponentActions` and `ComponentAPI`.
- **`FailOnMaintenance`**: also treat planned maintenance as unhealthy. This
  applies to the overall status (even when `Overall` is empty) and to the
  components listed in `Components`.

Each `Violation` has a `Reason`, either `ReasonSeverity` or
`ReasonMaintenance`, and the typed observed and threshold values (`Indicator`
and `IndicatorThreshold` for the overall status, `Status` and
`StatusThreshold` for components). Printing a violation gives text such as
`Actions (partial_outage >= partial_outage)` or
`Overall (under maintenance)`.

`Evaluate` returns an error if a threshold can never be exceeded, such as a
typo like `"Major"`, or `"none"` or `"operational"`. You can run the same
check earlier with `Thresholds.Validate`. If your values come from flags or
config files, `ParseIndicator` and `ParseComponentStatus` parse and check them
one at a time.

If a component in `Components` doesn't appear in GitHub's summary, for example
because of a typo or because GitHub renamed it, its name is listed in
`Result.MissingComponents`. It doesn't count as a violation, so it's up to you
whether to treat it as a failure.

## License

Apache License 2.0. See [LICENSE](LICENSE).
