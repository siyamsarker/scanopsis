// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/siyamsarker/scanopsis/internal/auth"
	"github.com/siyamsarker/scanopsis/internal/secretbox"
	"github.com/siyamsarker/scanopsis/internal/store"
)

// fakeStore is an in-memory Store with the semantics of the SQL queries.
type fakeStore struct {
	mu      sync.Mutex
	rows    map[string]store.Setting
	failOn  string // "upsert", "delete", "seed", "list" or ""
	listErr int    // fail the n-th ListSettings call (1-based) when > 0
	calls   int
}

func newFakeStore() *fakeStore { return &fakeStore{rows: map[string]store.Setting{}} }

func (f *fakeStore) ListSettings(context.Context) ([]store.Setting, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.failOn == "list" || f.listErr == f.calls {
		return nil, errors.New("list failed")
	}
	out := make([]store.Setting, 0, len(f.rows))
	for _, r := range f.rows {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeStore) SeedSetting(_ context.Context, a store.SeedSettingParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOn == "seed" {
		return errors.New("seed failed")
	}
	if _, ok := f.rows[a.Key]; !ok {
		f.rows[a.Key] = store.Setting{Key: a.Key, Value: a.Value, SecretEnc: a.SecretEnc, Source: "env", UpdatedBy: "system",
			UpdatedAt: pgtype.Timestamptz{Time: time.Unix(1000, 0).UTC(), Valid: true}}
	}
	return nil
}

func (f *fakeStore) UpsertSetting(_ context.Context, a store.UpsertSettingParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOn == "upsert" {
		return errors.New("upsert failed")
	}
	f.rows[a.Key] = store.Setting{Key: a.Key, Value: a.Value, SecretEnc: a.SecretEnc, Source: "ui", UpdatedBy: a.UpdatedBy,
		UpdatedAt: pgtype.Timestamptz{Time: time.Unix(2000, 0).UTC(), Valid: true}}
	return nil
}

func (f *fakeStore) DeleteSetting(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOn == "delete" {
		return errors.New("delete failed")
	}
	delete(f.rows, key)
	return nil
}

// tx runs fn and rolls the rows back when it fails.
func (f *fakeStore) tx(ctx context.Context, fn func(Store) error) error {
	f.mu.Lock()
	backup := make(map[string]store.Setting, len(f.rows))
	for k, v := range f.rows {
		backup[k] = v
	}
	f.mu.Unlock()
	if err := fn(f); err != nil {
		f.mu.Lock()
		f.rows = backup
		f.mu.Unlock()
		return err
	}
	return nil
}

type auditEvent struct {
	actor, action string
	detail        map[string]any
}

type fakeAudit struct {
	mu     sync.Mutex
	events []auditEvent
}

func (a *fakeAudit) Record(_ context.Context, actor, action string, detail map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, auditEvent{actor, action, detail})
}

