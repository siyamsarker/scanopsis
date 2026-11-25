// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package sonar

import (
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/auth"
	"github.com/siyamsarker/scanopsis/internal/settings"
	"github.com/siyamsarker/scanopsis/internal/store"
)

const (
	// queueSize is how many verified deliveries wait for a worker. A delivery
	// is a few kilobytes, so the queue is cheap; when it is still full, the
	// delivery stays pending and the next sync reads its analysis.
	queueSize = 500
	// linkedSize is how many newly linked projects wait to be synced. A
	// project that does not fit is read by the next sync, as it is linked.
	linkedSize = 64
	// workers is how many deliveries are processed at the same time.
	workers = 4
)

// Defaults of the rejection limiter: a client address gets this many
// webhook.rejected records at once and then one more per DefaultAuditEvery.
const (
	DefaultAuditBurst = 10
	DefaultAuditEvery = time.Minute
)

// auditTimeout bounds the audit write of a rejection.
const auditTimeout = 5 * time.Second

// SettingsSource provides the current settings.
type SettingsSource interface{ Current() settings.Snapshot }

// Options configure a Sonar. All fields are optional.
type Options struct {
	// HTTPClient is used for Web API calls, for example with a custom CA
	// pool. Nil means a default client.
	HTTPClient *http.Client
	Logger     *slog.Logger
	Now        func() time.Time
	// Audit records rejected webhooks. Nil records nothing.
	Audit auth.AuditRecorder
	// ClientIP returns the client address of a request, for the audit
	// record and its limit. Nil means the address is not recorded.
	ClientIP func(*http.Request) netip.Addr
	// AuditLimiter limits how many rejections per client address are
	// written to the audit log. Nil means a burst of DefaultAuditBurst and
	// then one per DefaultAuditEvery.
	AuditLimiter *auth.RateLimiter
}

// Sonar is the SonarQube integration.
type Sonar struct {
	pool *pgxpool.Pool
	q    *store.Queries
	cfg  SettingsSource
	hc   *http.Client
	log  *slog.Logger
	now  func() time.Time

	queue chan delivery
	// linked holds projects LinkWaiting has just linked, for the workers to
	// read their newest analysis at once.
	linked chan linkedProject
	// queueWarned is when the full queue was last logged (Unix nanoseconds).
	queueWarned atomic.Int64
	// workers is how many goroutines of Run process deliveries.
	workers int

	audit      auth.AuditRecorder
	clientIP   func(*http.Request) netip.Addr
	auditLimit *auth.RateLimiter
	// tick is how often Run checks whether a sync is due.
	tick time.Duration
	// minInterval is the shortest sync interval honoured.
	minInterval time.Duration
	// beforeLinkTaken, when set by a test, runs as linkWaitingTaken starts.
	beforeLinkTaken func()
	// beforeStoreLinked, when set by a test, runs before a delivery whose
	// link found the service taken checks the service's key again.
	beforeStoreLinked func()
	// analysesLocked, when set by a test, runs in insertAnalysis once the
	// service's analyses lock is held.
	analysesLocked func()

	mu         sync.Mutex
	version    string
	lastSyncAt time.Time
	lastErr    string
	// warnedPlain is the plain http URL the TLS warning was logged for.
	warnedPlain string
	keys        keySetCache
}

// New returns a Sonar that stores results in pool.
func New(pool *pgxpool.Pool, cfg SettingsSource, opts Options) *Sonar {
	s := &Sonar{
		pool: pool, q: store.New(pool), cfg: cfg,
		hc: opts.HTTPClient, log: opts.Logger, now: opts.Now,
		queue:       make(chan delivery, queueSize),
		linked:      make(chan linkedProject, linkedSize),
		workers:     workers,
		audit:       opts.Audit,
		clientIP:    opts.ClientIP,
		auditLimit:  opts.AuditLimiter,
		tick:        10 * time.Second,
		minInterval: settings.MinSonarSyncInterval,
	}
	if s.hc == nil {
		s.hc = &http.Client{}
	}
	s.hc = sameHostClient(s.hc)
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.auditLimit == nil {
		s.auditLimit = auth.NewRateLimiter(DefaultAuditEvery, DefaultAuditBurst, s.now)
	}
	return s
}

// Handlers returns the HTTP handlers by OpenAPI operation ID.
func (s *Sonar) Handlers() map[string]http.Handler {
	return map[string]http.Handler{
		"sonarWebhook":        http.HandlerFunc(s.webhook),
		"getServiceSonar":     http.HandlerFunc(s.getServiceSonar),
		"getSonarIntegration": http.HandlerFunc(s.getSonarIntegration),
	}
}

// sameHostClient returns a copy of c that refuses redirects to another scheme
// or host, so the bearer token is never sent anywhere else.
func sameHostClient(c *http.Client) *http.Client {
	out := *c
	prev := c.CheckRedirect
	out.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if first := via[0].URL; req.URL.Scheme != first.Scheme || req.URL.Host != first.Host {
			return errors.New("refusing a redirect to another scheme or host")
		}
		if prev != nil {
			return prev(req, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &out
}

func (s *Sonar) client(snap settings.Snapshot) *client {
	s.mu.Lock()
	if strings.HasPrefix(strings.ToLower(snap.SonarURL), "http://") {
		if s.warnedPlain != snap.SonarURL {
			s.warnedPlain = snap.SonarURL
			s.log.Warn("the SonarQube token is sent without TLS; use an https:// SonarQube URL")
		}
	} else {
		s.warnedPlain = ""
	}
	s.mu.Unlock()
	cfg := s.keys.forConfig(snap.SonarURL, snap.SonarToken)
	return &client{http: s.hc, base: snap.SonarURL, token: snap.SonarToken, keys: &s.keys, cfg: cfg}
}
