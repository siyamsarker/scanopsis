// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package backup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/audit"
	"github.com/siyamsarker/scanopsis/internal/auth"
	"github.com/siyamsarker/scanopsis/internal/retention"
	"github.com/siyamsarker/scanopsis/internal/settings"
	"github.com/siyamsarker/scanopsis/internal/sonar"
	"github.com/siyamsarker/scanopsis/internal/store"
	"github.com/siyamsarker/scanopsis/internal/store/storetest"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type auditEvent struct {
	actor, action string
	detail        map[string]any
}

type memAudit struct {
	mu     sync.Mutex
	events []auditEvent
}

func (a *memAudit) Record(_ context.Context, actor, action string, detail map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, auditEvent{actor, action, detail})
}

func (a *memAudit) of(action string) []auditEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []auditEvent
	for _, e := range a.events {
		if e.action == action {
			out = append(out, e)
		}
	}
	return out
}

// fakeClock drives the scheduler: the test sets the time and fires the
// timers the scheduler asks for.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers chan *fakeTimer
}

type fakeTimer struct {
	d       time.Duration
	c       chan time.Time
	stopped atomic.Bool
}

func (f *fakeTimer) C() <-chan time.Time { return f.c }
func (f *fakeTimer) Stop() bool          { return !f.stopped.Swap(true) }

func newFakeClock(at time.Time) *fakeClock {
	return &fakeClock{now: at, timers: make(chan *fakeTimer, 100)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) set(at time.Time) {
	c.mu.Lock()
	c.now = at
	c.mu.Unlock()
}

func (c *fakeClock) newTimer(d time.Duration) timer {
	t := &fakeTimer{d: d, c: make(chan time.Time, 1)}
	c.timers <- t
	return t
}

// next returns the next timer the scheduler started and is still waiting on.
func (c *fakeClock) next(t *testing.T) *fakeTimer {
	t.Helper()
	for {
		select {
		case tm := <-c.timers:
			if len(c.timers) == 0 && !tm.stopped.Load() {
				return tm
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the scheduler started no timer")
		}
	}
}

// fire sets the clock to at and fires the timer.
func (c *fakeClock) fire(tm *fakeTimer, at time.Time) {
	c.set(at)
	tm.c <- at
}

type svcEnv struct {
	pool  *pgxpool.Pool
	dir   *Dir
	conf  *settings.Service
	audit *memAudit
	svc   *Service
	// after counts AfterRestore calls; restoring records Restoring() in it.
	after          atomic.Int32
	afterRestoring atomic.Bool
}

func newSvcEnv(t *testing.T, seeds map[string]any) *svcEnv {
	t.Helper()
	pool := storetest.New(t)
	return newSvcEnvOn(t, pool, t.TempDir(), seeds)
}

func newSvcEnvOn(t *testing.T, pool *pgxpool.Pool, folder string, seeds map[string]any) *svcEnv {
	t.Helper()
	ctx := context.Background()
	q := store.New(pool)
	tx := func(ctx context.Context, fn func(settings.Store) error) error {
		return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return fn(store.New(tx)) })
	}
	rec := &memAudit{}
	conf, err := settings.New(ctx, q, tx, testBox(t), seeds, rec, quiet)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := OpenDir(folder, testFingerprint(t))
	if err != nil {
		t.Fatal(err)
	}
	e := &svcEnv{pool: pool, dir: dir, conf: conf, audit: rec}
	e.svc = NewService(ServiceOptions{
		Pool: pool, Dir: dir, Settings: conf, Audit: rec, Log: quiet, KeyFingerprint: testFingerprint(t),
		Dumper: &Dumper{Pool: pool, Dir: dir, App: "0.4.0", Commit: "abc", KeyFingerprint: testFingerprint(t), SigningKey: testSigning(t)},
		AfterRestore: func(context.Context) error {
			e.after.Add(1)
			e.afterRestoring.Store(e.svc.Restoring())
			return nil
		},
		ExclusiveLocks: []int64{retention.AdvisoryLockKey, sonar.AdvisoryLockKey},
	})
	t.Cleanup(e.svc.Close)
	return e
}