func testBox(t *testing.T, fill byte) *secretbox.Box {
	t.Helper()
	b, err := secretbox.New(bytes.Repeat([]byte{fill}, secretbox.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

type env struct {
	st  *fakeStore
	rec *fakeAudit
	svc *Service
}

func newEnv(t *testing.T, seeds map[string]any) *env {
	t.Helper()
	st := newFakeStore()
	return start(t, st, seeds)
}

func start(t *testing.T, st *fakeStore, seeds map[string]any) *env {
	t.Helper()
	rec := &fakeAudit{}
	svc, err := New(context.Background(), st, st.tx, testBox(t, 1), seeds, rec, discard)
	if err != nil {
		t.Fatal(err)
	}
	return &env{st, rec, svc}
}

func (e *env) patch(t *testing.T, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/api/v1/settings", strings.NewReader(body))
	req = req.WithContext(auth.WithPrincipal(req.Context(), &auth.Principal{Source: "local", Username: "alice"}))
	rr := httptest.NewRecorder()
	e.svc.UpdateHandler().ServeHTTP(rr, req)
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("response %q: %v", rr.Body.String(), err)
	}
	if cc := rr.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
	return rr.Code, out
}

func (e *env) items(t *testing.T) map[string]map[string]any {
	t.Helper()
	rr := httptest.NewRecorder()
	e.svc.ListHandler().ServeHTTP(rr, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/settings", nil))
	if rr.Code != 200 || rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d, cache %q", rr.Code, rr.Header().Get("Cache-Control"))
	}
	var out struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	m := map[string]map[string]any{}
	for _, it := range out.Items {
		m[it["key"].(string)] = it
	}
	if len(out.Items) != len(Definitions) {
		t.Fatalf("got %d items, want %d", len(out.Items), len(Definitions))
	}
	return m
}

func gitlabSeeds() map[string]any {
	return map[string]any{
		"gitlab.url":           "https://gitlab.example.com",
		"gitlab.client_id":     "cid",
		"gitlab.client_secret": "s3cret-value",
	}
}

func TestDefaults(t *testing.T) {
	e := newEnv(t, nil)
	want := Snapshot{
		GitLabAllowedGroups: []string{}, LocalLoginEnabled: true,
		SessionIdleTimeout: 8 * time.Hour, SessionAbsoluteTimeout: 24 * time.Hour,
	}
	if got := e.svc.Current(); !reflect.DeepEqual(got, want) {
		t.Errorf("Current = %+v, want %+v", got, want)
	}
	if e.svc.Current().GitLabEnabled() {
		t.Error("GitLabEnabled with defaults")
	}
	if len(e.st.rows) != 0 {
		t.Errorf("rows = %v, want none", e.st.rows)
	}
	items := e.items(t)
	idle := items["session.idle_timeout"]
	if idle["source"] != "default" || idle["value"] != "8h" || idle["is_set"] != true || idle["updated_by"] != nil || idle["updated_at"] != nil ||
		idle["type"] != "duration" || idle["secret"] != false || idle["env_var"] != "SCANOPSIS_SESSION_IDLE_TIMEOUT" || idle["description"] == "" {
		t.Errorf("idle = %v", idle)
	}
	if g := items["gitlab.allowed_groups"]; !reflect.DeepEqual(g["value"], []any{}) || g["is_set"] != false {
		t.Errorf("groups = %v", g)
	}
	if s := items["gitlab.client_secret"]; s["value"] != nil || s["is_set"] != false || s["secret"] != true {
		t.Errorf("secret = %v", s)
	}
}

func TestSeedingFillsOnlyMissingKeys(t *testing.T) {
	st := newFakeStore()
	st.rows["gitlab.client_id"] = store.Setting{Key: "gitlab.client_id", Value: []byte(`"from-ui"`), Source: "ui", UpdatedBy: "local:bob"}
	seeds := gitlabSeeds()
	seeds["session.idle_timeout"] = 30 * time.Minute
	seeds["gitlab.allowed_groups"] = []string{"platform", "sec/appsec"}
	seeds["auth.local_login_enabled"] = true
	e := start(t, st, seeds)

	if r := st.rows["gitlab.client_id"]; string(r.Value) != `"from-ui"` || r.Source != "ui" {
		t.Errorf("existing row changed: %+v", r)
	}
	if r := st.rows["gitlab.url"]; string(r.Value) != `"https://gitlab.example.com"` || r.Source != "env" || r.UpdatedBy != "system" {
		t.Errorf("seeded url row = %+v", r)
	}
	if r := st.rows["session.idle_timeout"]; string(r.Value) != `"30m"` || r.Source != "env" {
		t.Errorf("seeded idle row = %+v", r)
	}
	if r := st.rows["gitlab.allowed_groups"]; string(r.Value) != `["platform","sec/appsec"]` {
		t.Errorf("seeded groups row = %+v", r)
	}
	if _, ok := st.rows["session.absolute_timeout"]; ok {
		t.Error("unset env variable must not create a row")
	}
	want := Snapshot{
		GitLabURL: "https://gitlab.example.com", GitLabClientID: "from-ui", GitLabClientSecret: "s3cret-value",
		GitLabAllowedGroups: []string{"platform", "sec/appsec"}, LocalLoginEnabled: true,
		SessionIdleTimeout: 30 * time.Minute, SessionAbsoluteTimeout: 24 * time.Hour,
	}
	if got := e.svc.Current(); !reflect.DeepEqual(got, want) {
		t.Errorf("Current = %+v, want %+v", got, want)
	}
	items := e.items(t)
	if items["gitlab.client_id"]["source"] != "ui" || items["gitlab.client_id"]["updated_by"] != "local:bob" {
		t.Errorf("client_id = %v", items["gitlab.client_id"])
	}
	if it := items["gitlab.url"]; it["source"] != "env" || it["updated_by"] != "system" || it["updated_at"] != "1970-01-01T00:16:40Z" {
		t.Errorf("url = %v", it)
	}
	if it := items["gitlab.client_secret"]; it["value"] != nil || it["is_set"] != true || it["source"] != "env" {
		t.Errorf("secret = %v", it)
	}
}

func TestDatabaseWinsOverChangedEnv(t *testing.T) {
	st := newFakeStore()
	start(t, st, map[string]any{"session.idle_timeout": time.Hour})
	e := start(t, st, map[string]any{"session.idle_timeout": 2 * time.Hour})
	if got := e.svc.Current().SessionIdleTimeout; got != time.Hour {
		t.Errorf("idle = %v, want 1h (database wins)", got)
	}
}

func TestResetFallsBackToEnvThenDefault(t *testing.T) {
	e := newEnv(t, map[string]any{"session.idle_timeout": time.Hour})
	if code, body := e.patch(t, `{"session.idle_timeout":"2h"}`); code != 200 {
		t.Fatalf("%d %v", code, body)
	}
	if got := e.svc.Current().SessionIdleTimeout; got != 2*time.Hour {
		t.Fatalf("idle = %v", got)
	}
	if it := e.items(t)["session.idle_timeout"]; it["source"] != "ui" || it["updated_by"] != "local:alice" {
		t.Errorf("after edit: %v", it)
	}
	// Reset: the row is deleted and the env value applies again.
	if code, body := e.patch(t, `{"session.idle_timeout":null}`); code != 200 {
		t.Fatalf("%d %v", code, body)
	}
	if _, ok := e.st.rows["session.idle_timeout"]; ok {
		t.Error("row not deleted")
	}
	if got := e.svc.Current().SessionIdleTimeout; got != time.Hour {
		t.Errorf("idle after reset = %v, want env 1h", got)
	}
	if it := e.items(t)["session.idle_timeout"]; it["source"] != "env" || it["value"] != "1h" || it["updated_by"] != nil {
		t.Errorf("after reset: %v", it)
	}

	// Without an env value the default applies.
	d := newEnv(t, nil)
	d.patch(t, `{"session.idle_timeout":"2h"}`)
	d.patch(t, `{"session.idle_timeout":null}`)
	if got := d.svc.Current().SessionIdleTimeout; got != 8*time.Hour {
		t.Errorf("idle = %v, want default 8h", got)
	}
	if it := d.items(t)["session.idle_timeout"]; it["source"] != "default" {
		t.Errorf("source = %v", it["source"])
	}
}

func TestUpdateAppliesAndAudits(t *testing.T) {
	e := newEnv(t, nil)
	var got []Snapshot
	var order []string
	e.svc.OnChange(func(s Snapshot) { got = append(got, s); order = append(order, "first") })
	e.svc.OnChange(func(Snapshot) { order = append(order, "second") })

	body := `{"gitlab.url":"https://gitlab.example.com/","gitlab.client_id":" cid ","gitlab.client_secret":"topsecret-xyz",` +
		`"gitlab.allowed_groups":["a","b/c"],"auth.local_login_enabled":false,"session.idle_timeout":"90m","session.absolute_timeout":"36h"}`
	code, resp := e.patch(t, body)
	if code != 200 {
		t.Fatalf("%d %v", code, resp)
	}
	want := Snapshot{
		GitLabURL: "https://gitlab.example.com", GitLabClientID: "cid", GitLabClientSecret: "topsecret-xyz",
		GitLabAllowedGroups: []string{"a", "b/c"}, LocalLoginEnabled: false,
		SessionIdleTimeout: 90 * time.Minute, SessionAbsoluteTimeout: 36 * time.Hour,
	}
	if cur := e.svc.Current(); !reflect.DeepEqual(cur, want) {
		t.Errorf("Current = %+v", cur)
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0], want) || !reflect.DeepEqual(order, []string{"first", "second"}) {
		t.Errorf("callbacks: %v %v", got, order)
	}
	if len(resp["items"].([]any)) != len(Definitions) {
		t.Errorf("response items = %v", resp["items"])
	}
	it := e.items(t)["session.idle_timeout"]
	if it["value"] != "1h30m" || it["source"] != "ui" {
		t.Errorf("idle item = %v", it)
	}

	if len(e.rec.events) != 1 {
		t.Fatalf("events = %v", e.rec.events)
	}
	ev := e.rec.events[0]
	wantKeys := []string{"gitlab.url", "gitlab.client_id", "gitlab.client_secret", "gitlab.allowed_groups", "auth.local_login_enabled", "session.idle_timeout", "session.absolute_timeout"}
	if ev.actor != "local:alice" || ev.action != "settings.changed" || !reflect.DeepEqual(ev.detail, map[string]any{"keys": wantKeys}) {
		t.Errorf("event = %+v", ev)
	}
	dump := fmt.Sprintf("%+v", ev)
	for _, leak := range []string{"topsecret-xyz", "cid", "gitlab.example.com", "36h"} {
		if strings.Contains(dump, leak) {
			t.Errorf("audit event leaks %q: %s", leak, dump)
		}
	}
}

func TestSecretsEncryptedAndNeverReturned(t *testing.T) {
	e := newEnv(t, gitlabSeeds())
	check := func(stage string) {
		t.Helper()
		r := e.st.rows["gitlab.client_secret"]
		if r.Value != nil || len(r.SecretEnc) == 0 || bytes.Contains(r.SecretEnc, []byte("s3cret")) || bytes.Contains(r.SecretEnc, []byte("new-secret")) {
			t.Errorf("%s: row = %+v", stage, r)
		}
	}
	check("seed")
	if code, body := e.patch(t, `{"gitlab.client_secret":"new-secret"}`); code != 200 {
		t.Fatalf("%d %v", code, body)
	}
	check("update")
	if e.svc.Current().GitLabClientSecret != "new-secret" {
		t.Errorf("secret = %q", e.svc.Current().GitLabClientSecret)
	}
	rr := httptest.NewRecorder()
	e.svc.ListHandler().ServeHTTP(rr, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil))
	if strings.Contains(rr.Body.String(), "new-secret") || strings.Contains(rr.Body.String(), "s3cret") {
		t.Errorf("list leaks the secret: %s", rr.Body.String())
	}
	// The ciphertext is bound to its key.
	r := e.st.rows["gitlab.client_secret"]
	if _, err := testBox(t, 1).Open(r.SecretEnc, "setting:gitlab.client_secret"); err != nil {
		t.Errorf("open: %v", err)
	}
	if _, err := testBox(t, 1).Open(r.SecretEnc, "setting:gitlab.client_id"); err == nil {
		t.Error("ciphertext opens under another purpose")
	}
}

