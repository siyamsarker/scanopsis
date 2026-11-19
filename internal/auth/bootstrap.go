// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/siyamsarker/scanopsis/internal/audit"
	"github.com/siyamsarker/scanopsis/internal/store"
)

// AdminBootstrapStore is the persistence BootstrapAdmin needs;
// *store.Queries implements it.
type AdminBootstrapStore interface {
	CountAdmins(ctx context.Context) (int64, error)
	CreateLocalUser(ctx context.Context, arg store.CreateLocalUserParams) (store.User, error)
}

// BootstrapAdmin creates the first local administrator. It does nothing if an
// administrator already exists, or if none exists and password is empty (the
// caller should warn that nobody can sign in).
func BootstrapAdmin(ctx context.Context, s AdminBootstrapStore, rec AuditRecorder, username, password string) (created bool, err error) {
	n, err := s.CountAdmins(ctx)
	if err != nil {
		return false, fmt.Errorf("count administrators: %w", err)
	}
	if n > 0 || password == "" {
		return false, nil
	}
	if username == "" {
		return false, errors.New("bootstrap administrator needs a username")
	}
	hash, err := HashPassword(ctx, password)
	if err != nil {
		return false, err
	}
	u, err := s.CreateLocalUser(ctx, store.CreateLocalUserParams{
		Username:     username,
		Role:         store.RoleAdmin,
		PasswordHash: pgtype.Text{String: hash, Valid: true},
	})
	if err != nil {
		return false, fmt.Errorf("create administrator: %w", err)
	}
	rec.Record(ctx, "system", audit.ActionUserCreated, map[string]any{
		"username": u.Username, "role": store.RoleAdmin, "source": SourceLocal, "reason": "bootstrap",
	})
	return true, nil
}
