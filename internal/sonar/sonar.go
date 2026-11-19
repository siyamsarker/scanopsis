// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package sonar

import (
	"context"
	"log/slog"
	"net/http"
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

	mu         sync.Mutex
	version    string
	lastSyncAt time.Time
	lastErr    string
}

// New returns a Sonar that stores results in pool.
func New(pool *pgxpool.Pool, cfg SettingsSource, opts Options) *Sonar {
	s := &Sonar{
		pool: pool, q: store.New(pool), cfg: cfg,
		hc: opts.HTTPClient, log: opts.Logger, now: opts.Now,
		queue: make(chan delivery, queueSize),
		tick:  10 * time.Second,
	}
	if s.hc == nil {
		s.hc = &http.Client{}
	}
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
		"sonarWebhook": http.HandlerFunc(s.webhook),
	}
}

// Run processes webhook deliveries until ctx ends.
func (s *Sonar) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case d := <-s.queue:
			s.process(ctx, d)
		}
	}
}

func (s *Sonar) client(snap settings.Snapshot) *client {
	return &client{http: s.hc, base: snap.SonarURL, token: snap.SonarToken}
}