func TestWrongKeyOnLoad(t *testing.T) {
	st := newFakeStore()
	start(t, st, gitlabSeeds())
	_, err := New(context.Background(), st, st.tx, testBox(t, 2), nil, &fakeAudit{}, discard)
	if err == nil || !strings.Contains(err.Error(), "gitlab.client_secret") || !strings.Contains(err.Error(), "SCANOPSIS_SECRET_KEY changed or is wrong") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidation(t *testing.T) {
	tests := []struct {
		name   string
		seeds  map[string]any
		body   string
		fields map[string]string
	}{
		{"unknown key", nil, `{"nope":"x"}`, map[string]string{"nope": "Unknown setting."}},
		{"string type", nil, `{"gitlab.client_id":5}`, map[string]string{"gitlab.client_id": "Must be a string."}},
		{"url type", nil, `{"gitlab.url":true}`, map[string]string{"gitlab.url": "Must be a string."}},
		{"bool type", nil, `{"auth.local_login_enabled":"no"}`, map[string]string{"auth.local_login_enabled": "Must be true or false."}},
		{"duration type", nil, `{"session.idle_timeout":30}`, map[string]string{"session.idle_timeout": "Must be a string."}},
		{"list type", nil, `{"gitlab.allowed_groups":"a"}`, map[string]string{"gitlab.allowed_groups": "Must be a list of strings."}},
		{"list item type", nil, `{"gitlab.allowed_groups":["a",1]}`, map[string]string{"gitlab.allowed_groups": "Must be a list of strings."}},
		{"url not absolute", nil, `{"gitlab.url":"gitlab.example.com"}`,
			map[string]string{"gitlab.url": `Must be an absolute http or https URL, got "gitlab.example.com".`}},
		{"url with path", nil, `{"gitlab.url":"https://gitlab.example.com/x"}`,
			map[string]string{"gitlab.url": `Must not contain a path, query, fragment or credentials, got "https://gitlab.example.com/x".`}},
		{"url credentials", nil, `{"gitlab.url":"https://u:p@gitlab.example.com"}`,
			map[string]string{"gitlab.url": `Must not contain a path, query, fragment or credentials, got "https://u:p@gitlab.example.com".`}},
		{"empty secret", gitlabSeeds(), `{"gitlab.client_secret":""}`,
			map[string]string{"gitlab.client_secret": "Must not be empty; send null to reset."}},
		{"empty group", nil, `{"gitlab.allowed_groups":["a",""]}`, map[string]string{"gitlab.allowed_groups": "Group paths must not be empty."}},
		{"group slash", nil, `{"gitlab.allowed_groups":["/a"]}`, map[string]string{"gitlab.allowed_groups": `Group path "/a" must not start or end with a slash.`}},
		{"group trailing slash", nil, `{"gitlab.allowed_groups":["a/"]}`, map[string]string{"gitlab.allowed_groups": `Group path "a/" must not start or end with a slash.`}},
		{"group space", nil, `{"gitlab.allowed_groups":["a b"]}`, map[string]string{"gitlab.allowed_groups": `Group path "a b" must not contain spaces.`}},
		{"bad duration", nil, `{"session.idle_timeout":"soon"}`, map[string]string{"session.idle_timeout": "Must be a duration such as 30m or 8h."}},
		{"idle too short", nil, `{"session.idle_timeout":"4m59s"}`, map[string]string{"session.idle_timeout": "Must be at least 5m."}},
		{"absolute shorter", nil, `{"session.idle_timeout":"2h","session.absolute_timeout":"1h"}`,
			map[string]string{"session.absolute_timeout": "Must not be shorter than the idle timeout."}},
		{"absolute shorter than existing idle", nil, `{"session.absolute_timeout":"1h"}`,
			map[string]string{"session.absolute_timeout": "Must not be shorter than the idle timeout."}},
		{"gitlab url only", nil, `{"gitlab.url":"https://gitlab.example.com"}`, map[string]string{
			"gitlab.client_id": "Required when other GitLab settings are set.", "gitlab.client_secret": "Required when other GitLab settings are set."}},
		{"gitlab id only", nil, `{"gitlab.client_id":"x"}`, map[string]string{
			"gitlab.url": "Required when other GitLab settings are set.", "gitlab.client_secret": "Required when other GitLab settings are set."}},
		{"reset url leaves partial gitlab", gitlabSeeds(), `{"gitlab.url":""}`, map[string]string{
			"gitlab.url": "Required when other GitLab settings are set."}},
		{"local login off without gitlab", nil, `{"auth.local_login_enabled":false}`,
			map[string]string{"auth.local_login_enabled": "Local sign-in can be disabled only when GitLab sign-in is fully configured."}},
		{"all problems together", nil, `{"nope":1,"gitlab.client_id":3,"session.idle_timeout":"1m","auth.local_login_enabled":false}`, map[string]string{
			"nope": "Unknown setting.", "gitlab.client_id": "Must be a string.", "session.idle_timeout": "Must be at least 5m.",
			"auth.local_login_enabled": "Local sign-in can be disabled only when GitLab sign-in is fully configured."}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, tt.seeds)
			before := e.svc.Current()
			rowsBefore := len(e.st.rows)
			called := false
			e.svc.OnChange(func(Snapshot) { called = true })
			code, body := e.patch(t, tt.body)
			if code != 400 {
				t.Fatalf("status = %d, body %v", code, body)
			}
			errObj := body["error"].(map[string]any)
			if errObj["code"] != "invalid_settings" || errObj["message"] != "Some settings are not valid." {
				t.Errorf("error = %v", errObj)
			}
			got := map[string]string{}
			for k, v := range errObj["fields"].(map[string]any) {
				got[k] = v.(string)
			}
			if !reflect.DeepEqual(got, tt.fields) {
				t.Errorf("fields = %v\nwant %v", got, tt.fields)
			}
			if !reflect.DeepEqual(e.svc.Current(), before) || len(e.st.rows) != rowsBefore || called || len(e.rec.events) != 0 {
				t.Error("an invalid update must change nothing")
			}
		})
	}
}

