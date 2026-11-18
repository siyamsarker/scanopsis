// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

// Command scanopsis runs the Scanopsis server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/siyamsarker/scanopsis/web"
)

// Set at build time with -ldflags "-X main.version=... -X main.commit=...".
var (
	version = "dev"
	commit  = ""
)

const listenAddr = ":8080"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("scanopsis", flag.ContinueOnError)
	flags.SetOutput(stderr)
	showVersion := flags.Bool("version", false, "print the version and exit")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		_, _ = fmt.Fprintln(stdout, versionString())
		return 0
	}

	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	assets, ok := web.Assets()
	if !ok {
		logger.Warn("binary was built without the web interface")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Inside a container the server must accept connections on all interfaces.
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", listenAddr) //nolint:gosec // G102: intended, see comment above.
	if err != nil {
		logger.Error("cannot listen", "addr", listenAddr, "error", err)
		return 1
	}
	logger.Info("scanopsis started", "addr", ln.Addr().String(), "version", version)
	if err := serve(ctx, ln, newHandler(assets, ok)); err != nil {
		logger.Error("server stopped with an error", "error", err)
		return 1
	}
	logger.Info("scanopsis stopped")
	return 0
}

func versionString() string {
	return fmt.Sprintf("scanopsis %s (commit %s, %s)", version, resolveCommit(), runtime.Version())
}

// resolveCommit prefers the value injected at build time and falls back to
// the VCS revision that the Go toolchain records when building from git.
func resolveCommit() string {
	if commit != "" {
		return commit
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && s.Value != "" {
				return s.Value
			}
		}
	}
	return "unknown"
}

func newHandler(assets fs.FS, ok bool) http.Handler {
	if !ok {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "The web interface is not included in this build.", http.StatusServiceUnavailable)
		})
	}
	files := http.FileServerFS(assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !servablePath(assets, r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})
}

// servablePath rejects hidden files and directory listings, so only regular
// build output is ever served. A directory is servable only when it contains
// an index.html.
func servablePath(assets fs.FS, urlPath string) bool {
	name := strings.Trim(path.Clean("/"+urlPath), "/")
	for _, seg := range strings.Split(name, "/") {
		if strings.HasPrefix(seg, ".") {
			return false
		}
	}
	if name == "" {
		name = "."
	}
	info, err := fs.Stat(assets, name)
	if err != nil {
		return true // let the file server answer 404
	}
	if info.IsDir() {
		_, err := fs.Stat(assets, path.Join(name, "index.html"))
		return err == nil
	}
	return true
}

// serve runs an HTTP server on ln until ctx is cancelled, then shuts it down
// gracefully.
func serve(ctx context.Context, ln net.Listener, h http.Handler) error {
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
