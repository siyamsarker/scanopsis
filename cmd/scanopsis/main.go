// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

// Command scanopsis runs the Scanopsis server. `scanopsis admin ...` runs
// administration commands and `scanopsis healthcheck` probes a running
// server.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"sync/atomic"
	"syscall"
	"time"

	// The container image has no time zone database; the backup schedule
	// reads its time zone from this copy.
	_ "time/tzdata"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/audit"
	"github.com/siyamsarker/scanopsis/internal/auditlog"
	"github.com/siyamsarker/scanopsis/internal/auth"
	"github.com/siyamsarker/scanopsis/internal/backup"
	"github.com/siyamsarker/scanopsis/internal/catalog"
	"github.com/siyamsarker/scanopsis/internal/clientip"
	"github.com/siyamsarker/scanopsis/internal/config"
	"github.com/siyamsarker/scanopsis/internal/findings"
	"github.com/siyamsarker/scanopsis/internal/httpapi"
	"github.com/siyamsarker/scanopsis/internal/ingest"
	"github.com/siyamsarker/scanopsis/internal/retention"
	"github.com/siyamsarker/scanopsis/internal/secretbox"
	"github.com/siyamsarker/scanopsis/internal/settings"
	"github.com/siyamsarker/scanopsis/internal/sonar"
	"github.com/siyamsarker/scanopsis/internal/store"
	"github.com/siyamsarker/scanopsis/web"
)

// Set at build time with -ldflags "-X main.version=... -X main.commit=...".
var (
	version = "dev"
	commit  = ""
)

const (
	sessionCleanupInterval = time.Hour
	startupTimeout         = 30 * time.Second
	// shutdownGrace is how long a stop waits for requests in flight.
	shutdownGrace = 15 * time.Second
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Getenv)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) > 0 {
		switch args[0] {
		case "admin":
			return runAdmin(ctx, args[1:], stdout, stderr, getenv)
		case "healthcheck":
			return healthcheck(ctx, stderr, getenv)
		}
	}
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

	cfg, err := config.Load(getenv, os.ReadFile)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "configuration error:\n%v\n", err)
		return 2
	}
	log := slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	if err := serveApp(ctx, cfg, log); err != nil {
		log.Error("scanopsis stopped with an error", "error", err)
		return 1
	}
	log.Info("scanopsis stopped")
	return 0
}

