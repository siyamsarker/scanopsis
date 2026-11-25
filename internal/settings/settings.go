// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/siyamsarker/scanopsis/internal/audit"
	"github.com/siyamsarker/scanopsis/internal/auth"
	"github.com/siyamsarker/scanopsis/internal/config"
	"github.com/siyamsarker/scanopsis/internal/secretbox"
	"github.com/siyamsarker/scanopsis/internal/store"
)

// Setting sources.
const (
	SourceDefault = "default"
	SourceEnv     = "env"
	SourceUI      = "ui"
)

// Snapshot is an immutable view of the settings. Slices must not be
// modified by callers.
type Snapshot struct {
	GitLabURL           string
	GitLabClientID      string
	GitLabClientSecret  string
	GitLabAllowedGroups []string
	LocalLoginEnabled   bool

	SessionIdleTimeout     time.Duration
	SessionAbsoluteTimeout time.Duration

	SonarURL           string
	SonarToken         string
	SonarWebhookSecret string
	SonarSyncInterval  time.Duration

	IngestAllowedNamespaces []string
	IngestProtectedRefsOnly bool

	// RetentionDays is how many days scan history is kept.
	RetentionDays int

	// BackupScheduleEnabled turns scheduled backups on.
	BackupScheduleEnabled bool
	// BackupScheduleTimes are the times of day of scheduled backups, sorted
	// 24-hour "HH:MM" values in BackupTimeZone.
	BackupScheduleTimes []string
	// BackupTimeZone is the IANA name of the zone of BackupScheduleTimes.
	BackupTimeZone string
	// BackupKeepDays is how many days scheduled backups are kept.
	BackupKeepDays int
}

// SonarEnabled reports whether SonarQube results can be read (URL and token set).
func (s Snapshot) SonarEnabled() bool { return s.SonarURL != "" && s.SonarToken != "" }

// GitLabEnabled reports whether GitLab sign-in is fully configured.
func (s Snapshot) GitLabEnabled() bool {
	return s.GitLabURL != "" && s.GitLabClientID != "" && s.GitLabClientSecret != ""
}

// Store is the persistence the service needs; *store.Queries satisfies it.
type Store interface {
	ListSettings(ctx context.Context) ([]store.Setting, error)
	// CountAdmins counts the administrators: none means a new installation,
	// as for the setup page.
	CountAdmins(ctx context.Context) (int64, error)
	SeedSetting(ctx context.Context, arg store.SeedSettingParams) error
	UpsertSetting(ctx context.Context, arg store.UpsertSettingParams) error
	DeleteSetting(ctx context.Context, key string) error
}

// entry is the effective state of one setting.
type entry struct {
	value     any // canonical value; the plaintext for secrets
	source    string
	updatedBy string
	updatedAt time.Time
	stored    bool // backed by a database row
}

// state is immutable once published.
type state struct {
	entries map[string]entry
	snap    Snapshot
}

// Service serves the settings. It is safe for concurrent use.
type Service struct {
	st    Store
	tx    func(ctx context.Context, fn func(Store) error) error
	box   *secretbox.Box
	audit auth.AuditRecorder
	log   *slog.Logger
	seeds map[string]any // canonical env values

	cur atomic.Pointer[state]

	mu        sync.Mutex // serialises updates and guards callbacks
	callbacks []func(Snapshot)
}

func purpose(key string) string { return "setting:" + key }

