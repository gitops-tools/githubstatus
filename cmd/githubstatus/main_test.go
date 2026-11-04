package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// newServer serves the given summary fixture as summary.json, and the
// matching status fixture (data-status*.json) as status.json. It returns the
// base URL and a function reporting which paths were requested.
func newServer(t *testing.T, fixture string) (string, func() []string) {
	t.Helper()

	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
		if err != nil {
			t.Fatal(err)
		}

		return b
	}
	routes := map[string][]byte{
		"/summary.json": read(fixture),
		"/status.json":  read(strings.Replace(fixture, "summary", "status", 1)),
	}

	var (
		mu        sync.Mutex
		requested []string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()

		requested = append(requested, r.URL.Path)
		mu.Unlock()

		b, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}

		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)

	return srv.URL, func() []string {
		mu.Lock()
		defer mu.Unlock()

		return slices.Clone(requested)
	}
}

func TestRun(t *testing.T) {
	// data-summary.json: overall "minor", all components operational.
	// data-summary-2.json: overall "maintenance", Actions under maintenance.
	tests := []struct {
		name       string
		fixture    string
		args       []string
		wantCode   int
		wantStdout []string
		wantStderr []string
	}{
		{
			name:       "default threshold, healthy",
			fixture:    "data-summary.json",
			wantCode:   exitHealthy,
			wantStdout: []string{"GitHub: Minor Service Outage"},
		},
		{
			name:       "overall minor, unhealthy",
			fixture:    "data-summary.json",
			args:       []string{"--overall", "minor"},
			wantCode:   exitUnhealthy,
			wantStdout: []string{"unhealthy: Overall (minor >= minor)"},
		},
		{
			name:     "overall disabled",
			fixture:  "data-summary.json",
			args:     []string{"--overall", ""},
			wantCode: exitHealthy,
		},
		{
			name:     "component operational",
			fixture:  "data-summary.json",
			args:     []string{"--component", "Git Operations=degraded_performance"},
			wantCode: exitHealthy,
		},
		{
			name:     "maintenance ignored by default",
			fixture:  "data-summary-2.json",
			args:     []string{"--overall", "minor", "--component", "Actions=degraded_performance"},
			wantCode: exitHealthy,
		},
		{
			name:     "fail on maintenance",
			fixture:  "data-summary-2.json",
			args:     []string{"--fail-on-maintenance", "--component", "Actions=major_outage"},
			wantCode: exitUnhealthy,
			wantStdout: []string{
				"unhealthy: Overall (under maintenance)",
				"unhealthy: Actions (under maintenance)",
			},
		},
		{
			name:       "missing component warns",
			fixture:    "data-summary.json",
			args:       []string{"--component", "Actionz=partial_outage"},
			wantCode:   exitHealthy,
			wantStderr: []string{`no such component: "Actionz"`},
		},
		{
			name:       "missing component with fail-on-missing",
			fixture:    "data-summary.json",
			args:       []string{"--fail-on-missing", "--component", "Actionz=partial_outage"},
			wantCode:   exitUnhealthy,
			wantStderr: []string{`no such component: "Actionz"`},
		},
		{
			name:     "quiet",
			fixture:  "data-summary.json",
			args:     []string{"-q", "--overall", "minor"},
			wantCode: exitUnhealthy,
		},
		{
			name:       "invalid overall",
			args:       []string{"--overall", "Major"},
			wantCode:   exitUsage,
			wantStderr: []string{`invalid indicator threshold "Major"`},
		},
		{
			name:       "invalid component status",
			args:       []string{"--component", "Actions=operational"},
			wantCode:   exitUsage,
			wantStderr: []string{`invalid component status threshold "operational"`},
		},
		{
			name:       "malformed component",
			args:       []string{"--component", "Actions"},
			wantCode:   exitUsage,
			wantStderr: []string{`want "Name=status"`},
		},
		{
			name:       "unexpected argument",
			args:       []string{"extra"},
			wantCode:   exitUsage,
			wantStderr: []string{"unexpected arguments: extra"},
		},
		{
			name:       "help",
			args:       []string{"-h"},
			wantCode:   exitHealthy,
			wantStderr: []string{"Usage:", "githubstatus [flags]"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := tt.args
			if tt.fixture != "" {
				baseURL, _ := newServer(t, tt.fixture)
				args = append([]string{"--base-url", baseURL}, args...)
			}

			var stdout, stderr bytes.Buffer

			code := run(context.Background(), args, &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tt.wantCode, &stdout, &stderr)
			}

			for _, want := range tt.wantStdout {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("stdout missing %q:\n%s", want, &stdout)
				}
			}

			for _, want := range tt.wantStderr {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("stderr missing %q:\n%s", want, &stderr)
				}
			}

			if slices.Contains(args, "-q") && stdout.Len()+stderr.Len() > 0 {
				t.Errorf("-q produced output:\nstdout:\n%s\nstderr:\n%s", &stdout, &stderr)
			}
		})
	}
}

func TestRunFetchesOnlyWhatItNeeds(t *testing.T) {
	tests := []struct {
		name     string
		fixture  string
		args     []string
		wantCode int
		wantPath string
	}{
		{"overall only uses status.json", "data-summary.json", []string{"--overall", "minor"}, exitUnhealthy, "/status.json"},
		{"default uses status.json", "data-summary.json", nil, exitHealthy, "/status.json"},
		{"overall maintenance via status.json", "data-summary-2.json", []string{"--fail-on-maintenance"}, exitUnhealthy, "/status.json"},
		{"components need summary.json", "data-summary.json", []string{"--component", "Actions=major_outage"}, exitHealthy, "/summary.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseURL, requested := newServer(t, tt.fixture)

			var stdout, stderr bytes.Buffer

			code := run(context.Background(), append([]string{"--base-url", baseURL}, tt.args...), &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tt.wantCode, &stdout, &stderr)
			}

			if got := requested(); !slices.Equal(got, []string{tt.wantPath}) {
				t.Errorf("requested %v, want [%s]", got, tt.wantPath)
			}
		})
	}
}

func TestRunFetchError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)

	var stdout, stderr bytes.Buffer

	code := run(context.Background(), []string{"--base-url", srv.URL}, &stdout, &stderr)
	if code != exitFetch {
		t.Errorf("exit code = %d, want %d", code, exitFetch)
	}

	if !strings.Contains(stderr.String(), "unexpected status 404") {
		t.Errorf("stderr = %q", &stderr)
	}
}