// wait returns the status once the running job has ended.
func (e *svcEnv) wait(t *testing.T) JobStatus {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		st := e.svc.Status()
		if st.State == JobDone || st.State == JobFailed {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the job did not end: %+v", e.svc.Status())
	return JobStatus{}
}

func (e *svcEnv) backup(t *testing.T) string {
	t.Helper()
	if _, err := e.svc.StartBackup(KindManual, "local:alice"); err != nil {
		t.Fatal(err)
	}
	st := e.wait(t)
	if st.State != JobDone {
		t.Fatalf("backup: %+v", st)
	}
	return st.Name
}

func (e *svcEnv) count(t *testing.T, q string) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func names(t *testing.T, d *Dir) []string {
	t.Helper()
	list, err := d.List()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, b := range list {
		out = append(out, b.Name)
	}
	slices.Sort(out)
	return out
}

func TestServiceBackup(t *testing.T) {
	e := newSvcEnv(t, nil)
	if st := e.svc.Status(); st.State != JobIdle {
		t.Fatalf("status before any job = %+v", st)
	}
	st, err := e.svc.StartBackup(KindManual, "local:alice")
	if err != nil {
		t.Fatal(err)
	}
	if st.State != JobBackingUp || st.Kind != KindManual || st.StartedAt == nil || st.FinishedAt != nil {
		t.Errorf("started status = %+v", st)
	}
	st = e.wait(t)
	if st.State != JobDone || st.Kind != KindManual || st.Percent != 100 || st.FinishedAt == nil || st.Progress.TablesTotal == 0 ||
		st.Progress.TablesDone != st.Progress.TablesTotal {
		t.Fatalf("finished status = %+v", st)
	}
	p, err := e.dir.Path(st.Name)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	created := e.audit.of(audit.ActionBackupCreated)
	if len(created) != 1 || created[0].actor != "local:alice" || created[0].detail["name"] != st.Name ||
		created[0].detail["kind"] != KindManual || created[0].detail["size"] != fi.Size() {
		t.Errorf("audit = %+v", created)
	}
	if _, err := e.svc.StartBackup(KindUploaded, "local:alice"); err == nil {
		t.Error("an uploaded backup cannot be taken")
	}
}

func TestServiceBusy(t *testing.T) {
	e := newSvcEnv(t, nil)
	name := e.backup(t)

	release := make(chan struct{})
	real := e.svc.dump
	e.svc.dump = func(ctx context.Context, kind Kind, actor string, progress func(Progress)) (Info, error) {
		<-release
		return real(ctx, kind, actor, progress)
	}
	if _, err := e.svc.StartBackup(KindManual, "local:alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.StartBackup(KindManual, "local:bob"); !errors.Is(err, ErrBusy) {
		t.Errorf("second backup: err = %v, want ErrBusy", err)
	}
	if _, err := e.svc.StartRestore(name, "local:bob", true, true); !errors.Is(err, ErrBusy) {
		t.Errorf("restore during a backup: err = %v, want ErrBusy", err)
	}
	if st := e.svc.Status(); st.State != JobBackingUp {
		t.Errorf("status = %+v", st)
	}
	close(release)
	if st := e.wait(t); st.State != JobDone {
		t.Fatalf("status = %+v", st)
	}
	if _, err := e.svc.StartBackup(KindManual, "local:alice"); err != nil {
		t.Fatalf("backup after the first ended: %v", err)
	}
	e.wait(t)

	// A name that is not a backup, or a backup that is not there, is
	// refused before any job starts.
	for _, bad := range []string{"../etc/passwd", "scanopsis-backup-20260101T000000Z-manual-zzzzzz.tar.gz"} {
		if _, err := e.svc.StartRestore(bad, "local:alice", true, true); err == nil || errors.Is(err, ErrBusy) {
			t.Errorf("StartRestore(%q) err = %v", bad, err)
		}
	}
}

// TestServiceRestoreRefusedAtStart checks that a backup of another key is
// refused before a job starts, so no backup of the current data is taken
// for a restore that cannot happen.
func TestServiceRestoreRefusedAtStart(t *testing.T) {
	e := newSvcEnv(t, nil)
	src, err := e.dir.Path(e.backup(t))
	if err != nil {
		t.Fatal(err)
	}
	other := rebuild(t, src, func(m *Manifest) { m.KeyFingerprint = "other" }, nil)
	name := e.dir.NewName(KindUploaded, time.Now())
	if err := os.Rename(other, filepath.Join(e.dir.Root(), name)); err != nil {
		t.Fatal(err)
	}
	before := e.svc.Status()
	if _, err := e.svc.StartRestore(name, "local:alice", false, true); !errors.Is(err, ErrKeyMismatch) {
		t.Fatalf("err = %v, want ErrKeyMismatch", err)
	}
	if st := e.svc.Status(); st.State != before.State || st.Name != before.Name || len(e.audit.of(audit.ActionBackupCreated)) != 1 {
		t.Errorf("a job ran: %+v", st)
	}
	if _, err := e.svc.StartRestore(name, "", false, true); err == nil {
		t.Error("a restore without an actor started")
	}
}

// TestServiceLockAcrossInstances checks that a job of another instance,
// which holds the backup lock, makes a job here fail instead of running
// beside it.
func TestServiceLockAcrossInstances(t *testing.T) {
	e := newSvcEnv(t, nil)
	holder, err := e.pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()
	mustExec(t, holder, "SELECT pg_advisory_lock($1)", AdvisoryLockKey)
	if _, err := e.svc.StartBackup(KindManual, "local:alice"); err != nil {
		t.Fatal(err)
	}
	st := e.wait(t)
	if st.State != JobFailed || !strings.Contains(st.Message, "another Scanopsis instance") {
		t.Errorf("status = %+v", st)
	}
	if f := e.audit.of(audit.ActionBackupFailed); len(f) != 1 || f[0].detail["kind"] != KindManual {
		t.Errorf("audit = %+v", f)
	}
	mustExec(t, holder, "SELECT pg_advisory_unlock($1)", AdvisoryLockKey)
	e.backup(t)
}

func TestServiceRestoreWaitsForMaintenance(t *testing.T) {
	e := newSvcEnv(t, nil)
	name := e.backup(t)
	var restoreCalls atomic.Int32
	real := e.svc.restore
	e.svc.restore = func(ctx context.Context, pool *pgxpool.Pool, path string, o RestoreOptions) error {
		restoreCalls.Add(1)
		return real(ctx, pool, path, o)
	}

	holder, err := e.pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()

	// The data retention job is running and does not finish in time.
	mustExec(t, holder, "SELECT pg_advisory_lock($1)", retention.AdvisoryLockKey)
	e.svc.lockWait = 300 * time.Millisecond
	if _, err := e.svc.StartRestore(name, "local:alice", false, true); err != nil {
		t.Fatal(err)
	}
	st := e.wait(t)
	if st.State != JobFailed || st.Message != "another maintenance job is running; try again in a few minutes" || e.svc.Restoring() {
		t.Fatalf("status = %+v", st)
	}
	if restoreCalls.Load() != 0 || len(e.audit.of(audit.ActionBackupCreated)) != 1 {
		t.Error("the restore or its safety backup ran without the locks")
	}
	if f := e.audit.of(audit.ActionBackupRestoreFailed); len(f) != 1 || f[0].detail["name"] != name || f[0].actor != "local:alice" {
		t.Errorf("audit = %+v", f)
	}

	// It finishes while the restore waits: the restore goes ahead, and
	// neither background job can start until the restore is over.
	e.svc.lockWait = time.Minute
	var retentionSkipped, sonarLocked atomic.Bool
	e.svc.restore = func(ctx context.Context, pool *pgxpool.Pool, path string, o RestoreOptions) error {
		res := retention.New(pool, e.conf, quiet).RunOnce(ctx)
		retentionSkipped.Store(res.Skipped)
		var got bool
		if err := pool.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", sonar.AdvisoryLockKey).Scan(&got); err == nil {
			sonarLocked.Store(!got)
		}
		return real(ctx, pool, path, o)
	}
	if _, err := e.svc.StartRestore(name, "local:alice", true, true); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if st := e.svc.Status(); st.State != JobRestoring || e.svc.Restoring() {
		t.Errorf("while waiting for the lock: %+v, restoring %v", st, e.svc.Restoring())
	}
	mustExec(t, holder, "SELECT pg_advisory_unlock($1)", retention.AdvisoryLockKey)
	if st := e.wait(t); st.State != JobDone {
		t.Fatalf("status = %+v", st)
	}
	if !retentionSkipped.Load() || !sonarLocked.Load() {
		t.Errorf("during the restore: retention skipped %v, SonarQube lock held %v", retentionSkipped.Load(), sonarLocked.Load())
	}
	// Released afterwards.
	if res := retention.New(e.pool, e.conf, quiet).RunOnce(context.Background()); res.Skipped {
		t.Error("the retention lock is still held after the restore")
	}
}

