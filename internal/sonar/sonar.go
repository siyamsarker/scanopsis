// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package sonar

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/settings"
	"github.com/siyamsarker/scanopsis/internal/store"
)

const queueSize = 100

// SettingsSource provides the current settings.
type SettingsSource interface{ Current() settings.Snapshot }

// Options configure a Sonar. All fields are optional.
type Options struct {
	// HTTPClient is used for Web API calls, for example with a custom CA
	// pool. Nil means a default client.
	HTTPClient *http.Client
	Logger     *slog.Logger
	Now        func() time.Time
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
	// tick is how often Run checks whether a sync is due.
	tick time.Duration
	// minInterval is the shortest sync interval honoured.
	minInterval time.Duration

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
