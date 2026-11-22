// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Probe checks that the database is reachable over one connection of its
// own, outside the pool. The readiness probe uses it so that a pool that is
// saturated by request traffic does not make a healthy server look broken,
// which could make an orchestrator restart it. The connection is opened on
// first use and again after any failure. A Probe is safe for concurrent use;
// concurrent checks take turns.
type Probe struct {
	cfg  *pgx.ConnConfig
	turn chan struct{} // holds one token: whoever has it may use conn
	conn *pgx.Conn
}

// NewProbe returns a Probe that connects the way pool does, with the same
// credentials and statement timeout. It does not connect yet.
func NewProbe(pool *pgxpool.Pool) *Probe {
	p := &Probe{cfg: pool.Config().ConnConfig.Copy(), turn: make(chan struct{}, 1)}
	p.turn <- struct{}{}
	return p
}

// Ping checks the database, connecting first if there is no usable
// connection. It returns when ctx ends. A failed check drops the connection,
// so the next one starts from a fresh connect.
func (p *Probe) Ping(ctx context.Context) error {
	select {
	case <-p.turn:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { p.turn <- struct{}{} }()

	if p.conn == nil || p.conn.IsClosed() {
		conn, err := pgx.ConnectConfig(ctx, p.cfg)
		if err != nil {
			return fmt.Errorf("connect to database: %w", err)
		}
		p.conn = conn
	}
	if err := p.conn.Ping(ctx); err != nil {
		// ctx may be the reason for the failure; closing needs its own, short one.
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		_ = p.conn.Close(closeCtx)
		cancel()
		p.conn = nil
		return fmt.Errorf("ping database: %w", err)
	}
	return nil
}

// Close closes the connection, if any. The Probe can still be used
// afterwards; it connects again.
func (p *Probe) Close(ctx context.Context) {
	select {
	case <-p.turn:
	case <-ctx.Done():
		return
	}
	defer func() { p.turn <- struct{}{} }()
	if p.conn != nil {
		_ = p.conn.Close(ctx)
		p.conn = nil
	}
}