func TestServiceRestoreModeWindow(t *testing.T) {
	e := newSvcEnv(t, nil)
	mustExec(t, e.pool, `INSERT INTO services (name, gitlab_project_path, created_via) VALUES ('payments-api', 'example/payments-api', 'manual')`)
	name := e.backup(t)
	mustExec(t, e.pool, `INSERT INTO services (name, gitlab_project_path, created_via) VALUES ('orders-api', 'example/orders-api', 'manual')`)

	var safetyRestoring, restoreRestoring atomic.Bool
	var safetyState, restoreState atomic.Value
	realDump, realRestore := e.svc.dump, e.svc.restore
	e.svc.dump = func(ctx context.Context, kind Kind, actor string, progress func(Progress)) (Info, error) {
		if kind == KindBeforeRestore {
			safetyRestoring.Store(e.svc.Restoring())
			safetyState.Store(e.svc.Status().State)
		}
		return realDump(ctx, kind, actor, progress)
	}
	e.svc.restore = func(ctx context.Context, pool *pgxpool.Pool, path string, o RestoreOptions) error {
		restoreRestoring.Store(e.svc.Restoring())
		restoreState.Store(e.svc.Status().State)
		if o.Actor != "local:alice" || !o.KeepHostSettings || o.KeyFingerprint != testFingerprint(t) || o.Progress == nil {
			t.Errorf("options = %+v", o)
		}
		return realRestore(ctx, pool, path, o)
	}

	st, err := e.svc.StartRestore(name, "local:alice", false, true)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != JobBackingUp || e.svc.Restoring() {
		t.Errorf("started: %+v", st)
	}
	st = e.wait(t)
	if st.State != JobDone || st.Name != name || st.Percent != 100 {
		t.Fatalf("status = %+v", st)
	}
	if safetyRestoring.Load() || safetyState.Load() != JobBackingUp {
		t.Errorf("during the safety backup: restoring %v, state %v", safetyRestoring.Load(), safetyState.Load())
	}
	if !restoreRestoring.Load() || restoreState.Load() != JobRestoring {
		t.Errorf("during the restore: restoring %v, state %v", restoreRestoring.Load(), restoreState.Load())
	}
	if e.after.Load() != 1 || !e.afterRestoring.Load() {
		t.Errorf("AfterRestore calls %d, restoring during it %v", e.after.Load(), e.afterRestoring.Load())
	}
	if e.svc.Restoring() {
		t.Error("still restoring after success")
	}
	if n := e.count(t, `SELECT count(*) FROM services`); n != 1 {
		t.Errorf("%d services after the restore, want 1", n)
	}
	var safety []Info
	list, _ := e.dir.List()
	for _, b := range list {
		if b.Kind == KindBeforeRestore {
			safety = append(safety, b)
		}
	}
	if len(safety) != 1 || safety[0].CreatedBy != "local:alice" {
		t.Errorf("before-restore backups = %+v", safety)
	}

	// A failed restore: restore mode ends, the failure is recorded.
	e.svc.restore = func(ctx context.Context, pool *pgxpool.Pool, path string, o RestoreOptions) error {
		restoreRestoring.Store(e.svc.Restoring())
		return errors.New("restore backup: the file is damaged")
	}
	restoreRestoring.Store(false)
	if _, err := e.svc.StartRestore(name, "local:alice", true, true); err != nil {
		t.Fatal(err)
	}
	st = e.wait(t)
	if st.State != JobFailed || !strings.Contains(st.Message, "the file is damaged") || e.svc.Restoring() || !restoreRestoring.Load() {
		t.Errorf("failed restore: %+v, restoring %v", st, e.svc.Restoring())
	}
	if f := e.audit.of(audit.ActionBackupRestoreFailed); len(f) != 1 || f[0].detail["name"] != name ||
		!strings.Contains(f[0].detail["error"].(string), "the file is damaged") {
		t.Errorf("audit = %+v", f)
	}
	if e.after.Load() != 1 {
		t.Error("AfterRestore ran after a failed restore")
	}

	// A failed safety backup stops the restore before restore mode.
	e.svc.restore = func(context.Context, *pgxpool.Pool, string, RestoreOptions) error {
		t.Error("restored without the safety backup")
		return nil
	}
	e.svc.dump = func(context.Context, Kind, string, func(Progress)) (Info, error) {
		return Info{}, errors.New("take backup: not enough free space for a backup")
	}
	if _, err := e.svc.StartRestore(name, "local:alice", false, true); err != nil {
		t.Fatal(err)
	}
	st = e.wait(t)
	if st.State != JobFailed || !strings.Contains(st.Message, "not enough free space") || e.svc.Restoring() {
		t.Errorf("failed safety backup: %+v", st)
	}
	if f := e.audit.of(audit.ActionBackupFailed); len(f) != 1 || f[0].detail["kind"] != KindBeforeRestore {
		t.Errorf("backup.failed audit = %+v", f)
	}
}

