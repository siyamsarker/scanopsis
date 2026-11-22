// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/siyamsarker/scanopsis/internal/store"
)

type fakeMySessionsStore struct {
	err     error
	rows    []store.ListUserSessionsRow
	deleted int64
	list    store.ListUserSessionsParams
}

func (f *fakeMySessionsStore) ListUserSessions(_ context.Context, a store.ListUserSessionsParams) ([]store.ListUserSessionsRow, error) {
	f.list = a
	return f.rows, f.err
}

func (f *fakeMySessionsStore) DeleteUserSession(context.Context, store.DeleteUserSessionParams) (int64, error) {
	return f.deleted, f.err
}

func (f *fakeMySessionsStore) DeleteOtherUserSessions(context.Context, store.DeleteOtherUserSessionsParams) (int64, error) {
	return f.deleted, f.err
}

func serveMySessions(h http.Handler, method, id string, p *Principal) *httptest.ResponseRecorder {
	r := newRequest(method, "/api/v1/me/sessions", nil)
	r.SetPathValue("id", id)
	if p != nil {
		r = r.WithContext(WithPrincipal(r.Context(), p))
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestMySessionsErrors(t *testing.T) {
	sessions, _, clk := newTestSessions(t, true)
	fs := &fakeMySessionsStore{err: errors.New("db down")}
	audit := &fakeAudit{}
	m := NewMySessions(fs, sessions, audit, func(*http.Request) netip.Addr { return netip.Addr{} }, nil)
	p := &Principal{UserID: 7, Source: SourceLocal, Username: "root", SessionID: []byte("hash"), SessionNumber: 4}

	for name, h := range map[string]http.Handler{
		"list": m.ListHandler(), "end others": m.EndOthersHandler(), "end one": m.EndHandler(),
	} {
		if w := serveMySessions(h, http.MethodGet, "9", nil); w.Code != http.StatusUnauthorized {
			t.Errorf("%s without a principal: %d", name, w.Code)
		}
		if w := serveMySessions(h, http.MethodGet, "9", p); w.Code != http.StatusInternalServerError {
			t.Errorf("%s with a failing store: %d", name, w.Code)
		}
	}
	if fs.list.UserID != 7 || !fs.list.Now.Time.Equal(clk.Now()) || !fs.list.IdleCutoff.Time.Equal(clk.Now().Add(-testIdle)) || fs.list.MaxRows != maxListedSessions {
		t.Errorf("list parameters %+v", fs.list)
	}
	for _, id := range []string{"", "0", "-1", "x", "99999999999999999999"} {
		if w := serveMySessions(m.EndHandler(), http.MethodDelete, id, p); w.Code != http.StatusBadRequest {
			t.Errorf("id %q: %d", id, w.Code)
		}
	}
	if w := serveMySessions(m.EndHandler(), http.MethodDelete, "4", p); w.Code != http.StatusConflict {
		t.Errorf("current session: %d", w.Code)
	}
	if len(audit.events) != 0 {
		t.Errorf("failures were audited: %+v", audit.events)
	}
}
