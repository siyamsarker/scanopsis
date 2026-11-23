// Copyright 2026 Siyam Sarker
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
	ActionMFALocked       = "mfa.locked"
	ActionSettingsChanged = "settings.changed"
	ActionSetupCompleted  = "setup.completed"
	ActionPasswordChanged = "user.password_changed"
	ActionPasswordReset   = "user.password_reset"

	// An administrator disabled, enabled or deleted a user. The detail has
	// "user_id", "username" and "source".
	ActionUserDisabled = "user.disabled"
	ActionUserEnabled  = "user.enabled"
	ActionUserDeleted  = "user.deleted"

	// ActionSessionRevoked: users ended their own sessions. The detail has
	// "scope" ("one" with "session_id", or "others" with "count") and "ip".
	ActionSessionRevoked = "session.revoked"

	// Scan ingestion and services. ingest.rejected details carry "reason"
	// and, when the token was valid, "project_path".
	ActionIngestRejected = "ingest.rejected"
	ActionServiceCreated = "service.created"
	ActionServiceUpdated = "service.updated"
	ActionServiceDeleted = "service.deleted"

	// A SonarQube webhook that was refused. The actor is "sonarqube:unknown";
	// the detail carries "reason" and "ip".
	ActionWebhookRejected = "webhook.rejected"

	// An original scan report was exported with "scanopsis admin
	// export-report". The actor is "cli"; the detail carries only "scan_id".
	ActionReportExported = "report.exported"

	// Data retention. The actor is "retention"; the detail carries the
	// period, the cutoffs and how many rows of each kind were deleted.
	ActionRetentionDeleted = "retention.deleted"

	// Backups. backup.created details carry "name", "kind" and "size";
	// backup.failed carries "kind" and "error"; backup.restore_failed
	// and backup.restore_incomplete (the restore committed, but the server
	// must restart to finish it) carry "name" and "error". The actor is
	// who started the job, or "scheduler" for scheduled backups.
	// backup.uploaded carries "name", "original_name" and "size" (the
	// actor is "setup" for an upload on the setup screen);
	// backup.downloaded carries "name", "size" and the first and last byte
	// sent as "start" and "end" (with "ranges", their number, when several
	// ranges were sent); backup.deleted carries "name" and "size".
	ActionBackupCreated           = "backup.created"
	ActionBackupFailed            = "backup.failed"
	ActionBackupRestoreFailed     = "backup.restore_failed"
	ActionBackupRestoreIncomplete = "backup.restore_incomplete"
	ActionBackupUploaded          = "backup.uploaded"
	ActionBackupDownloaded        = "backup.downloaded"
	ActionBackupDeleted           = "backup.deleted"
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