// New seeds environment values for keys without a row, loads all rows and
// builds the first snapshot. tx runs fn with a Store bound to a transaction.
func New(ctx context.Context, st Store, tx func(ctx context.Context, fn func(Store) error) error, box *secretbox.Box, seeds map[string]any, rec auth.AuditRecorder, log *slog.Logger) (*Service, error) {
	s := &Service{st: st, tx: tx, box: box, audit: rec, log: log, seeds: map[string]any{}}
	for key, raw := range seeds {
		d, ok := definitionByKey[key]
		if !ok {
			return nil, fmt.Errorf("unknown setting %q in the environment seeds", key)
		}
		v, err := coerce(d, raw)
		if err != nil {
			return nil, fmt.Errorf("%s from %s: %w", key, d.EnvVar, err)
		}
		s.seeds[key] = v
	}

	rows, err := st.ListSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}
	// Check what the settings would be (saved rows, then environment values
	// for keys without a row) before saving anything, so a value that would
	// lock everyone out is never written.
	stored, err := s.decodeAll(rows)
	if err != nil {
		return nil, err
	}
	if err := s.checkSyncSeed(ctx, stored); err != nil {
		return nil, err
	}
	stt := s.build(stored)
	if err := signInPossible(stt); err != nil {
		return nil, err
	}

	seeded := false
	for _, d := range Definitions { // deterministic order
		v, ok := s.seeds[d.Key]
		if _, have := stored[d.Key]; !ok || have {
			continue
		}
		p, err := s.encode(d, v)
		if err != nil {
			return nil, err
		}
		if err := st.SeedSetting(ctx, store.SeedSettingParams{Key: d.Key, Value: p.value, SecretEnc: p.secret}); err != nil {
			return nil, fmt.Errorf("seed setting %s: %w", d.Key, err)
		}
		seeded = true
	}
	if seeded {
		if rows, err = st.ListSettings(ctx); err != nil {
			return nil, fmt.Errorf("load settings: %w", err)
		}
		if stored, err = s.decodeAll(rows); err != nil {
			return nil, err
		}
		stt = s.build(stored)
	}
	s.cur.Store(stt)
	return s, nil
}

// checkSyncSeed applies the minimum sync interval of the Settings page to
// SCANOPSIS_SONAR_SYNC_INTERVAL. A new installation, one without an
// administrator yet as for the setup page, refuses to start with a shorter
// value: saved, it would make every later save on the Settings page fail.
// An installation that exists starts whatever the variable says, so one
// that ran with a shorter value before keeps starting, even after the saved
// interval was reset or a backup without it was restored: the variable is
// ignored and dropped, so neither a reset nor a reload takes it up, the
// saved interval or the default applies, and a warning says so.
func (s *Service) checkSyncSeed(ctx context.Context, stored map[string]entry) error {
	v, ok := s.seeds[KeySonarSyncInterval]
	if !ok {
		return nil
	}
	if d, _ := time.ParseDuration(v.(string)); d >= MinSonarSyncInterval { // canonical
		return nil
	}
	admins, err := s.st.CountAdmins(ctx)
	if err != nil {
		return fmt.Errorf("count administrators: %w", err)
	}
	minimum := formatDuration(MinSonarSyncInterval)
	if admins == 0 {
		return fmt.Errorf("%sSONAR_SYNC_INTERVAL must be at least %s", config.Prefix, minimum)
	}
	delete(s.seeds, KeySonarSyncInterval)
	applies := fmt.Sprintf("the default of %v", definitionByKey[KeySonarSyncInterval].Default)
	if saved, have := stored[KeySonarSyncInterval]; have {
		applies = "the saved interval of " + saved.value.(string)
	}
	s.log.WarnContext(ctx, config.Prefix+"SONAR_SYNC_INTERVAL is ignored: it is below the minimum of "+minimum+
		", and "+applies+" applies; remove the variable or set it to "+minimum+" or more", "value", v)
	return nil
}

// decodeAll decodes the stored rows of known settings.
func (s *Service) decodeAll(rows []store.Setting) (map[string]entry, error) {
	stored := make(map[string]entry, len(rows))
	for _, r := range rows {
		d, ok := definitionByKey[r.Key]
		if !ok {
			s.log.Warn("ignoring unknown setting in the database", "key", r.Key)
			continue
		}
		e, err := s.decode(d, r)
		if err != nil {
			return nil, err
		}
		stored[r.Key] = e
	}
	return stored, nil
}

type payload struct{ value, secret []byte }

func (s *Service) encode(d Definition, v any) (payload, error) {
	if d.Secret {
		sealed, err := s.box.Seal([]byte(v.(string)), purpose(d.Key))
		if err != nil {
			return payload{}, fmt.Errorf("encrypt setting %s: %w", d.Key, err)
		}
		return payload{secret: sealed}, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return payload{}, fmt.Errorf("encode setting %s: %w", d.Key, err)
	}
	return payload{value: b}, nil
}

