// Copyright 2026 Siyam Sarker
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
	_ "time/tzdata" // the zone names the server knows, whatever the host has

	"github.com/siyamsarker/scanopsis/internal/config"

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
	admins  int64 // administrators, as CountAdmins reports them
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

func (f *fakeStore) CountAdmins(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.admins, nil
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
		SessionIdleTimeout: 30 * time.Minute, SessionAbsoluteTimeout: 12 * time.Hour, SonarSyncInterval: 15 * time.Minute, IngestAllowedNamespaces: []string{}, RetentionDays: 90,
		BackupScheduleTimes: []string{"02:00"}, BackupTimeZone: "UTC", BackupKeepDays: 7,
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
	if idle["source"] != "default" || idle["value"] != "30m" || idle["is_set"] != true || idle["updated_by"] != nil || idle["updated_at"] != nil ||
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
		SessionIdleTimeout: 30 * time.Minute, SessionAbsoluteTimeout: 12 * time.Hour, SonarSyncInterval: 15 * time.Minute, IngestAllowedNamespaces: []string{}, RetentionDays: 90,
		BackupScheduleTimes: []string{"02:00"}, BackupTimeZone: "UTC", BackupKeepDays: 7,
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
	if got := d.svc.Current().SessionIdleTimeout; got != 30*time.Minute {
		t.Errorf("idle = %v, want default 30m", got)
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
		SessionIdleTimeout: 90 * time.Minute, SessionAbsoluteTimeout: 36 * time.Hour, SonarSyncInterval: 15 * time.Minute, IngestAllowedNamespaces: []string{}, RetentionDays: 90,
		BackupScheduleTimes: []string{"02:00"}, BackupTimeZone: "UTC", BackupKeepDays: 7,
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
	wantChanges := []map[string]any{
		{"key": "gitlab.url", "from": "", "to": "https://gitlab.example.com"},
		{"key": "gitlab.client_id", "from": "", "to": "cid"},
		{"key": "gitlab.client_secret", "changed": true},
		{"key": "gitlab.allowed_groups", "from": []string{}, "to": []string{"a", "b/c"}},
		{"key": "auth.local_login_enabled", "from": true, "to": false},
		{"key": "session.idle_timeout", "from": "30m", "to": "1h30m"},
		{"key": "session.absolute_timeout", "from": "12h", "to": "36h"},
	}
	if ev.actor != "local:alice" || ev.action != "settings.changed" ||
		!reflect.DeepEqual(ev.detail, map[string]any{"keys": wantKeys, "changes": wantChanges}) {
		t.Errorf("event = %+v", ev)
	}
	if dump := fmt.Sprintf("%+v", ev); strings.Contains(dump, "topsecret-xyz") {
		t.Errorf("audit event leaks the secret: %s", dump)
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
		{"absolute shorter than existing idle", nil, `{"session.absolute_timeout":"20m"}`,
			map[string]string{"session.absolute_timeout": "Must not be shorter than the idle timeout."}},
		{"gitlab id only", nil, `{"gitlab.client_id":"x"}`, map[string]string{
			"gitlab.url": "Required when other GitLab sign-in settings are set.", "gitlab.client_secret": "Required when other GitLab sign-in settings are set."}},
		{"reset url leaves partial gitlab", gitlabSeeds(), `{"gitlab.url":""}`, map[string]string{
			"gitlab.url": "Required when other GitLab sign-in settings are set."}},
		{"sonar url without token", nil, `{"sonar.url":"https://sonarqube.example.com"}`, map[string]string{
			"sonar.token": "Required when other SonarQube settings are set."}},
		{"sonar webhook secret alone", nil, `{"sonar.webhook_secret":"x"}`, map[string]string{
			"sonar.url": "Required when other SonarQube settings are set.", "sonar.token": "Required when other SonarQube settings are set."}},
		{"sonar sync too short", nil, `{"sonar.sync_interval":"30s"}`, map[string]string{
			"sonar.sync_interval": "Must be at least 1m."}},
		{"ingest namespace with slash", nil, `{"ingest.allowed_namespaces":["/platform"]}`, map[string]string{
			"ingest.allowed_namespaces": `Group path "/platform" must not start or end with a slash.`}},
		{"new gitlab url keeps the stored client secret", gitlabSeeds(), `{"gitlab.url":"https://other.example.com"}`, map[string]string{
			"gitlab.client_secret": "Enter this value again when you change the URL, so the stored one is never sent to a new address."}},
		{"new sonar url keeps the stored token", sonarSeeds(), `{"sonar.url":"https://other.example.com"}`, map[string]string{
			"sonar.token": "Enter this value again when you change the URL, so the stored one is never sent to a new address."}},
		{"new sonar url with a reset token falls back to the env token", sonarSeeds(), `{"sonar.url":"https://other.example.com","sonar.token":null}`, map[string]string{
			"sonar.token": "Enter this value again when you change the URL, so the stored one is never sent to a new address."}},
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
	want := []string{"gitlab.url", "gitlab.client_id", "gitlab.client_secret", "gitlab.allowed_groups", "auth.local_login_enabled", "session.idle_timeout", "session.absolute_timeout",
		"sonar.url", "sonar.token", "sonar.webhook_secret", "sonar.sync_interval", "ingest.allowed_namespaces", "ingest.protected_refs_only", "retention.days",
		"backup.schedule_enabled", "backup.schedule_times", "backup.time_zone", "backup.keep_days"}
	var got []string
	for _, d := range Definitions {
		got = append(got, d.Key)
		// The backup settings are edited on the Backups page only and have
		// no environment variable.
		envOK := strings.HasPrefix(d.EnvVar, "SCANOPSIS_")
		if strings.HasPrefix(d.Key, "backup.") {
			envOK = d.EnvVar == ""
		}
		if d.Description == "" || !envOK || !strings.HasSuffix(d.Description, ".") {
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

func TestStartRefusesSettingsWithoutSignIn(t *testing.T) {
	// An environment seed (not validated by the Settings page) disables local
	// sign-in while GitLab is not configured: nobody could sign in.
	fresh := newFakeStore()
	_, err := New(context.Background(), fresh, fresh.tx, testBox(t, 1),
		map[string]any{KeyLocalLoginEnabled: false}, &fakeAudit{}, discard)
	if err == nil || !strings.Contains(err.Error(), "remove SCANOPSIS_LOCAL_LOGIN_ENABLED=false") {
		t.Fatalf("env seed: err = %v", err)
	}
	if len(fresh.rows) != 0 {
		t.Fatalf("the refused environment value was saved: %v", fresh.rows)
	}

	// The same combination saved in the database names the database fix.
	st := newFakeStore()
	st.rows[KeyLocalLoginEnabled] = store.Setting{Key: KeyLocalLoginEnabled, Value: []byte("false"), Source: SourceUI, UpdatedBy: "local:root"}
	_, err = New(context.Background(), st, st.tx, testBox(t, 1), nil, &fakeAudit{}, discard)
	if err == nil || !strings.Contains(err.Error(), "DELETE FROM settings WHERE key = 'auth.local_login_enabled'") {
		t.Fatalf("saved value: err = %v", err)
	}

	// With GitLab fully configured the seed takes effect, and is reported as is.
	seeds := gitlabSeeds()
	seeds[KeyLocalLoginEnabled] = false
	if newEnv(t, seeds).svc.Current().LocalLoginEnabled {
		t.Fatal("local sign-in enabled although it is off and GitLab sign-in is configured")
	}
}

// SCANOPSIS_SONAR_SYNC_INTERVAL below the minimum the Settings page enforces
// refuses the start when it would be saved, on a fresh database. Once the
// interval is saved, the variable is ignored as any other seed is, with a
// warning, so an installation that once started with a shorter value still
// starts.
func TestSyncIntervalSeedBelowMinimum(t *testing.T) {
	short := map[string]any{KeySonarSyncInterval: 30 * time.Second}

	fresh := newFakeStore()
	_, err := New(context.Background(), fresh, fresh.tx, testBox(t, 1), short, &fakeAudit{}, discard)
	if err == nil || err.Error() != "SCANOPSIS_SONAR_SYNC_INTERVAL must be at least 1m" {
		t.Fatalf("fresh database: err = %v", err)
	}
	if len(fresh.rows) != 0 {
		t.Fatalf("the refused value was saved: %v", fresh.rows)
	}

	st := newFakeStore()
	st.admins = 1 // an installation that exists
	st.rows[KeySonarSyncInterval] = store.Setting{Key: KeySonarSyncInterval, Value: []byte(`"5m"`), Source: SourceUI, UpdatedBy: "local:root"}
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	svc, err := New(context.Background(), st, st.tx, testBox(t, 1), short, &fakeAudit{}, log)
	if err != nil {
		t.Fatalf("saved 5m: err = %v", err)
	}
	if got := svc.Current().SonarSyncInterval; got != 5*time.Minute {
		t.Errorf("interval = %v, want the saved 5m", got)
	}
	if got := string(st.rows[KeySonarSyncInterval].Value); got != `"5m"` {
		t.Errorf("row = %s, want it unchanged", got)
	}
	if !strings.Contains(logs.String(), "level=WARN") ||
		!strings.Contains(logs.String(), "SCANOPSIS_SONAR_SYNC_INTERVAL is ignored") ||
		!strings.Contains(logs.String(), "value=30s") {
		t.Errorf("no warning logged: %s", logs.String())
	}
	// A reload, as after a restore without the row, does not take it up.
	delete(st.rows, KeySonarSyncInterval)
	if err := svc.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := svc.Current().SonarSyncInterval; got != 15*time.Minute {
		t.Errorf("after reload: interval = %v, want the default 15m", got)
	}

	// At the minimum the seed is saved as usual.
	if got := newEnv(t, map[string]any{KeySonarSyncInterval: time.Minute}).svc.Current().SonarSyncInterval; got != time.Minute {
		t.Errorf("1m seed: interval = %v", got)
	}
}

// An installation upgraded from a version that saved a short sync interval
// keeps starting after an administrator resets the interval, which deletes
// the saved row: with an administrator, the installation exists, so the
// short variable is only warned about and ignored. Only a new installation,
// one without an administrator, refuses to start with it.
func TestSyncIntervalSeedAfterReset(t *testing.T) {
	short := map[string]any{KeySonarSyncInterval: 30 * time.Second}
	st := newFakeStore()
	st.admins = 1
	st.rows[KeySonarSyncInterval] = store.Setting{Key: KeySonarSyncInterval, Value: []byte(`"30s"`), Source: SourceEnv, UpdatedBy: "system"}
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	start := func() *Service {
		t.Helper()
		svc, err := New(context.Background(), st, st.tx, testBox(t, 1), short, &fakeAudit{}, log)
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		return svc
	}
	svc := start()
	if got := svc.Current().SonarSyncInterval; got != 30*time.Second {
		t.Errorf("interval = %v, want the saved 30s", got)
	}
	e := &env{st, &fakeAudit{}, svc}
	if code, body := e.patch(t, `{"sonar.sync_interval":null}`); code != 200 {
		t.Fatalf("reset: %d %v", code, body)
	}
	if _, ok := st.rows[KeySonarSyncInterval]; ok {
		t.Fatal("row not deleted by the reset")
	}
	if got := svc.Current().SonarSyncInterval; got != 15*time.Minute {
		t.Errorf("after reset: interval = %v, want the default 15m", got)
	}

	logs.Reset()
	svc = start() // the next start, with the same environment
	if got := svc.Current().SonarSyncInterval; got != 15*time.Minute {
		t.Errorf("after restart: interval = %v, want the default 15m", got)
	}
	if _, ok := st.rows[KeySonarSyncInterval]; ok {
		t.Error("the short value was saved")
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "SCANOPSIS_SONAR_SYNC_INTERVAL is ignored") {
		t.Errorf("no warning logged: %s", logs.String())
	}

	// A new installation, without an administrator, refuses it.
	fresh := newFakeStore()
	if _, err := New(context.Background(), fresh, fresh.tx, testBox(t, 1), short, &fakeAudit{}, discard); err == nil ||
		err.Error() != "SCANOPSIS_SONAR_SYNC_INTERVAL must be at least 1m" {
		t.Fatalf("new installation: err = %v", err)
	}
}

func TestGitLabURLAloneIsValid(t *testing.T) {
	// Verifying CI job tokens needs only the GitLab URL; sign-in stays off.
	e := newEnv(t, nil)
	if code, body := e.patch(t, `{"gitlab.url":"https://gitlab.example.com"}`); code != 200 {
		t.Fatalf("status = %d, body %v", code, body)
	}
	snap := e.svc.Current()
	if snap.GitLabURL != "https://gitlab.example.com" || snap.GitLabEnabled() {
		t.Fatalf("snapshot = %+v, GitLabEnabled = %v", snap, snap.GitLabEnabled())
	}
}

func TestSonarSettings(t *testing.T) {
	e := newEnv(t, nil)
	code, body := e.patch(t, `{"sonar.url":"https://sonarqube.example.com/","sonar.token":"squ_abc","sonar.webhook_secret":"whsec","sonar.sync_interval":"5m"}`)
	if code != 200 {
		t.Fatalf("status = %d, body %v", code, body)
	}
	snap := e.svc.Current()
	if !snap.SonarEnabled() || snap.SonarURL != "https://sonarqube.example.com" || snap.SonarToken != "squ_abc" ||
		snap.SonarWebhookSecret != "whsec" || snap.SonarSyncInterval != 5*time.Minute {
		t.Fatalf("snapshot = %+v", snap)
	}
	for _, row := range e.st.rows {
		if strings.Contains(string(row.SecretEnc), "squ_abc") || strings.Contains(string(row.Value), "squ_abc") {
			t.Fatal("SonarQube token stored in plain text")
		}
	}
}

func TestRetentionDaysSetting(t *testing.T) {
	e := newEnv(t, map[string]any{"retention.days": 30})
	if got := e.svc.Current().RetentionDays; got != 30 {
		t.Fatalf("seeded RetentionDays = %d, want 30", got)
	}
	item := e.items(t)["retention.days"]
	if item["type"] != "integer" || item["value"] != float64(30) || item["source"] != "env" || item["is_set"] != true {
		t.Fatalf("item = %v", item)
	}
	if code, body := e.patch(t, `{"retention.days":365}`); code != 200 {
		t.Fatalf("status = %d, body %v", code, body)
	}
	if got := e.svc.Current().RetentionDays; got != 365 {
		t.Fatalf("RetentionDays = %d, want 365", got)
	}
	if r := e.st.rows["retention.days"]; string(r.Value) != "365" || r.Source != "ui" {
		t.Fatalf("stored row = %+v, want the JSON number 365", r)
	}
	if code, body := e.patch(t, `{"retention.days":null}`); code != 200 {
		t.Fatalf("reset: status = %d, body %v", code, body)
	}
	// Reset falls back to the environment seed.
	if got := e.svc.Current().RetentionDays; got != 30 {
		t.Fatalf("after reset RetentionDays = %d, want 30", got)
	}
}

func TestRetentionDaysDefault(t *testing.T) {
	e := newEnv(t, nil)
	item := e.items(t)["retention.days"]
	if item["value"] != float64(90) || item["source"] != "default" {
		t.Fatalf("item = %v", item)
	}
}

func TestRetentionDaysValidation(t *testing.T) {
	const msg = "Must be a whole number from 7 to 3650."
	for name, body := range map[string]string{
		"below minimum": `{"retention.days":6}`,
		"above maximum": `{"retention.days":3651}`,
		"zero":          `{"retention.days":0}`,
		"negative":      `{"retention.days":-90}`,
		"fraction":      `{"retention.days":90.5}`,
		"string":        `{"retention.days":"90"}`,
		"boolean":       `{"retention.days":true}`,
		"list":          `{"retention.days":[90]}`,
		"huge":          `{"retention.days":1e300}`,
		"tiny fraction": `{"retention.days":90.0000001}`,
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, nil)
			code, resp := e.patch(t, body)
			if code != 400 {
				t.Fatalf("status = %d, body %v", code, resp)
			}
			fields := resp["error"].(map[string]any)["fields"].(map[string]any)
			if fields["retention.days"] != msg {
				t.Errorf("fields = %v", fields)
			}
			if got := e.svc.Current().RetentionDays; got != 90 {
				t.Errorf("RetentionDays changed to %d", got)
			}
		})
	}
	for _, body := range []string{`{"retention.days":7}`, `{"retention.days":3650}`, `{"retention.days":90.0}`} {
		e := newEnv(t, nil)
		if code, resp := e.patch(t, body); code != 200 {
			t.Errorf("%s: status = %d, body %v", body, code, resp)
		}
	}
}

func TestCoerceInteger(t *testing.T) {
	d := definitionByKey[KeyRetentionDays]
	for _, tc := range []struct {
		in   any
		want int
		ok   bool
	}{
		{90, 90, true}, {int64(30), 30, true}, {float64(7), 7, true}, {float64(3650), 3650, true},
		{6, 0, false}, {3651, 0, false}, {int64(1) << 40, 0, false}, {-int64(1) << 40, 0, false},
		{7.5, 0, false}, {"90", 0, false}, {nil, 0, false}, {true, 0, false},
	} {
		got, err := coerce(d, tc.in)
		if (err == nil) != tc.ok || (tc.ok && got != tc.want) {
			t.Errorf("coerce(%#v) = %v, %v; want %d, ok=%v", tc.in, got, err, tc.want, tc.ok)
		}
	}
}

func TestNewRejectsInvalidIntegerSeed(t *testing.T) {
	st := newFakeStore()
	_, err := New(context.Background(), st, st.tx, testBox(t, 1), map[string]any{"retention.days": 3}, &fakeAudit{}, discard)
	if err == nil || !strings.Contains(err.Error(), "SCANOPSIS_RETENTION_DAYS") {
		t.Fatalf("err = %v", err)
	}
}

func sonarSeeds() map[string]any {
	return map[string]any{KeySonarURL: "https://sonarqube.example.com", KeySonarToken: "squ_seed"}
}

func TestURLChangeWithItsSecret(t *testing.T) {
	for _, tc := range []struct {
		name  string
		seeds map[string]any
		body  string
		check func(Snapshot) bool
	}{
		{"sonar url and token together", sonarSeeds(), `{"sonar.url":"https://other.example.com","sonar.token":"squ_new"}`,
			func(s Snapshot) bool { return s.SonarURL == "https://other.example.com" && s.SonarToken == "squ_new" }},
		{"gitlab url and client secret together", gitlabSeeds(), `{"gitlab.url":"https://other.example.com","gitlab.client_secret":"new"}`,
			func(s Snapshot) bool {
				return s.GitLabURL == "https://other.example.com" && s.GitLabClientSecret == "new"
			}},
		{"same url again", sonarSeeds(), `{"sonar.url":"https://sonarqube.example.com/"}`,
			func(s Snapshot) bool { return s.SonarToken == "squ_seed" }},
		{"gitlab url without a client secret", nil, `{"gitlab.url":"https://other.example.com"}`,
			func(s Snapshot) bool { return s.GitLabURL == "https://other.example.com" }},
		{"sonar url when no token is stored", nil, `{"sonar.url":"https://other.example.com","sonar.token":"squ_new"}`,
			func(s Snapshot) bool { return s.SonarToken == "squ_new" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, tc.seeds)
			code, body := e.patch(t, tc.body)
			if code != 200 {
				t.Fatalf("status = %d, body %v", code, body)
			}
			if snap := e.svc.Current(); !tc.check(snap) {
				t.Errorf("snapshot = %+v", snap)
			}
		})
	}
}

func TestSettingsAuditRecordsResetsAndSecretChanges(t *testing.T) {
	e := newEnv(t, gitlabSeeds())
	e.patch(t, `{"session.idle_timeout":"2h","gitlab.client_secret":"another-secret"}`)
	e.patch(t, `{"session.idle_timeout":null,"gitlab.client_secret":null}`)
	if len(e.rec.events) != 2 {
		t.Fatalf("events = %v", e.rec.events)
	}
	for i, ev := range e.rec.events {
		dump := fmt.Sprintf("%+v", ev.detail)
		for _, leak := range []string{"another-secret", "s3cret"} {
			if strings.Contains(dump, leak) {
				t.Errorf("event %d leaks a secret: %s", i, dump)
			}
		}
	}
	reset := e.rec.events[1].detail["changes"].([]map[string]any)
	want := []map[string]any{
		{"key": "gitlab.client_secret", "reset": true, "changed": true},
		{"key": "session.idle_timeout", "reset": true, "from": "2h", "to": "30m"},
	}
	if !reflect.DeepEqual(reset, want) {
		t.Errorf("reset changes = %v; want %v", reset, want)
	}
}

// The start-up check in internal/config assumes the same session defaults as
// the settings definitions.
func TestSessionDefaultsMatchConfig(t *testing.T) {
	for key, want := range map[string]time.Duration{
		KeySessionIdleTimeout: config.DefaultSessionIdleTimeout,
		KeySessionAbsTimeout:  config.DefaultSessionAbsoluteTimeout,
	} {
		d := definitionByKey[key]
		got, err := time.ParseDuration(d.Default.(string))
		if err != nil || got != want {
			t.Errorf("%s default %v, config %v", key, d.Default, want)
		}
	}
}

func TestBackupSettingsDefaults(t *testing.T) {
	e := newEnv(t, nil)
	snap := e.svc.Current()
	if snap.BackupScheduleEnabled || !reflect.DeepEqual(snap.BackupScheduleTimes, []string{"02:00"}) || snap.BackupTimeZone != "UTC" || snap.BackupKeepDays != 7 {
		t.Fatalf("snapshot = %+v", snap)
	}
	items := e.items(t)
	for key, typ := range map[string]string{
		"backup.schedule_enabled": "boolean", "backup.schedule_times": "string_list", "backup.time_zone": "string", "backup.keep_days": "integer",
	} {
		if it := items[key]; it["type"] != typ || it["source"] != "default" || it["env_var"] != "" {
			t.Errorf("%s = %v", key, it)
		}
	}
}

func TestBackupSettingsValid(t *testing.T) {
	e := newEnv(t, nil)
	body := `{"backup.schedule_enabled":true,"backup.schedule_times":["23:30"," 00:00","12:05"],"backup.time_zone":"Europe/Berlin","backup.keep_days":365}`
	if code, resp := e.patch(t, body); code != 200 {
		t.Fatalf("status = %d, body %v", code, resp)
	}
	snap := e.svc.Current()
	if !snap.BackupScheduleEnabled || !reflect.DeepEqual(snap.BackupScheduleTimes, []string{"00:00", "12:05", "23:30"}) ||
		snap.BackupTimeZone != "Europe/Berlin" || snap.BackupKeepDays != 365 {
		t.Fatalf("snapshot = %+v", snap)
	}
	all := make([]string, 0, 24)
	for h := range 24 {
		all = append(all, fmt.Sprintf("%q", fmt.Sprintf("%02d:00", h)))
	}
	if code, resp := e.patch(t, `{"backup.schedule_times":[`+strings.Join(all, ",")+`],"backup.time_zone":"Asia/Dhaka","backup.keep_days":1}`); code != 200 {
		t.Fatalf("24 times: status = %d, body %v", code, resp)
	}
}

// TestBackupTimeZoneNames checks that a zone is accepted by its current
// name and by the former one browsers still give, as the time zone database
// knows both.
func TestBackupTimeZoneNames(t *testing.T) {
	e := newEnv(t, nil)
	for _, zone := range []string{
		"Asia/Kolkata", "Asia/Calcutta", "Europe/Kyiv", "Europe/Kiev",
		"Asia/Ho_Chi_Minh", "Asia/Saigon", "Asia/Kathmandu", "Asia/Katmandu",
		"America/Nuuk", "America/Godthab", "Pacific/Kanton", "Pacific/Enderbury",
	} {
		if code, resp := e.patch(t, `{"backup.time_zone":"`+zone+`"}`); code != 200 {
			t.Errorf("%s: status = %d, body %v", zone, code, resp)
			continue
		}
		if got := e.svc.Current().BackupTimeZone; got != zone {
			t.Errorf("%s: saved as %q", zone, got)
		}
	}
}

func TestBackupSettingsValidation(t *testing.T) {
	const (
		timesMsg = "Each time must be a 24-hour time such as 02:00."
		zoneMsg  = "Must be a time zone name such as UTC or Europe/Berlin."
		keepMsg  = "Must be a whole number from 1 to 365."
	)
	var times25 []string
	for i := range 25 {
		times25 = append(times25, fmt.Sprintf("%q", fmt.Sprintf("%02d:%02d", i%24, i/24)))
	}
	for name, tc := range map[string]struct{ body, key, msg string }{
		"hour 25":        {`{"backup.schedule_times":["25:00"]}`, "backup.schedule_times", timesMsg},
		"minute 60":      {`{"backup.schedule_times":["02:60"]}`, "backup.schedule_times", timesMsg},
		"no leading 0":   {`{"backup.schedule_times":["2:00"]}`, "backup.schedule_times", timesMsg},
		"seconds":        {`{"backup.schedule_times":["02:00:00"]}`, "backup.schedule_times", timesMsg},
		"12-hour":        {`{"backup.schedule_times":["02:00 PM"]}`, "backup.schedule_times", timesMsg},
		"empty item":     {`{"backup.schedule_times":[""]}`, "backup.schedule_times", timesMsg},
		"duplicates":     {`{"backup.schedule_times":["02:00","03:00","02:00"]}`, "backup.schedule_times", `The time 02:00 is listed twice.`},
		"none":           {`{"backup.schedule_times":[]}`, "backup.schedule_times", "Must list from 1 to 24 times."},
		"25 entries":     {`{"backup.schedule_times":[` + strings.Join(times25, ",") + `]}`, "backup.schedule_times", "Must list from 1 to 24 times."},
		"not a list":     {`{"backup.schedule_times":"02:00"}`, "backup.schedule_times", "Must be a list of strings."},
		"unknown zone":   {`{"backup.time_zone":"Mars/Phobos"}`, "backup.time_zone", zoneMsg},
		"empty zone":     {`{"backup.time_zone":""}`, "backup.time_zone", zoneMsg},
		"local zone":     {`{"backup.time_zone":"Local"}`, "backup.time_zone", zoneMsg},
		"zone path":      {`{"backup.time_zone":"../etc/passwd"}`, "backup.time_zone", zoneMsg},
		"keep 0":         {`{"backup.keep_days":0}`, "backup.keep_days", keepMsg},
		"keep 366":       {`{"backup.keep_days":366}`, "backup.keep_days", keepMsg},
		"enabled string": {`{"backup.schedule_enabled":"yes"}`, "backup.schedule_enabled", "Must be true or false."},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, nil)
			before := e.svc.Current()
			code, resp := e.patch(t, tc.body)
			if code != 400 {
				t.Fatalf("status = %d, body %v", code, resp)
			}
			fields := resp["error"].(map[string]any)["fields"].(map[string]any)
			if fields[tc.key] != tc.msg || len(fields) != 1 {
				t.Errorf("fields = %v, want %s: %s", fields, tc.key, tc.msg)
			}
			if !reflect.DeepEqual(e.svc.Current(), before) {
				t.Error("an invalid update changed the settings")
			}
		})
	}
}

