// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/audit"
	"github.com/siyamsarker/scanopsis/internal/auth"
	"github.com/siyamsarker/scanopsis/internal/httpapi"
	"github.com/siyamsarker/scanopsis/internal/settings"
)

// DefaultMaxReportBytes is the limit on a decompressed report when
// Options.MaxReportBytes is 0.
const DefaultMaxReportBytes = 64 << 20

const (
	// maxRefLen bounds ref, which is in an index (scans_current_idx).
	maxRefLen = 1024
)

// Error codes of the ingestTrivy operation.
const (
	codeNotConfigured = "ingest_not_configured"
	codeInvalidToken  = "invalid_token"
	// codeGitLabUnavailable: GitLab's signing keys could not be fetched.
	codeGitLabUnavailable = "gitlab_unavailable"
	codeNamespace         = "namespace_not_allowed"
	codeRefProtected      = "ref_not_protected"
	codeMediaType         = "unsupported_media_type"
	codeTooLarge          = "report_too_large"
	codeInvalidReport     = "invalid_report"
	codeNotTrivy          = "not_a_trivy_report"
	codeRateLimited       = "too_many_requests"
	codeInvalidParam      = "invalid_parameter"
	codeConflict          = "service_conflict"
	codeArchived          = "service_archived"
	// codeBusy: every upload slot is taken and the client stopped waiting.
	codeBusy = "busy"
	// codeTimeout: the body did not arrive within the read deadline.
	codeTimeout = "request_timeout"
	// codeUnavailable: the database could not be reached or could not take
	// the work. The upload was not stored; retry.
	codeUnavailable = "unavailable"
	// codeProjectLimited: the project uploaded more often than it may.
	codeProjectLimited = "project_rate_limited"
	// codeJobLimited: one job uploaded more different artifacts than it may.
	codeJobLimited = "job_artifact_limit"
)

// Defaults of the per-project limiter: a project may upload DefaultProjectBurst
// reports at once and then one more every DefaultProjectEvery. The rate is
// per project, not per client address, so it holds however many runners a
// project has and whatever addresses they use.
//
// The figures come from the size of the installation: 3,000 pipelines a day
// over 500 services is six a day for the average service. A monorepo that
// scans 30 images uploads 31 reports per pipeline. The burst of 100 takes three
// such pipelines finishing together. The refill of one report per 10 s is 360
// an hour and 8,640 a day, which is 11 such pipelines an hour, round the
// clock: far above the average project, and 10 times the average day fits in
// working hours. A project that scripts uploads faster than that is answered 429,
// and the retries of the script spread the rest out. What the limit bounds is
// the growth of the database from one project: scans of different artifacts
// are never replaced, so without it a project could add them as fast as the
// network allows.
const (
	DefaultProjectBurst = 100
	DefaultProjectEvery = 10 * time.Second
)

// MaxArtifactsPerJob is the most different artifacts (artifact name and kind
// on the job's ref) one job may store. A job normally uploads one report for
// each image it builds and one secret scan; 50 leaves room for a large
// monorepo job and stops one job from creating scans without end, which the
// project rate would only slow down. Uploading again an artifact the job
// already has replaces it and does not count.
const MaxArtifactsPerJob = 50

// jobLimitedRetryAfter is the Retry-After value, in seconds, of a response for
// a job over its artifact limit. Waiting does not help that job; the value is
// above the retry time of the script in the CI guide so that it gives up.
const jobLimitedRetryAfter = 3600

// DefaultReadTimeout is how long an admitted upload has to send its whole
// body when Options.ReadTimeout is 0. It replaces the server's read timeout
// for this route (30 s in the server), which cuts off large uploads from slow
// links.
const DefaultReadTimeout = 5 * time.Minute

// writeGrace is how long after the read deadline the answer can still be
// written. It replaces the server's write timeout (60 s) for this route, so
// an upload that used its whole read time still gets an answer.
const writeGrace = 60 * time.Second

// DefaultAdmitWait is how long an upload waits for memory to be free when
// Options.AdmitWait is 0.
const DefaultAdmitWait = 30 * time.Second

// busyRetryAfter is the Retry-After value, in seconds, of a busy response.
const busyRetryAfter = 5

// auditTimeout bounds the audit write of a rejection.
const auditTimeout = 5 * time.Second