// oldBackup writes a file with a backup name made at the given time.
// Its content is not a backup; the list falls back to the name.
func oldBackup(t *testing.T, d *Dir, kind Kind, at time.Time) string {
	t.Helper()
	name := d.NewName(kind, at)
	if err := os.WriteFile(filepath.Join(d.Root(), name), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	return name
}

func scheduleSeeds(times ...string) map[string]any {
	return map[string]any{
		settings.KeyBackupScheduleEnabled: true,
		settings.KeyBackupScheduleTimes:   times,
		settings.KeyBackupTimeZone:        "Europe/Berlin",
		settings.KeyBackupKeepDays:        7,
	}
}

func (e *svcEnv) patchSettings(t *testing.T, body string) {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/api/v1/settings", strings.NewReader(body))
	req = req.WithContext(auth.WithPrincipal(req.Context(), &auth.Principal{Source: "local", Username: "alice"}))
	rr := httptest.NewRecorder()
	e.conf.UpdateHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("patch %s: %d %s", body, rr.Code, rr.Body)
	}
}

// startScheduler runs the scheduler on a fake clock until the test ends.
func (e *svcEnv) startScheduler(t *testing.T, clock *fakeClock) {
	t.Helper()
	e.svc.now = clock.Now
	e.svc.newTimer = clock.newTimer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.svc.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

func (e *svcEnv) waitScheduled(t *testing.T, runs int) ScheduleStatus {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if n := len(e.audit.of(audit.ActionBackupCreated)) + len(e.audit.of(audit.ActionBackupFailed)); n >= runs {
			if s := e.svc.Schedule(); s.LastRunAt != nil && e.svc.Status().State != JobBackingUp {
				return s
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no scheduled run; status %+v", e.svc.Status())
	return ScheduleStatus{}
}

func TestServiceScheduledRun(t *testing.T) {
	e := newSvcEnv(t, scheduleSeeds("03:00"))
	// 03:00 in Berlin is 02:00 UTC in November.
	start := utc("2026-11-23T01:59:00Z")
	keep := []string{
		oldBackup(t, e.dir, KindScheduled, start.Add(-24*time.Hour)),
		oldBackup(t, e.dir, KindManual, start.Add(-400*24*time.Hour)),
		oldBackup(t, e.dir, KindUploaded, start.Add(-400*24*time.Hour)),
		oldBackup(t, e.dir, KindBeforeRestore, start.Add(-400*24*time.Hour)),
	}
	expired := []string{
		oldBackup(t, e.dir, KindScheduled, start.Add(-8*24*time.Hour)),
		oldBackup(t, e.dir, KindScheduled, start.Add(-30*24*time.Hour)),
	}
	clock := newFakeClock(start)
	e.startScheduler(t, clock)

	tm := clock.next(t)
	if tm.d != time.Minute {
		t.Errorf("first wait = %v, want 1m", tm.d)
	}
	// A wake-up before the time runs nothing.
	clock.fire(tm, start.Add(30*time.Second))
	tm = clock.next(t)
	if tm.d != 30*time.Second {
		t.Errorf("second wait = %v, want 30s", tm.d)
	}
	if n := len(e.audit.of(audit.ActionBackupCreated)); n != 0 {
		t.Fatalf("%d backups before the scheduled time", n)
	}
	runAt := utc("2026-11-23T02:00:00Z")
	clock.fire(tm, runAt)
	sched := e.waitScheduled(t, 1)

	created := e.audit.of(audit.ActionBackupCreated)
	if len(created) != 1 || created[0].actor != "scheduler" || created[0].detail["kind"] != KindScheduled {
		t.Fatalf("audit = %+v", created)
	}
	newName := created[0].detail["name"].(string)
	want := append(slices.Clone(keep), newName)
	slices.Sort(want)
	if got := names(t, e.dir); !slices.Equal(got, want) {
		t.Errorf("backups after the run = %v\nwant %v (expired: %v)", got, want, expired)
	}
	if !sched.OK || sched.Error != "" || !sched.LastRunAt.Equal(runAt) {
		t.Errorf("schedule status = %+v", sched)
	}
	raw, err := os.ReadFile(filepath.Join(e.dir.Root(), ".schedule-status.json"))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk ScheduleStatus
	if err := json.Unmarshal(raw, &onDisk); err != nil || !onDisk.OK || !onDisk.LastRunAt.Equal(runAt) {
		t.Errorf("status file %s: %v", raw, err)
	}
	if fi, err := os.Stat(filepath.Join(e.dir.Root(), ".schedule-status.json")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("status file mode: %v %v", fi, err)
	}

	// A new time takes effect at once. The scheduler first waits for the
	// next day, then for the new time.
	if tm := clock.next(t); tm.d != time.Minute {
		t.Errorf("wait after the run = %v", tm.d)
	}
	e.patchSettings(t, `{"backup.schedule_times":["03:30"]}`)
	tm = clock.next(t)
	clock.fire(tm, utc("2026-11-23T02:29:59Z"))
	tm = clock.next(t)
	if tm.d != time.Second || len(e.audit.of(audit.ActionBackupCreated)) != 1 {
		t.Fatalf("wait = %v, backups %d", tm.d, len(e.audit.of(audit.ActionBackupCreated)))
	}
	clock.fire(tm, utc("2026-11-23T02:30:00Z"))
	e.waitScheduled(t, 2)

	// Turned off: no timer at all, so nothing runs.
	e.patchSettings(t, `{"backup.schedule_enabled":false}`)
	time.Sleep(50 * time.Millisecond)
	for len(clock.timers) > 0 {
		if tm := <-clock.timers; !tm.stopped.Load() {
			t.Errorf("a timer of %v runs while the schedule is off", tm.d)
		}
	}

	// The status is read back at start.
	other := newSvcEnvOn(t, e.pool, e.dir.Root(), nil)
	if s := other.svc.Schedule(); !s.OK || s.LastRunAt == nil || !s.LastRunAt.Equal(utc("2026-11-23T02:30:00Z")) {
		t.Errorf("status after restart = %+v", s)
	}
}

// TestServiceScheduledWaitsForJob checks that a scheduled time that comes
// while another backup runs waits for it and then runs.
func TestServiceScheduledWaitsForJob(t *testing.T) {
	e := newSvcEnv(t, scheduleSeeds("03:00"))
	release := make(chan struct{})
	real := e.svc.dump
	var kinds []Kind
	var mu sync.Mutex
	e.svc.dump = func(ctx context.Context, kind Kind, actor string, progress func(Progress)) (Info, error) {
		mu.Lock()
		kinds = append(kinds, kind)
		mu.Unlock()
		if kind == KindManual {
			<-release
		}
		return real(ctx, kind, actor, progress)
	}
	clock := newFakeClock(utc("2026-11-23T01:59:30Z"))
	e.startScheduler(t, clock)
	tm := clock.next(t)
	if _, err := e.svc.StartBackup(KindManual, "local:alice"); err != nil {
		t.Fatal(err)
	}
	clock.fire(tm, utc("2026-11-23T02:00:00Z"))
	time.Sleep(50 * time.Millisecond)
	if s := e.svc.Schedule(); s.LastRunAt != nil {
		t.Fatalf("the scheduled backup ran beside another job: %+v", s)
	}
	close(release)
	if s := e.waitScheduled(t, 2); !s.OK {
		t.Fatalf("schedule status = %+v", s)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(kinds, []Kind{KindManual, KindScheduled}) {
		t.Errorf("backups in order %v", kinds)
	}
}

func TestServiceFailedScheduledRecorded(t *testing.T) {
	pool := storetest.New(t)
	folder := t.TempDir()
	e := newSvcEnvOn(t, pool, folder, scheduleSeeds("03:00"))
	rec := audit.New(store.New(pool), quiet)
	e.svc.o.Audit = rec
	if err := os.Chmod(folder, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(folder, 0o700) }()
	if f, err := os.CreateTemp(folder, "x"); err == nil { // running as root
		_ = f.Close()
		t.Skip("folder permissions do not apply to this user")
	}

	clock := newFakeClock(utc("2026-11-23T01:59:59Z"))
	e.startScheduler(t, clock)
	tm := clock.next(t)
	clock.fire(tm, utc("2026-11-23T02:00:00Z"))

	deadline := time.Now().Add(30 * time.Second)
	for e.svc.Schedule().LastRunAt == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	s := e.svc.Schedule()
	if s.OK || s.LastRunAt == nil || !strings.Contains(s.Error, folder) {
		t.Fatalf("schedule status = %+v", s)
	}
	var actor string
	var detail map[string]any
	if err := pool.QueryRow(context.Background(), `SELECT actor, detail FROM audit_log WHERE action = 'backup.failed'`).Scan(&actor, &detail); err != nil {
		t.Fatal(err)
	}
	if actor != "scheduler" || detail["kind"] != "scheduled" || !strings.Contains(detail["error"].(string), folder) {
		t.Errorf("audit row: %s %v", actor, detail)
	}
	if st := e.svc.Status(); st.State != JobFailed || st.Kind != KindScheduled {
		t.Errorf("job status = %+v", st)
	}
}