// serveApp connects to the database, wires the application and serves it
// until ctx is cancelled.
func serveApp(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	log.Info("scanopsis starting", "version", version, "commit", resolveCommit(), "config", cfg)

	httpClient, err := newHTTPClient(cfg.CAFile)
	if err != nil {
		return err
	}
	box, err := secretbox.New(cfg.SecretKey)
	if err != nil {
		return fmt.Errorf("%sSECRET_KEY: %w", config.Prefix, err)
	}

	openCtx, cancelOpen := context.WithTimeout(ctx, startupTimeout)
	pool, err := store.Open(openCtx, cfg.DatabaseURL)
	cancelOpen()
	if err != nil {
		return err
	}
	defer pool.Close()
	// Migrations may build indexes on large tables, so they are bounded by
	// shutdown only; the start-up timeout covers the rest of the start-up
	// and begins after them.
	if err := store.MigrateLogged(ctx, pool, log); err != nil {
		return err
	}
	startCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()

	q := store.New(pool)
	rec := audit.New(q, log)
	resolver := clientip.New(cfg.TrustedProxies)

	conf, err := settings.New(startCtx, q, settingsTx(pool), box, cfg.Seeds, rec, log)
	if err != nil {
		return err
	}
	snap := conf.Current()
	sessions := auth.NewSessions(q, auth.SessionOptions{
		Idle: snap.SessionIdleTimeout, Absolute: snap.SessionAbsoluteTimeout, Secure: cfg.Secure(),
		ClientIP: resolver.IP,
	})
	mySessions := auth.NewMySessions(q, sessions, rec, resolver.IP, log)

	setup := auth.NewSetup(q, sessions, rec, resolver.IP, auth.NewRateLimiter(12*time.Second, 5, nil), log)
	// prepareSetupToken is also run after a restore, which may leave a
	// database without an administrator.
	prepareSetupToken := func(ctx context.Context) error {
		token, err := setup.PrepareToken(ctx)
		if err != nil {
			return err
		}
		if token != "" {
			log.Warn("no administrator exists yet: open Scanopsis in a browser to finish the setup",
				"setup_url", cfg.PublicURL.String()+"/setup", "setup_token", token)
		}
		return nil
	}
	if err := prepareSetupToken(startCtx); err != nil {
		return err
	}

	recoveryKey, err := auth.RecoveryKey(cfg.SecretKey)
	if err != nil {
		return err
	}
	mfa := auth.NewMFA(q, sessions, rec, resolver.IP, auth.MFAOptions{
		Issuer: "Scanopsis", Box: box, Limiter: auth.NewRateLimiter(30*time.Second, 5, nil), Logger: log,
		RecoveryKey: recoveryKey,
	})
	localLogin := auth.NewLocalLogin(q, sessions, rec, resolver.IP,
		auth.NewRateLimiter(12*time.Second, 10, nil), auth.NewRateLimiter(12*time.Second, 10, nil), log)

	// GitLab sign-in and session timeouts follow the Settings page; changes
	// apply to the next request without a restart.
	var gitlab atomic.Pointer[auth.GitLab]
	apply := func(s settings.Snapshot) {
		sessions.SetTimeouts(s.SessionIdleTimeout, s.SessionAbsoluteTimeout)
		if !s.GitLabEnabled() {
			gitlab.Store(nil)
			return
		}
		gitlab.Store(auth.NewGitLab(auth.GitLabOptions{
			IssuerURL:     s.GitLabURL,
			ClientID:      s.GitLabClientID,
			ClientSecret:  s.GitLabClientSecret,
			PublicURL:     cfg.PublicURL.String(),
			Secure:        cfg.Secure(),
			AllowedGroups: s.GitLabAllowedGroups,
			HTTPClient:    httpClient,
			Logger:        log,
		}, q, sessions, rec, resolver.IP))
	}
	apply(snap)
	conf.OnChange(apply)

	notEnabledGitLab := httpapi.NotEnabled("GitLab sign-in")
	gitlabHandler := func(pick func(*auth.GitLab) http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if g := gitlab.Load(); g != nil {
				pick(g).ServeHTTP(w, r)
				return
			}
			notEnabledGitLab.ServeHTTP(w, r)
		})
	}
	notEnabledLocal := httpapi.NotEnabled("Local sign-in")

	// The readiness probe uses a connection of its own: a pool saturated by
	// requests must not make a healthy server report "not ready".
	probe := store.NewProbe(pool)
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		probe.Close(closeCtx)
	}()

	deps := httpapi.Deps{
		Log: log,
		DB:  probe,
		// Requests beyond the pool's size would only queue for a connection;
		// they wait briefly for a slot and are then answered 503 busy.
		MaxConcurrent: int(pool.Config().MaxConns),
		Users:         userStore{q: q, pool: pool},
		Sessions:      sessions,
		Audit:         rec,
		ClientIP:      resolver.IP,
		LocalLogin: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if conf.Current().LocalLoginEnabled {
				localLogin.ServeHTTP(w, r)
				return
			}
			notEnabledLocal.ServeHTTP(w, r)
		}),
		GitLabLogin:    gitlabHandler((*auth.GitLab).LoginHandler),
		GitLabCallback: gitlabHandler((*auth.GitLab).CallbackHandler),
		Methods: func() (bool, bool) {
			return gitlab.Load() != nil, conf.Current().LocalLoginEnabled
		},
		Setup:                setup.Handler(),
		MFAStartEnrollment:   mfa.StartEnrollmentHandler(),
		MFAConfirmEnrollment: mfa.ConfirmEnrollmentHandler(),
		MFAVerify:            mfa.VerifyHandler(),
		PasswordChange:       auth.NewPasswordChange(q, rec, auth.NewRateLimiter(12*time.Second, 10, nil), log),
		MySessionsList:       mySessions.ListHandler(),
		MySessionsEndOthers:  mySessions.EndOthersHandler(),
		MySessionEnd:         mySessions.EndHandler(),
		SettingsList:         conf.ListHandler(),
		SettingsUpdate:       conf.UpdateHandler(),
		Build:                httpapi.BuildInfo{Version: version, Commit: resolveCommit(), Go: runtime.Version()},
		Secure:               cfg.Secure(),
	}
	catalogService := catalog.New(pool, conf, rec, log)
	backups, err := newBackupService(cfg, pool, conf, rec, log, func(ctx context.Context) error {
		// A restore replaced the database under the running server: read
		// the settings again and drop what was computed from the old data.
		// Sessions are read from the database on every request, so the ones
		// the restore deleted are already gone.
		if err := conf.Reload(ctx); err != nil {
			return err
		}
		catalogService.ResetCaches()
		return prepareSetupToken(ctx)
	})
	if err != nil {
		return err
	}
	// Ends running backup jobs and waits for them, also when start-up fails
	// below; Run does the same when ctx ends.
	defer backups.Close()
	sonarQube := sonar.New(pool, conf, sonar.Options{HTTPClient: httpClient, Logger: log, Audit: rec, ClientIP: resolver.IP})
	features, err := mergeFeatures(
		ingest.New(pool, conf, rec, ingest.Options{
			Audience:   cfg.PublicURL.String(),
			HTTPClient: httpClient,
			Limiter:    auth.NewRateLimiter(time.Second, 60, nil),
			ClientIP:   resolver.IP,
			Logger:     log,
		}).Handlers(),
		catalogService.Handlers(),
		findings.New(pool, conf, log).Handlers(),
		sonarQube.Handlers(),
		auditlog.New(pool, log).Handlers(),
	)
	if err != nil {
		return err
	}
	deps.Features = features

	if assets, ok := web.Assets(); ok {
		deps.Assets = assets
	} else {
		log.Warn("binary was built without the web interface")
	}

	go removeExpiredSessions(ctx, sessions, log)
	go sonarQube.Run(ctx)
	go retention.New(pool, conf, log).Run(ctx)
	go backups.Run(ctx)

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.ListenAddr, err)
	}
	log.Info("scanopsis listening", "addr", ln.Addr().String())
	return serve(ctx, ln, httpapi.New(deps), log, shutdownGrace)
}