func TestReload(t *testing.T) {
	e := newEnv(t, map[string]any{"retention.days": 30})
	var got []Snapshot
	e.svc.OnChange(func(s Snapshot) { got = append(got, s) })

	// The rows change behind the service's back, as a restore does.
	e.st.rows["backup.keep_days"] = store.Setting{Key: "backup.keep_days", Value: []byte(`14`), Source: "ui", UpdatedBy: "local:bob"}
	e.st.rows["retention.days"] = store.Setting{Key: "retention.days", Value: []byte(`45`), Source: "ui", UpdatedBy: "local:bob"}
	if err := e.svc.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if snap := e.svc.Current(); snap.BackupKeepDays != 14 || snap.RetentionDays != 45 {
		t.Fatalf("after reload %+v", snap)
	}
	if len(got) != 1 || got[0].BackupKeepDays != 14 {
		t.Fatalf("OnChange calls = %+v", got)
	}
	if it := e.items(t)["backup.keep_days"]; it["source"] != "ui" || it["updated_by"] != "local:bob" {
		t.Errorf("item = %v", it)
	}

	// A row deleted falls back to the environment seed, which is not
	// written again.
	delete(e.st.rows, "retention.days")
	if err := e.svc.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if snap := e.svc.Current(); snap.RetentionDays != 30 {
		t.Fatalf("after second reload RetentionDays = %d, want the seed 30", snap.RetentionDays)
	}
	if _, ok := e.st.rows["retention.days"]; ok {
		t.Error("Reload wrote a seed row")
	}

	// A failure keeps the current settings and calls nobody.
	e.st.failOn = "list"
	if err := e.svc.Reload(context.Background()); err == nil {
		t.Fatal("Reload succeeded with a failing store")
	}
	if snap := e.svc.Current(); snap.BackupKeepDays != 14 || len(got) != 2 {
		t.Errorf("after failed reload %+v, calls %d", snap, len(got))
	}
}

func TestReloadRefusesInvalidRow(t *testing.T) {
	e := newEnv(t, nil)
	e.st.rows["backup.time_zone"] = store.Setting{Key: "backup.time_zone", Value: []byte(`"Mars/Phobos"`), Source: "ui", UpdatedBy: "local:bob"}
	if err := e.svc.Reload(context.Background()); err == nil || !strings.Contains(err.Error(), "backup.time_zone") {
		t.Fatalf("err = %v", err)
	}
	if e.svc.Current().BackupTimeZone != "UTC" {
		t.Error("an invalid row was applied")
	}
}
