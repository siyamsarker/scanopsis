// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

// Package audit writes security-relevant events to the audit_log table.
package audit

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/siyamsarker/scanopsis/internal/store"
)

// Event actions. Detail values must never contain secrets.
const (
	ActionLoginSucceeded  = "login.succeeded"
	ActionLoginFailed     = "login.failed"
	ActionLogout          = "logout"
	ActionUserCreated     = "user.created"
	ActionRoleChanged     = "user.role_changed"
	ActionMFAEnrolled     = "mfa.enrolled"
	ActionMFAVerified     = "mfa.verified"
	ActionMFAFailed       = "mfa.failed"
	ActionMFAReset        = "mfa.reset"
	ActionSettingsChanged = "settings.changed"
)

// Inserter is the query the recorder needs; *store.Queries implements it.
type Inserter interface {
	InsertAudit(ctx context.Context, arg store.InsertAuditParams) error
}

// Recorder writes audit events. Failures are logged and never returned, so
// an audit problem cannot block sign-in or administration.
type Recorder struct {
	q   Inserter
	log *slog.Logger
}

// New returns a Recorder.
func New(q Inserter, log *slog.Logger) *Recorder { return &Recorder{q: q, log: log} }

// Record writes one event.
func (r *Recorder) Record(ctx context.Context, actor, action string, detail map[string]any) {
	if detail == nil {
		detail = map[string]any{}
	}
	b, err := json.Marshal(detail)
	if err == nil {
		err = r.q.InsertAudit(ctx, store.InsertAuditParams{Actor: actor, Action: action, Detail: b})
	}
	if err != nil {
		r.log.ErrorContext(ctx, "cannot write audit event", "action", action, "error", err)
	}
}
