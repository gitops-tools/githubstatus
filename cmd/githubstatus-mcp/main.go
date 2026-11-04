// Command githubstatus-mcp is a Model Context Protocol (MCP) server that
// exposes GitHub's status from githubstatus.com.
//
// By default it communicates over stdio, so it can be launched directly by
// an MCP client. With --http it instead serves the MCP streamable HTTP
// transport at /mcp on the given address:
//
//	githubstatus-mcp --http localhost:8080
//
// The HTTP server has no authentication; only expose it on a trusted
// network.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/bigkevmcd/githubstatus"
	"github.com/bigkevmcd/githubstatus/mcpserver"
)

// httpPath is where the streamable HTTP transport is served.
const httpPath = "/mcp"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Stderr)

	stop()

	if err != nil {
		os.Exit(1)
	}
}

// run starts the server and blocks until ctx is cancelled, the client
// disconnects (stdio) or the server fails. Errors are logged to stderr
// before being returned. stdout is reserved for the stdio transport.
func run(ctx context.Context, args []string, stderr io.Writer) error {
	logger := log.New(stderr, "githubstatus-mcp: ", 0)

	baseURL := githubstatus.DefaultBaseURL

	var httpAddr string

	cacheTTL := mcpserver.DefaultCacheTTL
	cmd := &cobra.Command{
		Use:           "githubstatus-mcp",
		Short:         "Expose GitHub status over MCP",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unexpected arguments: %v", args)
			}

			return nil
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			if cacheTTL == 0 {
				cacheTTL = -1 // the flag's 0 means "off"; Options' 0 means "default"
			}

			server := mcpserver.New(&githubstatus.Client{BaseURL: baseURL}, &mcpserver.Options{
				Version:  version(),
				CacheTTL: cacheTTL,
			})

			var err error
			if httpAddr != "" {
				err = serveHTTP(ctx, server, httpAddr, logger)
			} else {
				err = server.Run(ctx, &mcp.StdioTransport{})
			}

			if err != nil && ctx.Err() == nil {
				return err
			}

			return nil
		},
	}
	cmd.Flags().StringVar(&baseURL, "base-url", baseURL, "Statuspage API base URL")
	cmd.Flags().StringVar(&httpAddr, "http", "", `serve MCP over streamable HTTP at `+httpPath+` on this address (e.g. "localhost:8080") instead of stdio`)
	cmd.Flags().DurationVar(&cacheTTL, "cache-ttl", cacheTTL, "how long to reuse API responses; negative disables caching")
	cmd.SetArgs(append([]string{}, args...))
	cmd.SetOut(stderr)
	cmd.SetErr(stderr)

	if err := cmd.ExecuteContext(ctx); err != nil {
		logger.Print(err)
		return err
	}

	return nil
}

func serveHTTP(ctx context.Context, server *mcp.Server, addr string, logger *log.Logger) error {
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	mux := http.NewServeMux()
	mux.Handle(httpPath, http.NewCrossOriginProtection().Handler(handler))

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	logger.Printf("listening on http://%s%s", ln.Addr(), httpPath)

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	go func() {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_ = srv.Shutdown(shutdownCtx)
	}()

	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}

// version returns the module version when installed with go install, or
// "devel" for local builds.
func version() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}

	return "devel"
}