func TestValidCombinations(t *testing.T) {
	// Local login may be disabled when GitLab is configured in the same patch.
	e := newEnv(t, nil)
	body := `{"gitlab.url":"http://gl.local","gitlab.client_id":"i","gitlab.client_secret":"s","auth.local_login_enabled":false}`
	if code, resp := e.patch(t, body); code != 200 {
		t.Fatalf("%d %v", code, resp)
	}
	// ... and re-enabled, and GitLab cleared afterwards.
	if code, resp := e.patch(t, `{"auth.local_login_enabled":true,"gitlab.url":"","gitlab.client_id":"","gitlab.client_secret":null}`); code != 200 {
		t.Fatalf("%d %v", code, resp)
	}
	if e.svc.Current().GitLabEnabled() {
		t.Error("GitLab still enabled")
	}
	// Empty patch is a no-op.
	n := len(e.rec.events)
	if code, _ := e.patch(t, `{}`); code != 200 || len(e.rec.events) != n {
		t.Errorf("empty patch: %d, events %d", code, len(e.rec.events))
	}
}

func TestMalformedRequests(t *testing.T) {
	e := newEnv(t, nil)
	for name, body := range map[string]string{
		"not json": `{`, "array": `[]`, "null": `null`, "string": `"x"`, "trailing": `{} {}`,
		"too large": `{"gitlab.client_id":"` + strings.Repeat("a", 64<<10) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			code, resp := e.patch(t, body)
			if code != 400 || resp["error"].(map[string]any)["code"] != "invalid_request" {
				t.Errorf("%d %v", code, resp)
			}
		})
	}
	if len(e.rec.events) != 0 {
		t.Error("audit written for malformed requests")
	}
}