// Defaults of the audit limiter: a client address gets this many rejection
// records at once and then one more per DefaultAuditEvery.
const (
	DefaultAuditBurst = 10
	DefaultAuditEvery = time.Minute
)

// Options configure an Ingest.
type Options struct {
	// Audience is the Scanopsis public URL. CI tokens must carry it in aud.
	Audience string
	// MaxReportBytes limits the decompressed report (and the uploaded body).
	// 0 means DefaultMaxReportBytes.
	MaxReportBytes int64
	// HTTPClient fetches GitLab's signing keys (for a custom CA). nil means
	// http.DefaultClient.
	HTTPClient *http.Client
	// MemoryBudget is the number of bytes of estimated memory the uploads
	// being read and stored may use together, all addresses together (see
	// uploadWeight). Others wait for memory to be free. 0 means two fifths of the
	// process memory limit (GOMEMLIMIT), or 160 MiB without one.
	MemoryBudget int64
	// ReadTimeout is how long an upload has to send its whole body, counted
	// from the moment it is admitted. 0 means DefaultReadTimeout.
	ReadTimeout time.Duration
	// AdmitWait is how long an upload waits for memory before it is answered
	// with 503 busy. 0 means DefaultAdmitWait.
	AdmitWait time.Duration
	// DBSlots is how many uploads may be in their database phase at once.
	// 0 means a quarter of the pool's MaxConns, at least 2.
	DBSlots int
	// Limiter limits uploads per client address. nil means no limit.
	Limiter *auth.RateLimiter
	// ProjectLimiter limits uploads per GitLab project, keyed by the project
	// ID of the verified token. nil means a burst of DefaultProjectBurst and
	// then one per DefaultProjectEvery.
	ProjectLimiter *auth.RateLimiter
	// AuditLimiter limits how many rejections per client address are written
	// to the audit log. nil means a burst of DefaultAuditBurst and then one
	// per DefaultAuditEvery.
	AuditLimiter *auth.RateLimiter
	// ClientIP returns the client address for rate limiting and audit.
	ClientIP func(*http.Request) netip.Addr
	Logger   *slog.Logger
	Now      func() time.Time
}

// SettingsSource gives the current settings.
type SettingsSource interface{ Current() settings.Snapshot }

// Ingest serves the ingestTrivy operation.
type Ingest struct {
	cfg   SettingsSource
	audit auth.AuditRecorder
	opts  Options
	log   *slog.Logger
	ver   *verifier
	store saver
	// auditLimit bounds the audit records per client address, so requests
	// that need no credentials cannot fill the audit log.
	auditLimit *auth.RateLimiter
	// busyAuditLimit bounds the audit records of busy answers per client
	// address. It is separate from auditLimit, so that load cannot use up the
	// budget that records rejected credentials from the same address.
	busyAuditLimit *auth.RateLimiter
	// budget admits uploads by their estimated memory.
	budget *memBudget
	// dbSlots limits the uploads that hold a database connection to store
	// their scan, so uploads alone cannot take the whole pool.
	dbSlots chan struct{}
	// freeMemory returns unused heap memory to the operating system. It is a
	// field so that tests can observe it.
	freeMemory func()
	trimming   atomic.Bool
	// openWarned is set while the open-to-everyone warning has been logged
	// for the current settings, so it is logged once per change into that
	// state.
	openWarned atomic.Bool
}