// newBackupService opens the backup folder and returns the backup service.
// A folder that cannot be used is not fatal: the server starts, logs one
// warning that names the folder and the fix, and every backup and restore
// then fails with that reason. A secret key that cannot give a fingerprint or
// a signing key stops the start.
func newBackupService(cfg *config.Config, pool *pgxpool.Pool, conf *settings.Service, rec auth.AuditRecorder, log *slog.Logger, afterRestore func(context.Context) error) (*backup.Service, error) {
	fingerprint, err := backup.KeyFingerprint(cfg.SecretKey)
	if err != nil {
		return nil, err
	}
	signingKey, err := backup.SigningKey(cfg.SecretKey)
	if err != nil {
		return nil, err
	}
	o := backup.ServiceOptions{
		Pool:           pool,
		Settings:       conf,
		Audit:          rec,
		Log:            log,
		KeyFingerprint: fingerprint,
		AfterRestore:   afterRestore,
		ExclusiveLocks: []int64{retention.AdvisoryLockKey, sonar.AdvisoryLockKey},
	}
	dir, err := backup.OpenDir(cfg.BackupDir, fingerprint)
	switch {
	case errors.Is(err, backup.ErrFolder):
		log.Warn("backups are unavailable until the backup folder is fixed; fix it and restart Scanopsis",
			"folder", cfg.BackupDir, "error", err)
	case err != nil:
		return nil, err
	default:
		o.Dir = dir
		o.Dumper = &backup.Dumper{
			Pool: pool, Dir: dir, App: version, Commit: resolveCommit(),
			KeyFingerprint: fingerprint, SigningKey: signingKey,
		}
	}
	return backup.NewService(o), nil
}

// mergeFeatures combines the handlers of the feature packages, keyed by
// OpenAPI operation ID. Two packages claiming one operation is a wiring
// mistake and stops the server.
func mergeFeatures(sets ...map[string]http.Handler) (map[string]http.Handler, error) {
	out := map[string]http.Handler{}
	for _, set := range sets {
		for id, h := range set {
			if _, dup := out[id]; dup {
				return nil, fmt.Errorf("operation %s is handled twice", id)
			}
			out[id] = h
		}
	}
	return out, nil
}

