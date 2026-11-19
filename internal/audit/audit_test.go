// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/siyamsarker/scanopsis/internal/store"
)

type fakeInserter struct {
	got []store.InsertAuditParams
	err error
}

func (f *fakeInserter) InsertAudit(_ context.Context, arg store.InsertAuditParams) error {
	f.got = append(f.got, arg)
	return f.err
}

func TestRecord(t *testing.T) {
	f := &fakeInserter{}
	New(f, slog.New(slog.DiscardHandler)).Record(t.Context(), "alice", ActionRoleChanged, map[string]any{"user_id": 7, "role": "admin"})
	if len(f.got) != 1 {
		t.Fatalf("got %d inserts", len(f.got))
	}
	e := f.got[0]
	if e.Actor != "alice" || e.Action != "user.role_changed" || string(e.Detail) != `{"role":"admin","user_id":7}` {
		t.Fatalf("unexpected event %+v detail=%s", e, e.Detail)
	}
}

func TestRecordNilDetail(t *testing.T) {
	f := &fakeInserter{}
	New(f, slog.New(slog.DiscardHandler)).Record(t.Context(), "system", ActionLogout, nil)
	if string(f.got[0].Detail) != "{}" {
		t.Fatalf("detail = %s, want {}", f.got[0].Detail)
	}
}

func TestRecordLogsFailure(t *testing.T) {
	var buf bytes.Buffer
	f := &fakeInserter{err: errors.New("db down")}
	New(f, slog.New(slog.NewTextHandler(&buf, nil))).Record(t.Context(), "bob", ActionLoginFailed, nil)
	if !strings.Contains(buf.String(), "cannot write audit event") || !strings.Contains(buf.String(), "db down") {
		t.Fatalf("failure not logged: %s", buf.String())
	}
}
