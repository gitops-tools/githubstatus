package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const runMainEnv = "GITHUBSTATUS_MCP_TEST_RUN_MAIN"

// TestMain lets the test binary stand in for the real command: when
// runMainEnv is set, it runs main() instead of the tests. This exercises the
// real stdio wiring without needing to go build a separate binary.
func TestMain(m *testing.M) {
	if os.Getenv(runMainEnv) == "1" {
		main()
		// The test binary is acting as the command process in this branch.
		//revive:disable-next-line:redundant-test-main-exit
		os.Exit(0)
	}

	m.Run()
}

// newStatusAPI serves the summary fixture and returns its base URL.
func newStatusAPI(t *testing.T) string {
	t.Helper()

	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "data-summary.json"))
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/summary.json" {
			http.NotFound(w, r)
			return
		}

		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)

	return srv.URL
}

func newClient() *mcp.Client {
	return mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0"}, nil)
}

// checkSession verifies the server responds correctly over an established
// session.
func checkSession(t *testing.T, cs *mcp.ClientSession) {
	t.Helper()

	ctx := context.Background()

	if got := cs.InitializeResult().ServerInfo.Name; got != "githubstatus" {
		t.Errorf("server name = %q", got)
	}

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(tools.Tools) != 3 {
		t.Errorf("got %d tools, want 3", len(tools.Tools))
	}

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "check_github_health", Arguments: map[string]any{"overall": "minor"}})
	if err != nil {
		t.Fatal(err)
	}

	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}

	out, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Errorf("check_github_health = %#v", res.StructuredContent)
		return
	}

	healthy, healthyOK := out["healthy"].(bool)
	if !healthyOK || healthy || out["description"] != "Minor Service Outage" {
		t.Errorf("check_github_health = %#v", res.StructuredContent)
	}
}

func TestStdio(t *testing.T) {
	baseURL := newStatusAPI(t)

	cmd := exec.Command(os.Args[0], "--base-url", baseURL)

	cmd.Env = append(os.Environ(), runMainEnv+"=1")

	var stderr bytes.Buffer

	cmd.Stderr = &stderr

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Any stray output on stdout would corrupt the protocol and fail here.
	cs, err := newClient().Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connecting: %v\nstderr:\n%s", err, &stderr)
	}

	checkSession(t, cs)

	// Closing the session closes stdin, which should make the server exit
	// cleanly.
	if err := cs.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}

	if stderr.Len() > 0 {
		t.Errorf("unexpected stderr output:\n%s", &stderr)
	}
}

func TestHTTP(t *testing.T) {
	baseURL := newStatusAPI(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pr, pw := io.Pipe()

	done := make(chan error, 1)
	go func() {
		done <- run(ctx, []string{"--http", "127.0.0.1:0", "--base-url", baseURL}, pw)

		_ = pw.Close()
	}()

	// The first log line announces the address.
	lines := bufio.NewScanner(pr)
	if !lines.Scan() {
		t.Fatalf("no output from server: %v", <-done)
	}

	line := lines.Text()

	_, endpoint, ok := strings.Cut(line, "listening on ")
	if !ok || !strings.HasSuffix(endpoint, httpPath) {
		t.Fatalf("unexpected first line %q", line)
	}

	go func() { // keep draining so later log writes don't block
		for lines.Scan() {
			t.Logf("server: %s", lines.Text())
		}
	}()

	cs, err := newClient().Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint}, nil)
	if err != nil {
		t.Fatal(err)
	}

	checkSession(t, cs)
	_ = cs.Close()

	t.Run("rejects cross-origin browser requests", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "https://evil.example")
		req.Header.Set("Sec-Fetch-Site", "cross-site")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}

		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
		}
	})

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run returned %v after cancellation, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not shut down after cancellation")
	}
}

func TestRunErrors(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantError  bool
		wantStderr string
	}{
		{"help", []string{"--help"}, false, "Usage:\n  githubstatus-mcp [flags]"},
		{"unknown flag", []string{"--bogus"}, true, "unknown flag: --bogus"},
		{"unexpected argument", []string{"extra"}, true, "unexpected arguments: [extra]"},
		{"bad listen address", []string{"--http", "256.0.0.1:bad"}, true, "githubstatus-mcp: listen tcp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer

			err := run(context.Background(), tt.args, &stderr)
			if err == nil && tt.wantError {
				t.Fatal("expected an error")
			}

			if err != nil && !tt.wantError {
				t.Fatalf("err = %v, want nil", err)
			}

			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", &stderr, tt.wantStderr)
			}
		})
	}
}
