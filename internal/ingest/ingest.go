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
)

// DefaultMaxConcurrentUploads is the number of uploads processed at once when
// Options.MaxConcurrentUploads is 0. Each may hold a whole report in memory.
const DefaultMaxConcurrentUploads = 4

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
	// MaxConcurrentUploads limits the uploads read and stored at the same
	// time, all addresses together. Others wait for a free slot while their
	// request lasts. 0 means DefaultMaxConcurrentUploads.
	MaxConcurrentUploads int
	// Limiter limits uploads per client address. nil means no limit.
	Limiter *auth.RateLimiter
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
	// sem holds one entry per upload being processed.
	sem chan struct{}
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
	if opts.MaxConcurrentUploads <= 0 {
		opts.MaxConcurrentUploads = DefaultMaxConcurrentUploads
	}
	if opts.AuditLimiter == nil {
		opts.AuditLimiter = auth.NewRateLimiter(DefaultAuditEvery, DefaultAuditBurst, opts.Now)
	}
	i := &Ingest{
		cfg: cfg, audit: audit, opts: opts, log: opts.Logger,
		ver:        &verifier{audience: opts.Audience, client: opts.HTTPClient, now: opts.Now},
		store:      &pgSaver{pool: pool, log: opts.Logger},
		auditLimit: opts.AuditLimiter,
		sem:        make(chan struct{}, opts.MaxConcurrentUploads),
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

	if l := i.opts.Limiter; l != nil {
		if ok, wait := l.Allow(limitKey(ip)); !ok {
			secs := max(1, int(wait/time.Second)+btoi(wait%time.Second != 0))
			w.Header().Set("Retry-After", strconv.Itoa(secs))
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

	// A report can take up to limit bytes compressed and as many parsed, so
	// only a few are processed at once. The slot is held until the scan is
	// stored; the server's read timeout bounds how long a slow client can
	// keep it while sending the body.
	select {
	case i.sem <- struct{}{}:
		defer func() { <-i.sem }()
	case <-ctx.Done():
		w.Header().Set("Retry-After", strconv.Itoa(busyRetryAfter))
		reject(http.StatusServiceUnavailable, codeBusy, "The server is processing other uploads. Retry later.", "slots", cap(i.sem))
		return
	}

	rep, raw, err := i.readReport(w, r, gz, limit)
	if err != nil {
		rj := classifyRead(err, gz, limit)
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
	res, err := i.store.save(ctx, &saveInput{claims: claims, kind: kind, report: rep, raw: raw, scanTime: scanned})
	switch {
	case errors.Is(err, errServiceArchived):
		reject(http.StatusConflict, codeArchived, "The service for this project is archived. Ask an administrator to unarchive it.")
		return
	case errors.Is(err, errPathTaken):
		reject(http.StatusConflict, codeConflict,
			"A different service already uses the project path "+claims.ProjectPath+". Archive or delete it in Scanopsis.")
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
		"vulnerabilities", len(rep.Vulns), "stored_bytes", len(raw), "duration_ms", time.Since(started).Milliseconds())
	httpapi.WriteJSON(w, http.StatusCreated, res)
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

// readReport parses the body and returns the report with its gzip-compressed
// form. A gzip upload is kept byte for byte; anything else is compressed
// while it is parsed. At most limit bytes are read from the body and at most
// limit decompressed.
func (i *Ingest) readReport(w http.ResponseWriter, r *http.Request, gz bool, limit int64) (*Report, []byte, error) {
	body := http.MaxBytesReader(w, r.Body, limit)
	var stored bytes.Buffer
	if gz {
		zr, err := gzip.NewReader(io.TeeReader(body, &stored))
		if err != nil {
			return nil, nil, err
		}
		rep, err := Parse(&limitReader{r: zr, max: limit})
		if err != nil {
			return nil, nil, err
		}
		// Parse stopped at the end of the stream, so the checksum was checked.
		// Nothing may follow the stream.
		if _, err := io.Copy(io.Discard, body); err != nil {
			return nil, nil, err
		}
		return rep, stored.Bytes(), nil
	}
	zw, err := gzip.NewWriterLevel(&stored, gzip.BestSpeed)
	if err != nil {
		return nil, nil, err
	}
	rep, err := Parse(io.TeeReader(&limitReader{r: body, max: limit}, zw))
	if err != nil {
		return nil, nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, nil, err
	}
	return rep, stored.Bytes(), nil
}

func classifyRead(err error, gz bool, limit int64) rejection {
	var tooBig *http.MaxBytesError
	switch {
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

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