func (s *Service) decode(d Definition, r store.Setting) (entry, error) {
	e := entry{source: r.Source, updatedBy: r.UpdatedBy, stored: true}
	if r.UpdatedAt.Valid {
		e.updatedAt = r.UpdatedAt.Time
	}
	if d.Secret {
		b, err := s.box.Open(r.SecretEnc, purpose(d.Key))
		if err != nil {
			return entry{}, fmt.Errorf("setting %s cannot be decrypted: SCANOPSIS_SECRET_KEY changed or is wrong", d.Key)
		}
		e.value = string(b)
		return e, nil
	}
	var raw any
	if err := json.Unmarshal(r.Value, &raw); err != nil {
		return entry{}, fmt.Errorf("setting %s has an unreadable stored value: %w", d.Key, err)
	}
	v, err := coerce(d, raw)
	if err != nil {
		return entry{}, fmt.Errorf("setting %s has an invalid stored value: %w", d.Key, err)
	}
	e.value = v
	return e, nil
}

// build merges stored rows over the env seeds and defaults.
func (s *Service) build(stored map[string]entry) *state {
	entries := make(map[string]entry, len(Definitions))
	for _, d := range Definitions {
		entries[d.Key] = s.effective(d, stored)
	}
	return &state{entries: entries, snap: snapshotOf(entries)}
}

func (s *Service) effective(d Definition, stored map[string]entry) entry {
	if e, ok := stored[d.Key]; ok {
		return e
	}
	if v, ok := s.seeds[d.Key]; ok {
		return entry{value: v, source: SourceEnv}
	}
	v := d.Default
	if !d.Secret {
		v, _ = coerce(d, d.Default) // canonical form; definitions are valid
	}
	return entry{value: v, source: SourceDefault}
}

func snapshotOf(e map[string]entry) Snapshot {
	dur := func(key string) time.Duration {
		d, _ := time.ParseDuration(e[key].value.(string)) // canonical
		return d
	}
	return Snapshot{
		GitLabURL:              e[KeyGitLabURL].value.(string),
		GitLabClientID:         e[KeyGitLabClientID].value.(string),
		GitLabClientSecret:     e[KeyGitLabClientSecret].value.(string),
		GitLabAllowedGroups:    e[KeyGitLabAllowedGroups].value.([]string),
		LocalLoginEnabled:      e[KeyLocalLoginEnabled].value.(bool),
		SessionIdleTimeout:     dur(KeySessionIdleTimeout),
		SessionAbsoluteTimeout: dur(KeySessionAbsTimeout),

		SonarURL:           e[KeySonarURL].value.(string),
		SonarToken:         e[KeySonarToken].value.(string),
		SonarWebhookSecret: e[KeySonarWebhookSecret].value.(string),
		SonarSyncInterval:  dur(KeySonarSyncInterval),

		IngestAllowedNamespaces: e[KeyIngestAllowedNamespaces].value.([]string),
		IngestProtectedRefsOnly: e[KeyIngestProtectedRefsOnly].value.(bool),
		RetentionDays:           e[KeyRetentionDays].value.(int),

		BackupScheduleEnabled: e[KeyBackupScheduleEnabled].value.(bool),
		BackupScheduleTimes:   e[KeyBackupScheduleTimes].value.([]string),
		BackupTimeZone:        e[KeyBackupTimeZone].value.(string),
		BackupKeepDays:        e[KeyBackupKeepDays].value.(int),
	}
}

// signInPossible refuses settings that leave no way to sign in: local
// sign-in off while GitLab sign-in is incomplete. The Settings page never
// saves that combination, but an environment seed or a direct database
// change can produce it. Failing at start, with the exact fix, keeps what
// the Settings page shows identical to what the server does.
func signInPossible(st *state) error {
	if st.snap.LocalLoginEnabled || st.snap.GitLabEnabled() {
		return nil
	}
	fix := "remove SCANOPSIS_LOCAL_LOGIN_ENABLED=false from the environment"
	if st.entries[KeyLocalLoginEnabled].stored {
		fix = "run DELETE FROM settings WHERE key = 'auth.local_login_enabled' in the database (and remove SCANOPSIS_LOCAL_LOGIN_ENABLED=false from the environment if it is set)"
	}
	return fmt.Errorf("local sign-in is disabled but GitLab sign-in is not fully configured, so nobody could sign in: %s, or configure GitLab sign-in, then start again", fix)
}