// New returns an Ingest that stores uploads in pool.
func New(pool *pgxpool.Pool, cfg SettingsSource, audit auth.AuditRecorder, opts Options) *Ingest {
	if opts.MaxReportBytes <= 0 {
		opts.MaxReportBytes = DefaultMaxReportBytes
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = http.DefaultClient
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.MemoryBudget <= 0 {
		opts.MemoryBudget = budgetFromLimit()
	}
	if opts.ReadTimeout <= 0 {
		opts.ReadTimeout = DefaultReadTimeout
	}
	if opts.AdmitWait <= 0 {
		opts.AdmitWait = DefaultAdmitWait
	}
	if opts.ProjectLimiter == nil {
		opts.ProjectLimiter = auth.NewRateLimiter(DefaultProjectEvery, DefaultProjectBurst, opts.Now)
	}
	if opts.AuditLimiter == nil {
		opts.AuditLimiter = auth.NewRateLimiter(DefaultAuditEvery, DefaultAuditBurst, opts.Now)
	}
	if opts.DBSlots <= 0 {
		opts.DBSlots = 4
		if pool != nil {
			opts.DBSlots = max(int(pool.Config().MaxConns)/4, 2)
		}
	}
	i := &Ingest{
		cfg: cfg, audit: audit, opts: opts, log: opts.Logger,
		ver:            &verifier{audience: opts.Audience, client: opts.HTTPClient, now: opts.Now},
		store:          &pgSaver{pool: pool, log: opts.Logger},
		auditLimit:     opts.AuditLimiter,
		busyAuditLimit: auth.NewRateLimiter(DefaultAuditEvery, DefaultAuditBurst, opts.Now),
		budget:         newMemBudget(opts.MemoryBudget),
		dbSlots:        make(chan struct{}, opts.DBSlots),
		freeMemory:     debug.FreeOSMemory,
	}
	i.checkOpen(cfg.Current())
	// The settings service calls back after every change; a source without
	// OnChange is checked when an upload arrives instead.
	if n, ok := cfg.(interface{ OnChange(func(settings.Snapshot)) }); ok {
		n.OnChange(i.checkOpen)
	}
	return i
}

// checkOpen logs a warning when GitLab is configured and no namespace is
// allowed, which lets every project of the GitLab instance upload. It warns
// once for each time the settings change into that state.
func (i *Ingest) checkOpen(snap settings.Snapshot) {
	if snap.GitLabURL == "" || len(snap.IngestAllowedNamespaces) > 0 {
		i.openWarned.Store(false)
		return
	}
	if !i.openWarned.Swap(true) {
		i.log.Warn("ingest accepts uploads from every project of the GitLab instance; set ingest.allowed_namespaces to restrict it")
	}
}

// Handlers returns the handlers by OpenAPI operation ID.
func (i *Ingest) Handlers() map[string]http.Handler {
	return map[string]http.Handler{"ingestTrivy": http.HandlerFunc(i.ingestTrivy)}
}

// rejection is a refused upload.
type rejection struct {
	status  int
	code    string
	message string
}

func (i *Ingest) ingestTrivy(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	ctx := r.Context()
	ip := i.clientIP(r)
	var claims *Claims // nil until the token is verified
	// admitted is set once the token, the namespace allow-list and the
	// protected-ref check have passed and an allow-list is set: the project
	// is one this instance chose to accept. Until then, and always without an
	// allow-list, a rejection can be caused by anyone who can get an ID token
	// from the GitLab instance, so its audit records are bounded per client
	// address.
	admitted := false

	reject := func(status int, code, message string, extra ...any) {
		i.reject(ctx, w, ip, claims, !admitted, status, code, message, extra...)
	}
	// busy answers an upload that waited for memory or a database slot in
	// vain, also when the client has already gone (the answer then costs
	// nothing). Busy answers happen under load, so they are recorded within a
	// per-address bound of their own, which leaves the bound for rejected
	// credentials untouched.
	busy := func(attr string, value any) {
		const msg = "The server is processing other uploads. Retry later."
		w.Header().Set("Retry-After", strconv.Itoa(busyRetryAfter))
		extra := []any{attr, value, "client_gone", ctx.Err() != nil}
		if ok, _ := i.busyAuditLimit.Allow(limitKey(ip)); ok {
			i.reject(ctx, w, ip, claims, false, http.StatusServiceUnavailable, codeBusy, msg, extra...)
			return
		}
		httpapi.WriteError(w, http.StatusServiceUnavailable, codeBusy, msg)
		i.log.DebugContext(ctx, "upload busy; not recorded: too many from this address", append([]any{"ip", ipString(ip)}, extra...)...)
	}

	if l := i.opts.Limiter; l != nil {
		if ok, wait := l.Allow(limitKey(ip)); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(retrySeconds(wait)))
			// Neither audited nor logged above debug level: the client needs
			// no credentials to cause it, so it could fill the audit log.
			i.log.DebugContext(ctx, "upload rate limited", "ip", ipString(ip))
			httpapi.WriteError(w, http.StatusTooManyRequests, codeRateLimited, "Too many uploads from this address. Retry later.")
			return
		}
	}

	snap := i.cfg.Current()
	i.checkOpen(snap)
	gitlabURL := strings.TrimRight(snap.GitLabURL, "/")
	if gitlabURL == "" {
		httpapi.WriteError(w, http.StatusServiceUnavailable, codeNotConfigured,
			"Uploads are not enabled: the GitLab URL is not configured in Scanopsis.")
		return
	}
	if i.opts.Audience == "" {
		httpapi.WriteError(w, http.StatusServiceUnavailable, codeNotConfigured,
			"Uploads are not enabled: the Scanopsis public URL is not configured.")
		return
	}

	claims, te := i.ver.verify(ctx, gitlabURL, r.Header.Get("Authorization"))
	if te != nil {
		if te.unavailable {
			// Not the pipeline's fault: say so instead of calling the token
			// invalid. It is still audited, with its own reason. The URL and
			// the cause stay in the server log; the caller gets neither.
			reject(http.StatusServiceUnavailable, codeGitLabUnavailable, te.msg+" Try again later.",
				"gitlab_url", gitlabURL, "cause", te.detail)
			return
		}
		reject(http.StatusUnauthorized, codeInvalidToken, te.msg, "gitlab_url", gitlabURL)
		return
	}

	if len(claims.Ref) > maxRefLen || clip(claims.Ref, len(claims.Ref)) != claims.Ref {
		i.log.InfoContext(ctx, "ref clipped for storage", "project", claims.ProjectPath, "job_id", claims.JobID, "length", len(claims.Ref))
		claims.Ref = clip(claims.Ref, maxRefLen)
	}

	if len(snap.IngestAllowedNamespaces) > 0 && !namespaceAllowed(snap.IngestAllowedNamespaces, claims.NamespacePath) {
		reject(http.StatusForbidden, codeNamespace,
			"The project namespace "+claims.NamespacePath+" is not in the allowed namespaces of this Scanopsis.")
		return
	}
	if snap.IngestProtectedRefsOnly && !claims.RefProtected {
		reject(http.StatusForbidden, codeRefProtected,
			"Only protected branches and tags may upload, and "+claims.Ref+" is not protected.")
		return
	}
	admitted = len(snap.IngestAllowedNamespaces) > 0

	// A project is limited by its own rate, before its body is read. The
	// rejection is audited, bounded per client address like those of a
	// project that was not checked against an allow-list: a project that goes
	// on uploading must not fill the audit log either.
	if ok, wait := i.opts.ProjectLimiter.Allow(strconv.FormatInt(claims.ProjectID, 10)); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(retrySeconds(wait)))
		i.reject(ctx, w, ip, claims, true, http.StatusTooManyRequests, codeProjectLimited,
			"This project uploaded too often. Retry later.", "project_id", claims.ProjectID)
		return
	}

	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		reject(http.StatusUnsupportedMediaType, codeMediaType, "Content-Type must be application/json.")
		return
	}
	var gz bool
	switch enc := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))); enc {
	case "", "identity":
	case "gzip":
		gz = true
	default:
		reject(http.StatusUnsupportedMediaType, codeMediaType, "Content-Encoding must be gzip or identity.")
		return
	}
	kind := r.URL.Query().Get("kind")
	switch kind {
	case "":
		kind = KindImage
	case KindImage, KindSecretFS:
	default:
		reject(http.StatusBadRequest, codeInvalidParam, "kind must be image or secret-fs.")
		return
	}
	limit := i.opts.MaxReportBytes
	if r.ContentLength > limit {
		reject(http.StatusRequestEntityTooLarge, codeTooLarge, tooLargeMessage(limit))
		return
	}

	// An upload is admitted by the memory it is estimated to need, which
	// grows with its size, not by a count: many small reports are processed
	// at once and a few large ones are. The memory is held from before the
	// body is read until the scan is stored, so a slow client holds only the
	// share its own report needs, and for no longer than the read deadline.
	// Waiting is bounded, so clients are never queued for ever.
	weight := uploadWeight(r.ContentLength, limit)
	wctx, cancel := context.WithTimeout(ctx, i.opts.AdmitWait)
	release, err := i.budget.acquire(wctx, weight)
	cancel()
	if err != nil {
		busy("budget_bytes", i.opts.MemoryBudget)
		return
	}
	defer func() {
		release()
		i.trim(weight)
	}()

	// The server's own deadlines (30 s to read, 60 s to write) fit small
	// requests, not a report of tens of megabytes over a slow link. This
	// route sets its own, now that the upload holds its memory. A response
	// writer that cannot (a test recorder) keeps the server's.
	rc := http.NewResponseController(w)
	deadline := time.Now().Add(i.opts.ReadTimeout)
	if err := rc.SetReadDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
		i.log.WarnContext(ctx, "cannot set the read deadline of an upload", "error", err)
	}
	if err := rc.SetWriteDeadline(deadline.Add(writeGrace)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		i.log.WarnContext(ctx, "cannot set the write deadline of an upload", "error", err)
	}

	rep, raw, replay, err := i.readReport(w, r, gz, limit)
	if err != nil {
		rj := classifyRead(err, gz, limit)
		if rj.status == http.StatusRequestTimeout {
			// The rest of the body is still on its way, or never comes.
			w.Header().Set("Connection", "close")
		}
		reject(rj.status, rj.code, rj.message)
		return
	}

	now := i.opts.Now()
	scanned := rep.CreatedAt
	// The uploader controls CreatedAt: scanned_at is the earlier of it and the
	// server time. A time in the past only makes the scan look older, but one
	// in the future would keep it "current" for ever and order it after real,
	// later scans.
	switch {
	case scanned.IsZero():
		scanned = now
		i.log.InfoContext(ctx, "report has no usable CreatedAt; using the server time", "project", claims.ProjectPath, "job_id", claims.JobID)
	case scanned.After(now):
		i.log.InfoContext(ctx, "report CreatedAt is in the future; using the server time",
			"project", claims.ProjectPath, "job_id", claims.JobID, "created_at", scanned.UTC().Format(time.RFC3339))
		scanned = now
	}
	// The memory budget admits many small uploads at once, and each needs a
	// pooled connection to store its scan, so the database phase has its own
	// limit, taken only now that the body has been read and checked. It waits
	// as long as admission does and answers busy in the same way.
	dctx, dcancel := context.WithTimeout(ctx, i.opts.AdmitWait)
	select {
	case i.dbSlots <- struct{}{}:
		dcancel()
	case <-dctx.Done():
		dcancel()
		busy("db_slots", cap(i.dbSlots))
		return
	}
	res, err := func() (Result, error) {
		defer func() { <-i.dbSlots }()
		return i.store.save(ctx, &saveInput{claims: claims, kind: kind, report: rep, raw: raw, findings: replay, scanTime: scanned})
	}()
	switch {
	case errors.Is(err, errServiceArchived):
		reject(http.StatusConflict, codeArchived, "The service for this project is archived. Ask an administrator to unarchive it.")
		return
	case errors.Is(err, errJobArtifacts):
		w.Header().Set("Retry-After", strconv.Itoa(jobLimitedRetryAfter))
		i.reject(ctx, w, ip, claims, true, http.StatusTooManyRequests, codeJobLimited,
			"This job already uploaded "+strconv.Itoa(MaxArtifactsPerJob)+" different artifacts, which is the most one job may upload. Uploading again one of them replaces it.",
			"job_artifacts", MaxArtifactsPerJob)
		return
	case errors.Is(err, errPathTaken):
		reject(http.StatusConflict, codeConflict,
			"A different service already uses the project path "+claims.ProjectPath+". Archive or delete it in Scanopsis.")
		return
	case isUnavailable(ctx, err):
		// Neither the client's fault nor a bug. It is not audited (the audit
		// log is in the same database) and the client is told to retry.
		i.log.ErrorContext(ctx, "cannot store scan: the database is not available",
			"project", claims.ProjectPath, "job_id", claims.JobID, "error", err)
		w.Header().Set("Retry-After", strconv.Itoa(unavailableRetryAfter))
		httpapi.WriteError(w, http.StatusServiceUnavailable, codeUnavailable,
			"The database is not available, so the report was not stored. Retry later.")
		return
	case err != nil:
		i.log.ErrorContext(ctx, "cannot store scan", "project", claims.ProjectPath, "job_id", claims.JobID, "error", err)
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "Something went wrong. Try again later.")
		return
	}
	i.log.InfoContext(ctx, "scan ingested",
		"project", claims.ProjectPath, "project_id", claims.ProjectID, "pipeline_id", claims.PipelineID,
		"job_id", claims.JobID, "artifact", rep.ArtifactName, "kind", kind, "scan_id", res.ScanID,
		"service_id", res.ServiceID, "service_created", res.ServiceCreated, "replaced", res.Replaced,
		"critical", res.Counts.Critical, "high", res.Counts.High, "medium", res.Counts.Medium,
		"low", res.Counts.Low, "unknown", res.Counts.Unknown, "fixable", res.Fixable,
		"secrets", res.Secrets, "suppressed", res.Suppressed,
		"findings", rep.Counts.total(), "stored_bytes", len(raw), "duration_ms", time.Since(started).Milliseconds())
	httpapi.WriteJSON(w, http.StatusCreated, res)
}