func TestAtomicityOnStoreFailure(t *testing.T) {
	for _, failOn := range []string{"upsert", "delete"} {
		t.Run(failOn, func(t *testing.T) {
			e := newEnv(t, nil)
			e.patch(t, `{"session.idle_timeout":"2h"}`)
			before := e.svc.Current()
			called := false
			e.svc.OnChange(func(Snapshot) { called = true })
			events := len(e.rec.events)
			e.st.failOn = failOn
			code, resp := e.patch(t, `{"session.idle_timeout":null,"session.absolute_timeout":"30h"}`)
			if code != 500 || resp["error"].(map[string]any)["code"] != "internal_error" {
				t.Fatalf("%d %v", code, resp)
			}
			if !reflect.DeepEqual(e.svc.Current(), before) || called || len(e.rec.events) != events {
				t.Error("failed save must change nothing")
			}
			if _, ok := e.st.rows["session.idle_timeout"]; !ok {
				t.Error("rolled-back delete is visible")
			}
			if _, ok := e.st.rows["session.absolute_timeout"]; ok {
				t.Error("rolled-back upsert is visible")
			}
		})
	}
}

func TestUnknownActor(t *testing.T) {
	e := newEnv(t, nil)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/", strings.NewReader(`{"session.idle_timeout":"2h"}`))
	rr := httptest.NewRecorder()
	e.svc.UpdateHandler().ServeHTTP(rr, req)
	if rr.Code != 200 || e.rec.events[0].actor != "unknown" || e.st.rows["session.idle_timeout"].UpdatedBy != "unknown" {
		t.Errorf("%d %+v", rr.Code, e.rec.events)
	}
}