// Current returns the current settings without locking or any query.
func (s *Service) Current() Snapshot { return s.cur.Load().snap }

// OnChange registers fn, called with the new snapshot after every successful
// update, in registration order.
func (s *Service) OnChange(fn func(Snapshot)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callbacks = append(s.callbacks, fn)
}

// Reload reads the settings from the database again, as after a restore
// replaced the rows, and calls the OnChange functions with the new
// snapshot. Environment values apply to keys without a row, as at start,
// but are not written. On an error the current settings stay.
func (s *Service) Reload(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.st.ListSettings(ctx)
	if err != nil {
		return fmt.Errorf("reload settings: %w", err)
	}
	stored, err := s.decodeAll(rows)
	if err != nil {
		return fmt.Errorf("reload settings: %w", err)
	}
	next := s.build(stored)
	if err := signInPossible(next); err != nil {
		// The data came from a backup of a working installation, or was
		// changed by hand; serving it is still better than keeping
		// settings that no longer match the database.
		s.log.WarnContext(ctx, "the reloaded settings leave no way to sign in", "error", err)
	}
	s.cur.Store(next)
	for _, fn := range s.callbacks {
		fn(next.snap)
	}
	return nil
}

// validate checks the rules that span several settings and adds one message
// per offending key to fields without replacing earlier messages.
func validate(snap Snapshot, fields map[string]string) {
	add := func(key, msg string) {
		if _, ok := fields[key]; !ok {
			fields[key] = msg
		}
	}
	// The GitLab URL alone verifies CI job tokens; sign-in also needs the
	// client ID and secret, which need each other and the URL.
	if snap.GitLabClientID != "" || snap.GitLabClientSecret != "" {
		for key, set := range map[string]bool{
			KeyGitLabURL:          snap.GitLabURL != "",
			KeyGitLabClientID:     snap.GitLabClientID != "",
			KeyGitLabClientSecret: snap.GitLabClientSecret != "",
		} {
			if !set {
				add(key, "Required when other GitLab sign-in settings are set.")
			}
		}
	}
	if snap.SonarURL != "" || snap.SonarToken != "" || snap.SonarWebhookSecret != "" {
		if snap.SonarURL == "" {
			add(KeySonarURL, "Required when other SonarQube settings are set.")
		}
		if snap.SonarToken == "" {
			add(KeySonarToken, "Required when other SonarQube settings are set.")
		}
	}
	if snap.SonarSyncInterval < MinSonarSyncInterval {
		add(KeySonarSyncInterval, "Must be at least 1m.")
	}
	if !snap.LocalLoginEnabled && !snap.GitLabEnabled() {
		add(KeyLocalLoginEnabled, "Local sign-in can be disabled only when GitLab sign-in is fully configured.")
	}
	if snap.SessionIdleTimeout < MinIdleTimeout {
		add(KeySessionIdleTimeout, "Must be at least 5m.")
	}
	if snap.SessionAbsoluteTimeout < snap.SessionIdleTimeout {
		add(KeySessionAbsTimeout, "Must not be shorter than the idle timeout.")
	}
}

// change is one validated key of a patch.
type change struct {
	key   string
	reset bool
	entry entry
}

// plan validates a patch (key -> decoded JSON, nil meaning reset) against
// the current state and returns the changes in definition order, or the
// problems per key.
func (s *Service) plan(cur *state, patch map[string]any, actor string, now time.Time) ([]change, map[string]string) {
	fields := map[string]string{}
	var changes []change
	for key, raw := range patch {
		d, ok := definitionByKey[key]
		if !ok {
			fields[key] = "Unknown setting."
			continue
		}
		if raw == nil {
			changes = append(changes, change{key: key, reset: true})
			continue
		}
		v, err := coerce(d, raw)
		if err != nil {
			fields[key] = err.Error()
			continue
		}
		changes = append(changes, change{key: key, entry: entry{value: v, source: SourceUI, updatedBy: actor, updatedAt: now, stored: true}})
	}
	slices.SortFunc(changes, func(a, b change) int { return compareKeys(a.key, b.key) })

	// Validate the whole resulting snapshot.
	next := make(map[string]entry, len(Definitions))
	for k, e := range cur.entries {
		if e.stored {
			next[k] = e
		}
	}
	for _, c := range changes {
		if c.reset {
			delete(next, c.key)
		} else {
			next[c.key] = c.entry
		}
	}
	nextState := s.build(next)
	validate(nextState.snap, fields)
	requireSecretWithNewURL(cur, nextState, patch, changes, fields)
	return changes, fields
}