// trimThreshold is the estimated memory of an upload from which the heap is
// returned to the operating system when the server falls idle.
const trimThreshold = 16 << 20

// trim returns the heap to the operating system after a large upload when no
// upload is running any more. Without it the garbage of a burst stays in the
// process until the runtime's own two-minute cycle, so the container looks
// busy for long after the last upload. At most one runs at a time.
func (i *Ingest) trim(weight int64) {
	if weight < trimThreshold || i.budget.inUse() != 0 || !i.trimming.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer i.trimming.Store(false)
		i.freeMemory()
	}()
}

// reject answers with an error, writes the audit event and logs it. claims is
// nil when the token was not valid. extra are log attributes for the server
// log only. A bounded rejection (one before the project passed the allow-list
// checks) is audited and logged as a warning for at most a bounded number per
// client address, and logged at debug level beyond that; any other rejection
// is always recorded. The response is the same either way.
func (i *Ingest) reject(ctx context.Context, w http.ResponseWriter, ip netip.Addr, claims *Claims, bounded bool, status int, code, message string, extra ...any) {
	httpapi.WriteError(w, status, code, message)
	actor := "ci:unknown"
	detail := map[string]any{"reason": code, "ip": ipString(ip)}
	attrs := []any{"status", status, "reason", code, "ip", ipString(ip)}
	if claims != nil {
		actor = "ci:" + claims.ProjectPath
		detail["project_path"] = claims.ProjectPath
		attrs = append(attrs, "project", claims.ProjectPath, "job_id", claims.JobID)
	}
	attrs = append(attrs, extra...)
	if bounded {
		if ok, _ := i.auditLimit.Allow(limitKey(ip)); !ok {
			i.log.DebugContext(ctx, "upload rejected; not recorded: too many rejections from this address", attrs...)
			return
		}
	}
	// The request may already be over (a client that stopped waiting for a
	// slot), so the record does not depend on it, but it cannot hold the
	// request for long either.
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), auditTimeout)
	defer cancel()
	i.audit.Record(actx, actor, audit.ActionIngestRejected, detail)
	i.log.WarnContext(ctx, "upload rejected", attrs...)
}