func TestNewErrors(t *testing.T) {
	box := testBox(t, 1)
	ctx := context.Background()
	tests := []struct {
		name  string
		setup func(*fakeStore)
		seeds map[string]any
		want  string
	}{
		{"list fails", func(f *fakeStore) { f.failOn = "list" }, nil, "load settings"},
		{"relist fails", func(f *fakeStore) { f.listErr = 2 }, map[string]any{"session.idle_timeout": time.Hour}, "load settings"},
		{"seed fails", func(f *fakeStore) { f.failOn = "seed" }, map[string]any{"session.idle_timeout": time.Hour}, "seed setting session.idle_timeout"},
		{"unknown seed", nil, map[string]any{"nope": "x"}, `unknown setting "nope"`},
		{"invalid seed", nil, map[string]any{"session.idle_timeout": 5}, "session.idle_timeout from SCANOPSIS_SESSION_IDLE_TIMEOUT: Must be a string."},
		{"unreadable row", func(f *fakeStore) {
			f.rows["gitlab.url"] = store.Setting{Key: "gitlab.url", Value: []byte("{"), Source: "ui"}
		}, nil, "setting gitlab.url has an unreadable stored value"},
		{"invalid row", func(f *fakeStore) {
			f.rows["gitlab.url"] = store.Setting{Key: "gitlab.url", Value: []byte("5"), Source: "ui"}
		}, nil, "setting gitlab.url has an invalid stored value: Must be a string."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newFakeStore()
			if tt.setup != nil {
				tt.setup(st)
			}
			_, err := New(ctx, st, st.tx, box, tt.seeds, &fakeAudit{}, discard)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestUnknownRowIgnored(t *testing.T) {
	st := newFakeStore()
	st.rows["legacy.key"] = store.Setting{Key: "legacy.key", Value: []byte(`1`), Source: "ui"}
	e := start(t, st, nil)
	if !e.svc.Current().LocalLoginEnabled {
		t.Error("defaults lost")
	}
}

func TestFormatDuration(t *testing.T) {
	for in, want := range map[time.Duration]string{
		8 * time.Hour: "8h", 30 * time.Minute: "30m", 90 * time.Minute: "1h30m", 90 * time.Second: "1m30s", time.Hour + time.Second: "1h0m1s",
	} {
		if got := formatDuration(in); got != want {
			t.Errorf("formatDuration(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestDefinitions(t *testing.T) {
	want := []string{"gitlab.url", "gitlab.client_id", "gitlab.client_secret", "gitlab.allowed_groups", "auth.local_login_enabled", "session.idle_timeout", "session.absolute_timeout"}
	var got []string
	for _, d := range Definitions {
		got = append(got, d.Key)
		if d.Description == "" || !strings.HasPrefix(d.EnvVar, "SCANOPSIS_") || !strings.HasSuffix(d.Description, ".") {
			t.Errorf("bad definition %+v", d)
		}
		if _, err := coerce(d, d.Default); err != nil && !d.Secret {
			t.Errorf("%s default: %v", d.Key, err)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("keys = %v", got)
	}
}

func TestConcurrentReadsDuringUpdates(t *testing.T) {
	e := newEnv(t, nil)
	e.svc.OnChange(func(Snapshot) {})
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					s := e.svc.Current()
					if s.SessionIdleTimeout < MinIdleTimeout || s.SessionAbsoluteTimeout < s.SessionIdleTimeout {
						t.Errorf("inconsistent snapshot %+v", s)
						return
					}
					rr := httptest.NewRecorder()
					e.svc.ListHandler().ServeHTTP(rr, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil))
				}
			}
		}()
	}
	for i := range 50 {
		d := []string{"2h", "3h"}[i%2]
		if code, body := e.patch(t, `{"session.idle_timeout":"`+d+`"}`); code != 200 {
			t.Errorf("%d %v", code, body)
		}
	}
	close(stop)
	wg.Wait()
}