// urlSecrets maps a URL setting to the stored secret Scanopsis sends to it.
var urlSecrets = map[string]string{
	KeySonarURL:  KeySonarToken,
	KeyGitLabURL: KeyGitLabClientSecret,
}

// requireSecretWithNewURL refuses a change of a URL unless the patch also
// gives a new value for the secret that is sent to it, or leaves no secret,
// so that changing an address never sends a stored secret to a new host. A
// reset alone is not enough: it falls back to the value from the
// environment.
func requireSecretWithNewURL(cur, next *state, patch map[string]any, changes []change, fields map[string]string) {
	for _, c := range changes {
		secretKey, ok := urlSecrets[c.key]
		if !ok || c.reset {
			continue
		}
		newURL, _ := c.entry.value.(string)
		oldURL, _ := cur.entries[c.key].value.(string)
		secret, _ := next.entries[secretKey].value.(string)
		if patch[secretKey] != nil || newURL == "" || newURL == oldURL || secret == "" {
			continue
		}
		if _, taken := fields[secretKey]; !taken {
			fields[secretKey] = "Enter this value again when you change the URL, so the stored one is never sent to a new address."
		}
	}
}

func compareKeys(a, b string) int {
	return slices.IndexFunc(Definitions, func(d Definition) bool { return d.Key == a }) -
		slices.IndexFunc(Definitions, func(d Definition) bool { return d.Key == b })
}

// update applies a patch atomically. It returns the problems per key when
// the patch is not valid; nothing changes in that case.
func (s *Service) update(ctx context.Context, patch map[string]any, actor string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.cur.Load()
	now := time.Now().UTC()
	changes, fields := s.plan(cur, patch, actor, now)
	if len(fields) > 0 {
		return fields, nil
	}
	if len(changes) == 0 {
		return nil, nil
	}

	writes := make([]store.UpsertSettingParams, 0, len(changes))
	keys := make([]string, 0, len(changes))
	for _, c := range changes {
		keys = append(keys, c.key)
		if c.reset {
			continue
		}
		p, err := s.encode(definitionByKey[c.key], c.entry.value)
		if err != nil {
			return nil, err
		}
		writes = append(writes, store.UpsertSettingParams{Key: c.key, Value: p.value, SecretEnc: p.secret, UpdatedBy: actor})
	}
	err := s.tx(ctx, func(q Store) error {
		for _, c := range changes {
			if c.reset {
				if err := q.DeleteSetting(ctx, c.key); err != nil {
					return err
				}
			}
		}
		for _, w := range writes {
			if err := q.UpsertSetting(ctx, w); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("save settings: %w", err)
	}

	stored := make(map[string]entry, len(Definitions))
	for k, e := range cur.entries {
		if e.stored {
			stored[k] = e
		}
	}
	for _, c := range changes {
		if c.reset {
			delete(stored, c.key)
		} else {
			stored[c.key] = c.entry
		}
	}
	next := s.build(stored)
	s.cur.Store(next)
	for _, fn := range s.callbacks {
		fn(next.snap)
	}
	s.audit.Record(ctx, actor, audit.ActionSettingsChanged, map[string]any{
		"keys": keys, "changes": auditChanges(cur, next, changes),
	})
	return nil, nil
}

// auditChanges describes each change for the audit log: the effective value
// before and after for ordinary settings, and only that it changed for a
// secret, whose value never reaches the log. A reset is marked as such; its
// "to" is the value that applies afterwards (from the environment or the
// default).
func auditChanges(cur, next *state, changes []change) []map[string]any {
	out := make([]map[string]any, 0, len(changes))
	for _, c := range changes {
		item := map[string]any{"key": c.key}
		if c.reset {
			item["reset"] = true
		}
		if definitionByKey[c.key].Secret {
			item["changed"] = true
		} else {
			item["from"] = cur.entries[c.key].value
			item["to"] = next.entries[c.key].value
		}
		out = append(out, item)
	}
	return out
}