var errTooLarge = errors.New("report too large")

// limitReader fails with errTooLarge once more than max bytes were read.
type limitReader struct {
	r    io.Reader
	max  int64
	read int64
}

func (l *limitReader) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	l.read += int64(n)
	if l.read > l.max {
		return 0, errTooLarge
	}
	return n, err
}

// errTrailingData means bytes follow the gzip stream of an upload.
var errTrailingData = errors.New("data after the gzip stream")

// readReport reads the body and returns what the report says, without its
// findings, with the gzip-compressed body and a function that reads the
// findings again from it.
//
// The body is kept compressed and is never held decompressed: a gzip upload
// is kept byte for byte, anything else is compressed while it is read. The
// compressed form is stored with the scan anyway, so holding it costs nothing
// extra. One parse over it validates the report, counts the findings and
// checks the limits, before the database is touched. The findings themselves
// are read a second time, straight into the database (see pgSaver), so the
// memory of an upload is the compressed body and a few buffers whatever the
// number of findings. At most limit bytes are read from the body and at most
// limit decompressed.
func (i *Ingest) readReport(w http.ResponseWriter, r *http.Request, gz bool, limit int64) (*Report, []byte, func(sink) error, error) {
	body := http.MaxBytesReader(w, r.Body, limit)
	var stored bytes.Buffer
	if gz {
		if n := r.ContentLength; n > 0 && n <= limit {
			stored.Grow(int(n))
		}
		if _, err := io.Copy(&stored, body); err != nil {
			return nil, nil, nil, err
		}
	} else {
		zw, err := gzip.NewWriterLevel(&stored, gzip.BestSpeed)
		if err != nil {
			return nil, nil, nil, err
		}
		if _, err := io.Copy(zw, &limitReader{r: body, max: limit}); err != nil {
			return nil, nil, nil, err
		}
		if err := zw.Close(); err != nil {
			return nil, nil, nil, err
		}
	}
	raw := stored.Bytes()
	rep := &Report{}
	if err := parseGzip(raw, limit, nopSink{}, false, rep); err != nil {
		return nil, nil, nil, err
	}
	// The first parse found whether the findings may be handed on as they are
	// read; if not, the second holds one result at a time, as Parse does.
	buffer := !rep.ordered
	replay := func(s sink) error {
		return parseGzip(raw, limit, s, buffer, &Report{})
	}
	return rep, raw, replay, nil
}

