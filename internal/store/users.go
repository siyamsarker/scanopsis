// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// guardLastAdmin locks the enabled administrators and returns ErrLastAdmin
// if id is the only one. Callers run it first in the transaction that
// disables, demotes or deletes the user.
func guardLastAdmin(ctx context.Context, q *Queries, id int64) error {
	admins, err := q.LockAdmins(ctx)
	if err != nil {
		return err
	}
	if len(admins) == 1 && admins[0] == id {
		return ErrLastAdmin
	}
	return nil
}

// SetUserDisabled disables or enables a user. Disabling refuses the last
// enabled administrator and ends every session of the user, in the same
// transaction. It returns ErrNotFound for an unknown user.
func SetUserDisabled(ctx context.Context, pool *pgxpool.Pool, id int64, disabled bool) (User, error) {
	var out User
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		q := New(tx)
		if disabled {
			if err := guardLastAdmin(ctx, q, id); err != nil {
				return err
			}
		}
		var err error
		out, err = q.SetUserDisabled(ctx, SetUserDisabledParams{ID: id, Disabled: disabled})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil || !disabled {
			return err
		}
		return q.DeleteUserSessions(ctx, id)
	})
	return out, err
}

// DeleteUser removes a user and returns the removed row. Sessions and
// recovery codes are removed with it. It refuses the last enabled
// administrator and returns ErrNotFound for an unknown user.
func DeleteUser(ctx context.Context, pool *pgxpool.Pool, id int64) (User, error) {
	var out User
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		q := New(tx)
		if err := guardLastAdmin(ctx, q, id); err != nil {
			return err
		}
		var err error
		out, err = q.DeleteUser(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	return out, err
}