// settingsTx runs fn with queries bound to one database transaction.
func settingsTx(pool *pgxpool.Pool) func(context.Context, func(settings.Store) error) error {
	return func(ctx context.Context, fn func(settings.Store) error) error {
		return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return fn(store.New(tx)) })
	}
}

// userStore adapts the generated queries to the API's needs.
type userStore struct {
	q    *store.Queries
	pool *pgxpool.Pool
}

func (u userStore) ListUsers(ctx context.Context, arg store.ListUsersParams) ([]store.User, error) {
	return u.q.ListUsers(ctx, arg)
}

func (u userStore) GetUser(ctx context.Context, id int64) (store.User, error) {
	usr, err := u.q.GetUser(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return usr, store.ErrNotFound
	}
	return usr, err
}

func (u userStore) SetUserRole(ctx context.Context, id int64, role string) (store.User, error) {
	return store.SetUserRole(ctx, u.pool, id, role)
}

func (u userStore) ResetMFA(ctx context.Context, id int64) error {
	return store.ResetMFA(ctx, u.pool, id)
}

func (u userStore) SetUserDisabled(ctx context.Context, id int64, disabled bool) (store.User, error) {
	return store.SetUserDisabled(ctx, u.pool, id, disabled)
}

func (u userStore) DeleteUser(ctx context.Context, id int64) (store.User, error) {
	return store.DeleteUser(ctx, u.pool, id)
}

// expiredSessionRemover is the part of *auth.Sessions the clean-up loop uses.
type expiredSessionRemover interface {
	RemoveExpired(ctx context.Context) (int64, error)
}

// removeExpiredSessions deletes expired sessions every hour until ctx ends.
func removeExpiredSessions(ctx context.Context, s expiredSessionRemover, log *slog.Logger) {
	t := time.NewTicker(sessionCleanupInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := s.RemoveExpired(ctx); err != nil {
				log.Error("cannot remove expired sessions", "error", err)
			} else if n > 0 {
				log.Debug("removed expired sessions", "count", n)
			}
		}
	}
}

// newHTTPClient returns the client for outgoing calls to GitLab. It trusts
// the system roots plus, if set, the PEM bundle in caFile. TLS verification
// is never disabled.
func newHTTPClient(caFile string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if caFile != "" {
		pem, err := os.ReadFile(caFile) //nolint:gosec // G304: the path comes from the operator's configuration.
		if err != nil {
			return nil, fmt.Errorf("read %sCA_FILE: %w", config.Prefix, err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("%sCA_FILE %s contains no PEM certificates", config.Prefix, caFile)
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	}
	return &http.Client{Transport: transport, Timeout: 30 * time.Second}, nil
}

// healthcheck asks the local server for /healthz. It lets the container
// image, which has no shell or curl, declare a Docker health check.
func healthcheck(ctx context.Context, stderr io.Writer, getenv func(string) string) int {
	addr := getenv(config.Prefix + "LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "healthcheck: invalid %sLISTEN_ADDR %q\n", config.Prefix, addr)
		return 1
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort("127.0.0.1", port)+"/healthz", nil)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "healthcheck: %v\n", err)
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "healthcheck: %v\n", err)
		return 1
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = fmt.Fprintf(stderr, "healthcheck: status %d\n", resp.StatusCode)
		return 1
	}
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

// serve runs an HTTP server on ln until ctx is cancelled, then shuts it down
// gracefully: it stops accepting connections and waits up to grace for
// requests in flight. Requests still running after that, such as a slow
// upload, are cut off; that is the expected result of a deliberate stop, so it
// is logged as a warning and is not an error.
func serve(ctx context.Context, ln net.Listener, h http.Handler, log *slog.Logger, grace time.Duration) error {
	var inflight atomic.Int64
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			inflight.Add(1)
			defer inflight.Add(-1)
			h.ServeHTTP(w, r)
		}),
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

	shutdownCtx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		if !errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("shutdown: %w", err)
		}
		cutOff := inflight.Load()
		_ = srv.Close()
		log.Warn("shutdown deadline reached: requests still running were cut off",
			"requests_cut_off", cutOff, "grace", grace.String())
	}
	if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