// parseGzip parses a gzip-compressed report held in memory. A decompressed
// size above limit is errTooLarge; data after the gzip stream is
// errTrailingData.
func parseGzip(raw []byte, limit int64, s sink, buffer bool, rep *Report) error {
	br := bytes.NewReader(raw)
	zr, err := gzip.NewReader(br)
	if err != nil {
		return err
	}
	// Parse stops at the end of the stream, so the checksum was checked.
	if _, err := parseTo(&limitReader{r: zr, max: limit}, MaxFindings, s, buffer, rep); err != nil {
		return err
	}
	if br.Len() != 0 {
		return errTrailingData
	}
	return nil
}

func classifyRead(err error, gz bool, limit int64) rejection {
	var tooBig *http.MaxBytesError
	var netErr net.Error
	switch {
	case errors.Is(err, os.ErrDeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return rejection{http.StatusRequestTimeout, codeTimeout,
			"The body did not arrive in time. Upload it faster, or retry."}
	case errors.Is(err, errTooLarge), errors.As(err, &tooBig):
		return rejection{http.StatusRequestEntityTooLarge, codeTooLarge, tooLargeMessage(limit)}
	case errors.Is(err, ErrTooManyFindings):
		return rejection{http.StatusRequestEntityTooLarge, codeTooLarge,
			"The report has too many findings: at most " + strconv.Itoa(MaxFindings) + " vulnerabilities and secrets (suppressed ones included) are accepted."}
	case errors.Is(err, ErrNotTrivy):
		return rejection{http.StatusUnprocessableEntity, codeNotTrivy,
			"This is not a Trivy JSON report (SchemaVersion 2): " + strings.TrimPrefix(err.Error(), ErrNotTrivy.Error()+": ")}
	case errors.Is(err, ErrInvalidJSON):
		return rejection{http.StatusBadRequest, codeInvalidReport, "The body is not valid JSON: " + strings.TrimPrefix(err.Error(), ErrInvalidJSON.Error()+": ")}
	case gz:
		return rejection{http.StatusBadRequest, codeInvalidReport, "The body is not a complete, valid gzip stream."}
	}
	return rejection{http.StatusBadRequest, codeInvalidReport, "The request body could not be read completely."}
}

func tooLargeMessage(limit int64) string {
	return "The report is larger than the limit of " + strconv.FormatInt(limit, 10) + " bytes (decompressed)."
}

func (i *Ingest) clientIP(r *http.Request) netip.Addr {
	if i.opts.ClientIP != nil {
		return i.opts.ClientIP(r)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a, _ := netip.ParseAddr(host)
	return a
}

// limitKey is the rate-limit key for a client: an IPv4 address whole, an
// IPv6 address by its /64, because one client usually controls a whole /64.
func limitKey(a netip.Addr) string {
	a = a.Unmap()
	switch {
	case !a.IsValid():
		return "unknown"
	case a.Is4():
		return a.String()
	}
	return netip.PrefixFrom(a, 64).Masked().String()
}

func ipString(a netip.Addr) string {
	if !a.IsValid() {
		return ""
	}
	return a.Unmap().String()
}

// retrySeconds is wait as a Retry-After value: whole seconds, at least one.
func retrySeconds(wait time.Duration) int {
	secs := int(wait / time.Second)
	if wait%time.Second != 0 {
		secs++
	}
	return max(1, secs)
}
