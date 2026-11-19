// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/siyamsarker/scanopsis/internal/audit"
	"github.com/siyamsarker/scanopsis/internal/auth"
	"github.com/siyamsarker/scanopsis/internal/settings"
)

type auditEvent struct {
	actor, action string
	detail        map[string]any
}

type recorder struct {
	mu     sync.Mutex
	events []auditEvent
}

func (r *recorder) Record(_ context.Context, actor, action string, detail map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, auditEvent{actor, action, detail})
}

func (r *recorder) list() []auditEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]auditEvent(nil), r.events...)
}

type fakeSettings struct {
	mu   sync.Mutex
	snap settings.Snapshot
}

func (f *fakeSettings) Current() settings.Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snap
}

func (f *fakeSettings) set(fn func(*settings.Snapshot)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(&f.snap)
}

type fakeSaver struct {
	mu    sync.Mutex
	calls []*saveInput
	res   Result
	err   error
}

func (f *fakeSaver) save(_ context.Context, in *saveInput) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, in)
	if f.err != nil {
		return Result{}, f.err
	}
	res := f.res
	res.Counts = SeverityCounts{
		Critical: in.report.Counts.Critical, High: in.report.Counts.High, Medium: in.report.Counts.Medium,
		Low: in.report.Counts.Low, Unknown: in.report.Counts.Unknown,
	}
	res.Fixable, res.Secrets, res.Suppressed = in.report.Counts.Fixable, in.report.Counts.Secrets, in.report.Counts.Suppressed
	return res, nil
}

type hEnv struct {
	t     *testing.T
	g     *gitlab
	cfg   *fakeSettings
	audit *recorder
	save  *fakeSaver
	in    *Ingest
	logs  *bytes.Buffer
}

func newHEnv(t *testing.T, mutate func(*Options)) *hEnv {
	t.Helper()
	e := &hEnv{t: t, g: newGitLab(t), audit: &recorder{}, save: &fakeSaver{res: Result{ScanID: 7, ServiceID: 3, ServiceCreated: true}}, logs: &bytes.Buffer{}}
	e.cfg = &fakeSettings{snap: settings.Snapshot{GitLabURL: e.g.url + "/"}}
	opts := Options{
		Audience: testAudience,
		Logger:   slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		ClientIP: func(*http.Request) netip.Addr { return netip.MustParseAddr("192.0.2.10") },
	}
	if mutate != nil {
		mutate(&opts)
	}
	e.in = New(nil, e.cfg, e.audit, opts)
	e.in.store = e.save
	return e
}

type req struct {
	header      map[string]string
	body        []byte
	query       string
	noAuth      bool
	contentType *string
}

func (e *hEnv) do(r req) *httptest.ResponseRecorder {
	e.t.Helper()
	hr := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/ingest/trivy"+r.query, bytes.NewReader(r.body))
	if !r.noAuth {
		hr.Header.Set("Authorization", "Bearer "+e.g.token(e.t, nil))
	}
	ct := "application/json"
	if r.contentType != nil {
		ct = *r.contentType
	}
	if ct != "" {
		hr.Header.Set("Content-Type", ct)
	}
	for k, v := range r.header {
		hr.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.in.Handlers()["ingestTrivy"].ServeHTTP(w, hr)
	return w
}

func errCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var b struct {
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatalf("body %q: %v", w.Body, err)
	}
	return b.Error.Code
}

