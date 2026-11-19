// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/siyamsarker/scanopsis/internal/store"
)

var _ AdminBootstrapStore = (*store.Queries)(nil)

type fakeAdminStore struct {
	admins   int64
	created  []store.CreateLocalUserParams
	countErr error
	makeErr  error
}

func (f *fakeAdminStore) CountAdmins(context.Context) (int64, error) { return f.admins, f.countErr }

func (f *fakeAdminStore) CreateLocalUser(_ context.Context, a store.CreateLocalUserParams) (store.User, error) {
	if f.makeErr != nil {
		return store.User{}, f.makeErr
	}
	f.created = append(f.created, a)
	f.admins++
	return store.User{ID: 1, Source: SourceLocal, Username: a.Username, Role: a.Role, PasswordHash: a.PasswordHash}, nil
}

func TestBootstrapAdminExistingAdminDoesNothing(t *testing.T) {
	s, a := &fakeAdminStore{admins: 1}, &fakeAudit{}
	created, err := BootstrapAdmin(context.Background(), s, a, "admin", "pw")
	if created || err != nil || len(s.created) != 0 || len(a.events) != 0 {
		t.Fatalf("created=%v err=%v users=%d events=%d; want nothing", created, err, len(s.created), len(a.events))
	}
}

func TestBootstrapAdminWithoutPasswordDoesNothing(t *testing.T) {
	s, a := &fakeAdminStore{}, &fakeAudit{}
	created, err := BootstrapAdmin(context.Background(), s, a, "admin", "")
	if created || err != nil || len(s.created) != 0 || len(a.events) != 0 {
		t.Fatalf("created=%v err=%v users=%d events=%d; want nothing", created, err, len(s.created), len(a.events))
	}
}

func TestBootstrapAdminCreatesAdmin(t *testing.T) {
	s, a := &fakeAdminStore{}, &fakeAudit{}
	created, err := BootstrapAdmin(context.Background(), s, a, "root", "initial-pass")
	if !created || err != nil {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if len(s.created) != 1 {
		t.Fatalf("%d users created; want 1", len(s.created))
	}
	u := s.created[0]
	if u.Username != "root" || u.Role != "admin" || !u.PasswordHash.Valid {
		t.Fatalf("user %+v", u)
	}
	if u.PasswordHash.String == "initial-pass" || !phcPattern.MatchString(u.PasswordHash.String) {
		t.Fatalf("stored hash %q is not an Argon2id hash", u.PasswordHash.String)
	}
	if ok, err := VerifyPassword(u.PasswordHash.String, "initial-pass"); err != nil || !ok {
		t.Fatalf("stored hash does not verify: %v, %v", ok, err)
	}
	want := []auditEvent{{
		Actor: "system", Action: "user.created",
		Detail: map[string]any{"username": "root", "role": "admin", "source": "local", "reason": "bootstrap"},
	}}
	if !reflect.DeepEqual(a.events, want) {
		t.Fatalf("audit %+v; want %+v", a.events, want)
	}

	// A second call finds the admin and does nothing.
	created, err = BootstrapAdmin(context.Background(), s, a, "root", "initial-pass")
	if created || err != nil || len(s.created) != 1 || len(a.events) != 1 {
		t.Fatalf("second call: created=%v err=%v users=%d events=%d", created, err, len(s.created), len(a.events))
	}
}

func TestBootstrapAdminErrors(t *testing.T) {
	boom := errors.New("boom")
	ctx := context.Background()
	a := &fakeAudit{}
	if created, err := BootstrapAdmin(ctx, &fakeAdminStore{countErr: boom}, a, "u", "p"); created || !errors.Is(err, boom) {
		t.Errorf("count failure: %v, %v", created, err)
	}
	if created, err := BootstrapAdmin(ctx, &fakeAdminStore{makeErr: boom}, a, "u", "p"); created || !errors.Is(err, boom) {
		t.Errorf("create failure: %v, %v", created, err)
	}
	s := &fakeAdminStore{}
	if created, err := BootstrapAdmin(ctx, s, a, "", "p"); created || err == nil || len(s.created) != 0 {
		t.Errorf("empty username: %v, %v", created, err)
	}
	if len(a.events) != 0 {
		t.Errorf("failures were audited as creations: %+v", a.events)
	}
}