func gz(t testing.TB, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func gunzip(t testing.TB, b []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestIngestCreated(t *testing.T) {
	e := newHEnv(t, nil)
	body := readFixture(t, "image-suppressed.json")
	w := e.do(req{body: body})
	if w.Code != http.StatusCreated {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q", got)
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"scan_id": 7.0, "service_id": 3.0, "service_created": true, "replaced": false,
		"counts":  map[string]any{"critical": 1.0, "high": 43.0, "medium": 57.0, "low": 31.0, "unknown": 3.0},
		"fixable": 64.0, "secrets": 0.0, "suppressed": 3.0,
	}
	if !jsonEqual(got, want) {
		t.Errorf("body = %v\nwant   %v", got, want)
	}
	if len(e.save.calls) != 1 {
		t.Fatalf("%d saves", len(e.save.calls))
	}
	in := e.save.calls[0]
	if *in.claims != (Claims{ProjectID: 42, ProjectPath: "platform/payments-api", NamespacePath: "platform", PipelineID: 1001, JobID: 2002,
		Ref: "main", RefProtected: true, SHA: "0123456789abcdef0123456789abcdef01234567"}) {
		t.Errorf("claims = %+v", in.claims)
	}
	if in.kind != KindImage || in.report.ArtifactName != "python:3.9-slim-buster" {
		t.Errorf("kind %q artifact %q", in.kind, in.report.ArtifactName)
	}
	// A plain upload is stored compressed, byte for byte the same report.
	if !bytes.Equal(gunzip(t, in.raw), body) {
		t.Error("stored report differs from the upload")
	}
	if len(in.raw) >= len(body)/2 {
		t.Errorf("stored %d bytes for %d uploaded: not compressed", len(in.raw), len(body))
	}
	// The scan time comes from the report.
	if !in.scanTime.Equal(in.report.CreatedAt) || in.scanTime.IsZero() {
		t.Errorf("scanTime = %v", in.scanTime)
	}
	if len(e.audit.list()) != 0 {
		t.Errorf("audit events on success: %+v", e.audit.list())
	}
	logs := e.logs.String()
	if !strings.Contains(logs, "scan ingested") || !strings.Contains(logs, "project=platform/payments-api") ||
		!strings.Contains(logs, "job_id=2002") || !strings.Contains(logs, "artifact=python:3.9-slim-buster") {
		t.Errorf("success log: %s", logs)
	}
	if strings.Contains(logs, "Bearer") || strings.Contains(logs, "eyJ") {
		t.Errorf("token in log: %s", logs)
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func TestIngestScanTimeFallsBackToNow(t *testing.T) {
	now := time.Now().Add(time.Minute).Truncate(time.Second)
	e := newHEnv(t, func(o *Options) { o.Now = func() time.Time { return now } })
	w := e.do(req{body: []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)})
	if w.Code != http.StatusCreated || !e.save.calls[0].scanTime.Equal(now) {
		t.Fatalf("%d %v", w.Code, e.save.calls)
	}
}

func TestIngestGzipKeptAsUploaded(t *testing.T) {
	e := newHEnv(t, nil)
	body := readFixture(t, "secret-fs.json")
	packed := gz(t, body)
	w := e.do(req{body: packed, header: map[string]string{"Content-Encoding": "gzip"}, query: "?kind=secret-fs"})
	if w.Code != http.StatusCreated {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	in := e.save.calls[0]
	if !bytes.Equal(in.raw, packed) {
		t.Error("an uploaded gzip stream must be stored as it is")
	}
	if in.kind != KindSecretFS || in.report.Counts.Secrets != 2 {
		t.Errorf("kind %q counts %+v", in.kind, in.report.Counts)
	}
	for _, enc := range []string{"GZIP", " gzip "} {
		if w := e.do(req{body: packed, header: map[string]string{"Content-Encoding": enc}}); w.Code != http.StatusCreated {
			t.Errorf("Content-Encoding %q: %d", enc, w.Code)
		}
	}
	if w := e.do(req{body: body, header: map[string]string{"Content-Encoding": "identity"}}); w.Code != http.StatusCreated {
		t.Errorf("identity: %d", w.Code)
	}
}

func TestIngestContentTypeVariants(t *testing.T) {
	e := newHEnv(t, nil)
	for _, ct := range []string{"application/json", "application/json; charset=utf-8", "Application/JSON"} {
		ct := ct
		if w := e.do(req{body: []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`), contentType: &ct}); w.Code != http.StatusCreated {
			t.Errorf("%q: %d", ct, w.Code)
		}
	}
}

func TestIngestRejections(t *testing.T) {
	str := func(s string) *string { return &s }
	valid := []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)
	tests := []struct {
		name   string
		mutate func(*hEnv)
		req    req
		status int
		code   string
		// project says the audit event must carry the project path.
		project bool
	}{
		{name: "no token", req: req{body: valid, noAuth: true}, status: 401, code: "invalid_token"},
		{name: "bad token", req: req{body: valid, noAuth: true, header: map[string]string{"Authorization": "Bearer abc"}}, status: 401, code: "invalid_token"},
		{name: "namespace", mutate: func(e *hEnv) {
			e.cfg.set(func(s *settings.Snapshot) { s.IngestAllowedNamespaces = []string{"security", "apps"} })
		}, req: req{body: valid}, status: 403, code: "namespace_not_allowed", project: true},
		{name: "protected refs", mutate: func(e *hEnv) {
			e.cfg.set(func(s *settings.Snapshot) { s.IngestProtectedRefsOnly = true })
		}, req: req{body: valid, header: map[string]string{"Authorization": "unset"}}, status: 403, code: "ref_not_protected", project: true},
		{name: "content type", req: req{body: valid, contentType: str("text/plain")}, status: 415, code: "unsupported_media_type", project: true},
		{name: "no content type", req: req{body: valid, contentType: str("")}, status: 415, code: "unsupported_media_type", project: true},
		{name: "encoding", req: req{body: valid, header: map[string]string{"Content-Encoding": "br"}}, status: 415, code: "unsupported_media_type", project: true},
		{name: "kind", req: req{body: valid, query: "?kind=sbom"}, status: 400, code: "invalid_parameter", project: true},
		{name: "invalid json", req: req{body: []byte(`{"SchemaVersion":`)}, status: 400, code: "invalid_report", project: true},
		{name: "empty body", req: req{body: nil}, status: 400, code: "invalid_report", project: true},
		{name: "trailing data", req: req{body: append(append([]byte{}, valid...), []byte(`{}`)...)}, status: 400, code: "invalid_report", project: true},
		{name: "not gzip", req: req{body: valid, header: map[string]string{"Content-Encoding": "gzip"}}, status: 400, code: "invalid_report", project: true},
		{name: "truncated gzip", req: req{body: gz(t, valid)[:20], header: map[string]string{"Content-Encoding": "gzip"}}, status: 400, code: "invalid_report", project: true},
		{name: "gzip with trailing garbage", req: req{body: append(gz(t, valid), 'x', 'y', 'z'), header: map[string]string{"Content-Encoding": "gzip"}}, status: 400, code: "invalid_report", project: true},
		{name: "gzip of invalid json", req: req{body: gz(t, []byte(`nope`)), header: map[string]string{"Content-Encoding": "gzip"}}, status: 400, code: "invalid_report", project: true},
		{name: "not trivy", req: req{body: []byte(`{"hello":"world"}`)}, status: 422, code: "not_a_trivy_report", project: true},
		{name: "schema 1", req: req{body: []byte(`{"SchemaVersion":1,"Results":[]}`)}, status: 422, code: "not_a_trivy_report", project: true},
		{name: "schema 3", req: req{body: []byte(`{"SchemaVersion":3,"Results":[]}`)}, status: 422, code: "not_a_trivy_report", project: true},
		{name: "array", req: req{body: []byte(`[1]`)}, status: 422, code: "not_a_trivy_report", project: true},
		{name: "too large plain", mutate: func(e *hEnv) { e.in.opts.MaxReportBytes = 100 },
			req: req{body: append([]byte(`{"SchemaVersion":2,"ArtifactName":"`), append(bytes.Repeat([]byte("a"), 200), []byte(`"}`)...)...)}, status: 413, code: "report_too_large", project: true},
		{name: "too large decompressed", mutate: func(e *hEnv) { e.in.opts.MaxReportBytes = 1000 },
			req: req{body: gz(t, append([]byte(`{"SchemaVersion":2,"ArtifactName":"`), append(bytes.Repeat([]byte("a"), 100_000), []byte(`"}`)...)...)), header: map[string]string{"Content-Encoding": "gzip"}}, status: 413, code: "report_too_large", project: true},
		{name: "too large compressed", mutate: func(e *hEnv) { e.in.opts.MaxReportBytes = 50 },
			req: req{body: gz(t, valid), header: map[string]string{"Content-Encoding": "gzip"}}, status: 413, code: "report_too_large", project: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newHEnv(t, nil)
			if tc.mutate != nil {
				tc.mutate(e)
			}
			r := tc.req
			if tc.name == "protected refs" {
				// A token for an unprotected branch.
				r.header = nil
				r.noAuth = true
				tok := e.g.token(t, func(c map[string]any) { c["ref_protected"] = "false"; c["ref"] = "feature/x" })
				r.header = map[string]string{"Authorization": "Bearer " + tok}
			}
			w := e.do(r)
			if w.Code != tc.status || errCode(t, w) != tc.code {
				t.Fatalf("%d %s, want %d %s", w.Code, w.Body, tc.status, tc.code)
			}
			if len(e.save.calls) != 0 {
				t.Fatal("rejected upload was stored")
			}
			events := e.audit.list()
			if len(events) != 1 || events[0].action != audit.ActionIngestRejected {
				t.Fatalf("audit = %+v", events)
			}
			ev := events[0]
			wantActor, wantDetail := "ci:unknown", map[string]any{"reason": tc.code, "ip": "192.0.2.10"}
			if tc.project {
				wantActor = "ci:platform/payments-api"
				wantDetail["project_path"] = "platform/payments-api"
			}
			if ev.actor != wantActor || !jsonEqual(ev.detail, wantDetail) {
				t.Errorf("audit = %s %v, want %s %v", ev.actor, ev.detail, wantActor, wantDetail)
			}
			if b, _ := json.Marshal(ev.detail); strings.Contains(string(b), "eyJ") || strings.Contains(string(b), "Bearer") {
				t.Errorf("token in audit detail: %s", b)
			}
			logs := e.logs.String()
			if !strings.Contains(logs, "level=WARN") || !strings.Contains(logs, "upload rejected") || !strings.Contains(logs, "reason="+tc.code) {
				t.Errorf("rejection log: %s", logs)
			}
			if strings.Contains(logs, "eyJ") || strings.Contains(logs, "Bearer") {
				t.Errorf("token in log: %s", logs)
			}
			if w.Header().Get("Content-Type") != "application/json" {
				t.Errorf("Content-Type = %q", w.Header().Get("Content-Type"))
			}
		})
	}
}

func TestIngestTokenMessagesNameTheCheck(t *testing.T) {
	e := newHEnv(t, nil)
	w := e.do(req{body: []byte(`{}`), noAuth: true})
	var b struct{ Error struct{ Message string } }
	_ = json.Unmarshal(w.Body.Bytes(), &b)
	if w.Code != 401 || !strings.Contains(b.Error.Message, "Authorization header is missing") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	tok := e.g.token(t, func(c map[string]any) { c["aud"] = "https://elsewhere.example.com" })
	w = e.do(req{body: []byte(`{}`), noAuth: true, header: map[string]string{"Authorization": "Bearer " + tok}})
	_ = json.Unmarshal(w.Body.Bytes(), &b)
	if w.Code != 401 || !strings.Contains(b.Error.Message, "audience") || strings.Contains(b.Error.Message, tok) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestIngestNotConfigured(t *testing.T) {
	e := newHEnv(t, nil)
	e.cfg.set(func(s *settings.Snapshot) { s.GitLabURL = "" })
	w := e.do(req{body: []byte(`{}`)})
	if w.Code != 503 || errCode(t, w) != "ingest_not_configured" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if len(e.audit.list()) != 0 {
		t.Error("503 must not be audited")
	}

	e = newHEnv(t, func(o *Options) { o.Audience = "" })
	w = e.do(req{body: []byte(`{}`)})
	if w.Code != 503 || errCode(t, w) != "ingest_not_configured" {
		t.Fatalf("no audience: %d %s", w.Code, w.Body)
	}
}

func TestIngestGitLabUnreachable(t *testing.T) {
	e := newHEnv(t, nil)
	// Nothing listens on port 1: the signing keys cannot be fetched.
	e.cfg.set(func(s *settings.Snapshot) { s.GitLabURL = "http://127.0.0.1:1" })
	w := e.do(req{body: []byte(`{}`)})
	if w.Code != 503 || errCode(t, w) != "gitlab_unavailable" || !strings.Contains(w.Body.String(), "could not fetch the signing keys") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if ev := e.audit.list(); len(ev) != 1 || ev[0].detail["reason"] != "gitlab_unavailable" {
		t.Errorf("audit = %+v, want one ingest.rejected with reason gitlab_unavailable", ev)
	}
}

func TestIngestIssuerTextCannotFakeAnOutage(t *testing.T) {
	// The issuer mismatch message contains the token's iss claim; a token
	// whose iss mimics the key-fetch message must still be a 401.
	e := newHEnv(t, nil)
	hr := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/ingest/trivy", bytes.NewReader([]byte(`{}`)))
	hr.Header.Set("Authorization", "Bearer "+e.g.token(t, func(c map[string]any) { c["iss"] = "fetching keys oidc: get keys failed" }))
	hr.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.in.Handlers()["ingestTrivy"].ServeHTTP(w, hr)
	if w.Code != 401 || errCode(t, w) != "invalid_token" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// The GitLab URL setting is read on every request.
func TestIngestFollowsSettings(t *testing.T) {
	e := newHEnv(t, nil)
	other := newGitLab(t)
	if w := e.do(req{body: []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)}); w.Code != 201 {
		t.Fatalf("%d", w.Code)
	}
	e.cfg.set(func(s *settings.Snapshot) { s.GitLabURL = other.url })
	if w := e.do(req{body: []byte(`{}`)}); w.Code != 401 {
		t.Fatalf("token of the previous GitLab accepted: %d", w.Code)
	}
	e.cfg.set(func(s *settings.Snapshot) { s.IngestAllowedNamespaces = []string{"platform"}; s.GitLabURL = e.g.url })
	if w := e.do(req{body: []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)}); w.Code != 201 {
		t.Fatalf("namespace platform: %d", w.Code)
	}
	e.cfg.set(func(s *settings.Snapshot) { s.IngestAllowedNamespaces = []string{"plat"} })
	if w := e.do(req{body: []byte(`{}`)}); w.Code != 403 {
		t.Fatalf("namespace plat must not match platform: %d", w.Code)
	}
}

func TestIngestRateLimit(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	e := newHEnv(t, func(o *Options) {
		o.Limiter = auth.NewRateLimiter(10*time.Second, 2, func() time.Time { return now })
	})
	body := []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)
	for i := range 2 {
		if w := e.do(req{body: body}); w.Code != 201 {
			t.Fatalf("request %d: %d", i, w.Code)
		}
	}
	w := e.do(req{body: body})
	if w.Code != 429 || errCode(t, w) != "too_many_requests" || w.Header().Get("Retry-After") != "10" {
		t.Fatalf("%d %q %s", w.Code, w.Header().Get("Retry-After"), w.Body)
	}
	// A rate-limited request needs no credentials, so it is neither audited
	// nor logged as a warning.
	if ev := e.audit.list(); len(ev) != 0 {
		t.Fatalf("audit = %+v", ev)
	}
	if strings.Contains(e.logs.String(), "level=WARN") {
		t.Errorf("rate limit logged as a warning: %s", e.logs)
	}
	// The limit is per address.
	e.in.opts.ClientIP = func(*http.Request) netip.Addr { return netip.MustParseAddr("192.0.2.11") }
	if w := e.do(req{body: body}); w.Code != 201 {
		t.Fatalf("other address: %d", w.Code)
	}
	// And recovers with time.
	now = now.Add(11 * time.Second)
	e.in.opts.ClientIP = func(*http.Request) netip.Addr { return netip.MustParseAddr("192.0.2.10") }
	if w := e.do(req{body: body}); w.Code != 201 {
		t.Fatalf("after waiting: %d", w.Code)
	}
}

func TestIngestRateLimitBeforeAnythingElse(t *testing.T) {
	e := newHEnv(t, func(o *Options) { o.Limiter = auth.NewRateLimiter(time.Hour, 1, nil) })
	e.cfg.set(func(s *settings.Snapshot) { s.GitLabURL = "" })
	if w := e.do(req{body: nil}); w.Code != 503 {
		t.Fatalf("%d", w.Code)
	}
	if w := e.do(req{body: nil}); w.Code != 429 {
		t.Fatalf("%d", w.Code)
	}
}

func TestIngestStorageErrors(t *testing.T) {
	body := []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)
	e := newHEnv(t, nil)
	e.save.err = errPathTaken
	w := e.do(req{body: body})
	if w.Code != 409 || errCode(t, w) != "service_conflict" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if ev := e.audit.list(); len(ev) != 1 || ev[0].detail["reason"] != "service_conflict" || ev[0].detail["project_path"] != "platform/payments-api" {
		t.Fatalf("audit = %+v", ev)
	}

	e = newHEnv(t, nil)
	e.save.err = errors.New("connection refused: secret-host")
	w = e.do(req{body: body})
	if w.Code != 500 || errCode(t, w) != "internal_error" || strings.Contains(w.Body.String(), "secret-host") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if len(e.audit.list()) != 0 || !strings.Contains(e.logs.String(), "level=ERROR") {
		t.Errorf("audit %v logs %s", e.audit.list(), e.logs)
	}
}

func TestIngestClientIPFallback(t *testing.T) {
	e := newHEnv(t, func(o *Options) { o.ClientIP = nil })
	hr := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/x", strings.NewReader("{}"))
	hr.RemoteAddr = "198.51.100.7:4711"
	if got := e.in.clientIP(hr); got != netip.MustParseAddr("198.51.100.7") {
		t.Errorf("ip = %v", got)
	}
	hr.RemoteAddr = "garbage"
	if got := e.in.clientIP(hr); got.IsValid() {
		t.Errorf("ip = %v", got)
	}
	if ipString(netip.Addr{}) != "" || ipString(netip.MustParseAddr("::ffff:192.0.2.1")) != "192.0.2.1" {
		t.Error("ipString")
	}
	for in, want := range map[string]string{
		"192.0.2.1":              "192.0.2.1",
		"::ffff:192.0.2.1":       "192.0.2.1",
		"2001:db8:1:2:3:4:5:6":   "2001:db8:1:2::/64",
		"2001:db8:1:2:ffff::abc": "2001:db8:1:2::/64",
	} {
		if got := limitKey(netip.MustParseAddr(in)); got != want {
			t.Errorf("limitKey(%s) = %s, want %s", in, got, want)
		}
	}
	if limitKey(netip.Addr{}) != "unknown" {
		t.Error("limitKey of an invalid address")
	}
}

func TestLastSegment(t *testing.T) {
	for in, want := range map[string]string{"platform/payments-api": "payments-api", "a/b/c": "c", "solo": "solo", "trailing/": "trailing/"} {
		if got := lastSegment(in); got != want {
			t.Errorf("lastSegment(%q) = %q, want %q", in, got, want)
		}
	}
}

// Nothing of the secret text, even if a report carried it, reaches the logs
// or the saved input.
func TestIngestNeverLogsReportContents(t *testing.T) {
	e := newHEnv(t, nil)
	doc := `{"SchemaVersion":2,"ArtifactName":"a","Results":[{"Target":"f","Secrets":[{"RuleID":"r","Severity":"HIGH","Match":"TOPSECRETVALUE","Code":{"Lines":[{"Content":"TOPSECRETVALUE"}]}}]}]}`
	if w := e.do(req{body: []byte(doc)}); w.Code != 201 {
		t.Fatalf("%d", w.Code)
	}
	if strings.Contains(e.logs.String(), "TOPSECRETVALUE") {
		t.Errorf("report text in log: %s", e.logs)
	}
	for _, s := range secretStrings(e.save.calls[0].report) {
		if strings.Contains(s, "TOPSECRETVALUE") {
			t.Error("report text in parsed secrets")
		}
	}
}

func TestIngestTooManyFindings(t *testing.T) {
	e := newHEnv(t, nil)
	body := `{"SchemaVersion":2,"ArtifactName":"a","Results":[{"Vulnerabilities":[` +
		strings.TrimSuffix(strings.Repeat(`{"Severity":"LOW"},`, MaxFindings+1), ",") + `]}]}`
	w := e.do(req{body: []byte(body)})
	var b struct {
		Error struct{ Code, Message string }
	}
	_ = json.Unmarshal(w.Body.Bytes(), &b)
	if w.Code != 413 || b.Error.Code != "report_too_large" || !strings.Contains(b.Error.Message, "too many findings") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if len(e.save.calls) != 0 {
		t.Error("stored")
	}
	if ev := e.audit.list(); len(ev) != 1 || ev[0].detail["reason"] != "report_too_large" {
		t.Errorf("audit = %+v", ev)
	}
}

func TestIngestScanTimeIsNeverInTheFuture(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	tests := []struct {
		name, created string
		want          time.Time
		logged        string
	}{
		{"future", now.Add(time.Hour).Format(time.RFC3339), now, "in the future"},
		{"one minute ahead", now.Add(time.Minute).Format(time.RFC3339), now, "in the future"},
		{"one second ahead", now.Add(time.Second).Format(time.RFC3339), now, "in the future"},
		{"exactly now", now.Format(time.RFC3339), now, ""},
		{"past", now.Add(-48 * time.Hour).Format(time.RFC3339), now.Add(-48 * time.Hour), ""},
		{"missing", "", now, "no usable CreatedAt"},
		{"unparsable", "yesterday", now, "no usable CreatedAt"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newHEnv(t, func(o *Options) { o.Now = func() time.Time { return now } })
			doc := `{"SchemaVersion":2,"ArtifactName":"a"`
			if tc.created != "" {
				doc += `,"CreatedAt":"` + tc.created + `"`
			}
			if w := e.do(req{body: []byte(doc + `}`)}); w.Code != 201 {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			if got := e.save.calls[0].scanTime; !got.Equal(tc.want) {
				t.Errorf("scanTime = %v, want %v", got, tc.want)
			}
			logs := e.logs.String()
			if tc.logged == "" && strings.Contains(logs, "CreatedAt") {
				t.Errorf("unexpected log: %s", logs)
			}
			if tc.logged != "" && (!strings.Contains(logs, tc.logged) || !strings.Contains(logs, "level=INFO")) {
				t.Errorf("log %q missing: %s", tc.logged, logs)
			}
		})
	}
}

func TestIngestLongRefIsClipped(t *testing.T) {
	e := newHEnv(t, nil)
	long := strings.Repeat("r", 5000)
	tok := e.g.token(t, func(c map[string]any) { c["ref"] = long })
	w := e.do(req{body: []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`), noAuth: true, header: map[string]string{"Authorization": "Bearer " + tok}})
	if w.Code != 201 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got := e.save.calls[0].claims.Ref; got != long[:maxRefLen] {
		t.Errorf("ref length %d", len(got))
	}
	if !strings.Contains(e.logs.String(), "ref clipped") {
		t.Errorf("not logged: %s", e.logs)
	}
}

func TestIngestArchivedService(t *testing.T) {
	e := newHEnv(t, nil)
	e.save.err = errServiceArchived
	w := e.do(req{body: []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)})
	var b struct {
		Error struct{ Code, Message string }
	}
	_ = json.Unmarshal(w.Body.Bytes(), &b)
	if w.Code != 409 || b.Error.Code != "service_archived" ||
		b.Error.Message != "The service for this project is archived. Ask an administrator to unarchive it." {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if ev := e.audit.list(); len(ev) != 1 || ev[0].action != audit.ActionIngestRejected || ev[0].detail["reason"] != "service_archived" ||
		ev[0].actor != "ci:platform/payments-api" {
		t.Fatalf("audit = %+v", ev)
	}
}
