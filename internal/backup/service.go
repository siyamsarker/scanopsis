// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/audit"
	"github.com/siyamsarker/scanopsis/internal/auth"
	"github.com/siyamsarker/scanopsis/internal/settings"
)

// AdvisoryLockKey is the PostgreSQL advisory lock held for every backup and
// restore job, so two instances of Scanopsis on one database never run one
// at the same time. It differs from the keys of the other background jobs.
const AdvisoryLockKey int64 = 0x53434e424b // "SCNBK"

// ScheduleActor is the actor of scheduled backups in the audit log and in
// the manifest.
const ScheduleActor = "scheduler"

// statusFile holds the outcome of the last scheduled backup, in the folder.
const statusFile = ".schedule-status.json"

// defaultLockWait is how long a restore waits for the other maintenance
// jobs (data retention, the SonarQube sync) to finish.
const defaultLockWait = 2 * time.Minute

// closeTimeout bounds the closing of the lock connection.
const closeTimeout = 5 * time.Second

// maxWait is the longest the scheduler sleeps before it looks at the clock
// again, so a clock that is set or jumps is noticed within a minute.
const maxWait = time.Minute

var (
	// ErrBusy means a backup or restore job is already running.
	ErrBusy = errors.New("a backup or restore is already running")
	// ErrMaintenance means another maintenance job held its lock for longer
	// than a restore waits.
	ErrMaintenance = errors.New("another maintenance job is running; try again in a few minutes")
	// errOtherInstance means another instance runs a backup or restore.
	errOtherInstance = errors.New("another Scanopsis instance is running a backup or restore on this database; try again when it has finished")
	// errClosed means the server is shutting down.
	errClosed = errors.New("the server is shutting down")
	// errInternal replaces a panic of a job.
	errInternal = errors.New("an unexpected error stopped the job; see the server log for details")
	// errStopped means the service was closed while the job ran.
	errStopped = fmt.Errorf("stopped by a shutdown: %w", context.Canceled)
)

// JobState is the state of the backup or restore job.
type JobState string

// Job states. A finished job stays done or failed until the next one starts.
const (
	JobIdle      JobState = "idle"
	JobBackingUp JobState = "backing_up"
	JobRestoring JobState = "restoring"
	JobDone      JobState = "done"
	JobFailed    JobState = "failed"
)

// JobStatus describes the current or last job. For a backup, Kind and Name
// describe the new file once it is written; for a restore, they describe
// the backup being restored (while the backup before the restore runs,
// Kind is KindBeforeRestore). Message says what happens or went wrong in a
// sentence for people.
type JobStatus struct {
	State      JobState   `json:"state"`
	Kind       Kind       `json:"kind,omitempty"`
	Name       string     `json:"name,omitempty"`
	Progress   Progress   `json:"progress"`
	Percent    int        `json:"percent"`
	Message    string     `json:"message,omitempty"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

// ScheduleStatus is the outcome of the last scheduled backup. It is kept in
// the backup folder, so it survives a restart and a restore.
type ScheduleStatus struct {
	LastRunAt *time.Time `json:"last_run_at"`
	OK        bool       `json:"ok"`
	Error     string     `json:"error"`
}

// ServiceOptions configure a Service. Dir and Dumper may be nil when the
// backup folder cannot be used; every job then fails with ErrFolder.
type ServiceOptions struct {
	Pool     *pgxpool.Pool
	Dir      *Dir
	Dumper   *Dumper
	Settings *settings.Service
	Audit    auth.AuditRecorder
	Log      *slog.Logger
	// KeyFingerprint is the fingerprint of the running secret key; a
	// restore refuses backups of another key.
	KeyFingerprint string
	// AfterRestore runs after a successful restore, while restore mode
	// still holds, to reload what the server keeps in memory.
	AfterRestore func(ctx context.Context) error
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// ExclusiveLocks are the advisory lock keys of the other background
	// jobs that write to the database. A restore holds them all while it
	// runs, so none of those jobs runs during a restore.
	ExclusiveLocks []int64
}

// timer is the part of time.Timer the scheduler uses.
type timer interface {
	C() <-chan time.Time
	Stop() bool
}

type realTimer struct{ t *time.Timer }

func (r realTimer) C() <-chan time.Time { return r.t.C }
func (r realTimer) Stop() bool          { return r.t.Stop() }

// Service runs backup and restore jobs, one at a time, and the backup
// schedule. Jobs run in the background on a context of their own, which
// Close cancels; a request that starts one can return at once.
type Service struct {
	o        ServiceOptions
	now      func() time.Time
	newTimer func(time.Duration) timer
	lockWait time.Duration
	dump     func(ctx context.Context, kind Kind, actor string, progress func(Progress)) (Info, error)
	restore  func(ctx context.Context, pool *pgxpool.Pool, path string, o RestoreOptions) error
	// expiring, if not nil, is called with the names expiry is about to
	// delete.
	expiring func(names []string)

	ctx    context.Context
	cancel context.CancelFunc
	jobs   sync.WaitGroup

	restoring atomic.Bool
	progress  atomic.Pointer[Progress]

	mu      sync.Mutex
	running bool
	idle    chan struct{} // closed when the running job ends
	status  JobStatus
	sched   ScheduleStatus
}

// NewService returns a Service. It reads the status of the last scheduled
// backup from the folder. Start the schedule with go s.Run(ctx).
func NewService(o ServiceOptions) *Service {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	s := &Service{
		o: o, now: o.Now, lockWait: defaultLockWait,
		newTimer: func(d time.Duration) timer { return realTimer{time.NewTimer(d)} },
		restore:  Restore,
		status:   JobStatus{State: JobIdle},
	}
	if s.now == nil {
		s.now = time.Now
	}
	if o.Dumper != nil {
		s.dump = o.Dumper.Dump
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	idle := make(chan struct{})
	close(idle)
	s.idle = idle
	if o.Dir != nil {
		if b, err := os.ReadFile(filepath.Join(o.Dir.Root(), statusFile)); err == nil {
			if err := json.Unmarshal(b, &s.sched); err != nil {
				o.Log.Warn("ignoring an unreadable backup schedule status", "file", statusFile, "error", err)
				s.sched = ScheduleStatus{}
			}
		}
	}
	return s
}

// Close cancels the running job, if any, and waits for it to end. A
// cancelled backup leaves no file; a cancelled restore changes nothing.
func (s *Service) Close() {
	s.stop()
	s.jobs.Wait()
}

// stop cancels the jobs' context. It holds mu, so begin either claims the
// slot (and adds to jobs) before Close waits, or sees the service closed.
func (s *Service) stop() {
	s.mu.Lock()
	s.cancel()
	s.mu.Unlock()
}

// Status returns the state of the current or last job.
func (s *Service) Status() JobStatus {
	s.mu.Lock()
	st := s.status
	s.mu.Unlock()
	if p := s.progress.Load(); p != nil && (st.State == JobBackingUp || st.State == JobRestoring) {
		st.Progress = *p
	}
	switch {
	case st.State == JobDone:
		st.Percent = 100
	case st.Progress.TablesTotal > 0:
		st.Percent = min(st.Progress.TablesDone*100/st.Progress.TablesTotal, 99)
	}
	return st
}

// Restoring reports whether a restore is replacing the data right now. The
// server turns requests away while it is true.
func (s *Service) Restoring() bool { return s.restoring.Load() }

// Schedule returns the outcome of the last scheduled backup.
func (s *Service) Schedule() ScheduleStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sched
}

// begin claims the job slot. It returns ErrBusy when a job runs.
func (s *Service) begin(st JobStatus) (JobStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx.Err() != nil {
		return JobStatus{}, errClosed
	}
	if s.running {
		return JobStatus{}, ErrBusy
	}
	now := s.now().UTC()
	st.StartedAt = &now
	s.running = true
	s.idle = make(chan struct{})
	s.status = st
	s.progress.Store(nil)
	s.jobs.Add(1)
	return st, nil
}

// update changes the status of the running job.
func (s *Service) update(fn func(*JobStatus)) {
	s.mu.Lock()
	fn(&s.status)
	s.mu.Unlock()
}

// finish ends the running job with the final status fn sets.
func (s *Service) finish(fn func(*JobStatus)) {
	s.mu.Lock()
	if p := s.progress.Load(); p != nil {
		s.status.Progress = *p
	}
	fn(&s.status)
	now := s.now().UTC()
	s.status.FinishedAt = &now
	s.running = false
	close(s.idle)
	s.mu.Unlock()
	s.jobs.Done()
}

func (s *Service) setProgress(p Progress) { s.progress.Store(&p) }

// usable reports why no job can run, or nil.
func (s *Service) usable() error {
	if s.o.Dir == nil || s.dump == nil {
		return fmt.Errorf("%w: the backup folder could not be opened when Scanopsis started; see the server log for the reason and restart after fixing it", ErrFolder)
	}
	return nil
}

// StartBackup starts a backup of the given kind (manual, scheduled or
// before-restore) in the background and returns at once. actor is who asked
// for it.
func (s *Service) StartBackup(kind Kind, actor string) (JobStatus, error) {
	switch kind {
	case KindManual, KindScheduled, KindBeforeRestore:
	default:
		return JobStatus{}, fmt.Errorf("a %q backup cannot be taken", kind)
	}
	if err := s.usable(); err != nil {
		return JobStatus{}, err
	}
	st, err := s.begin(JobStatus{State: JobBackingUp, Kind: kind, Message: "Taking a backup."})
	if err != nil {
		return JobStatus{}, err
	}
	go func() { _, _ = s.backupJob(kind, actor, nil) }()
	return st, nil
}

// backupJob runs a backup in the job slot begin claimed. then, if not nil,
// runs after a successful backup while the job still holds the slot and
// its lock.
func (s *Service) backupJob(kind Kind, actor string, then func(context.Context)) (Info, error) {
	ctx := s.ctx
	var info Info
	err := s.guard(ctx, func() error {
		release, err := s.lock(ctx, false)
		if err != nil {
			return err
		}
		defer release()
		if info, err = s.takeBackup(ctx, kind, actor); err != nil {
			return err
		}
		if then != nil {
			_ = s.guard(ctx, func() error { then(ctx); return nil })
		}
		return nil
	})
	err = s.stopped(err)
	if err != nil {
		s.failed(ctx, kind, actor, err)
	}
	s.finish(func(st *JobStatus) {
		if err != nil {
			st.State, st.Message = JobFailed, message(err)
			return
		}
		st.State, st.Name, st.Message = JobDone, info.Name, "Backup finished."
	})
	return info, err
}

// guard runs fn and turns a panic into errInternal, logged with its stack.
// The deferred calls of fn, which release the locks, run on the way.
func (s *Service) guard(ctx context.Context, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			s.o.Log.ErrorContext(ctx, "a backup job stopped with a panic", "panic", r, "stack", string(debug.Stack()))
			err = errInternal
		}
	}()
	return fn()
}

// stopped replaces err with errStopped when the job ended because the
// service was closed: a shutdown is not a failure of the job.
func (s *Service) stopped(err error) error {
	if err != nil && s.ctx.Err() != nil {
		return errStopped
	}
	return err
}

// takeBackup writes a backup under the job lock and audits it. The caller
// records a failure.
func (s *Service) takeBackup(ctx context.Context, kind Kind, actor string) (Info, error) {
	info, err := s.dump(ctx, kind, actor, s.setProgress)
	if err != nil {
		return Info{}, err
	}
	s.o.Audit.Record(context.WithoutCancel(ctx), actor, audit.ActionBackupCreated, map[string]any{"name": info.Name, "kind": info.Kind, "size": info.Size})
	s.o.Log.InfoContext(ctx, "backup taken", "name", info.Name, "kind", info.Kind, "size", info.Size, "actor", actor)
	return info, nil
}

// failed records a backup that could not be taken. A backup stopped by a
// shutdown is only logged.
func (s *Service) failed(ctx context.Context, kind Kind, actor string, err error) {
	if errors.Is(err, errStopped) {
		s.o.Log.InfoContext(ctx, "backup stopped because Scanopsis is shutting down", "kind", kind, "actor", actor)
		return
	}
	s.o.Log.ErrorContext(ctx, "backup failed", "kind", kind, "actor", actor, "error", err)
	s.o.Audit.Record(context.WithoutCancel(ctx), actor, audit.ActionBackupFailed, map[string]any{"kind": kind, "error": message(err)})
}

// message is the text of err for people: the reason without the package's
// "take backup: " and "restore backup: " prefixes.
func message(err error) string {
	if errors.Is(err, context.Canceled) {
		return "Scanopsis stopped before the job finished; nothing was changed."
	}
	m := err.Error()
	for _, p := range []string{"take backup: ", "restore backup: "} {
		m = strings.TrimPrefix(m, p)
	}
	return m
}

// StartRestore starts restoring the backup called name in the background
// and returns at once. Unless skipSafety is set, a backup of the current
// data (kind before-restore) is taken first. keepHostSettings keeps this
// server's backup settings (see RestoreOptions). The data is replaced in
// one transaction; Restoring is true only while that runs.
func (s *Service) StartRestore(name string, actor string, skipSafety bool, keepHostSettings bool) (JobStatus, error) {
	if actor == "" {
		return JobStatus{}, errors.New("restore backup: no actor given")
	}
	if err := s.usable(); err != nil {
		return JobStatus{}, err
	}
	path, err := s.o.Dir.Path(name)
	if err != nil {
		return JobStatus{}, err
	}
	kind, err := s.check(path)
	if err != nil {
		return JobStatus{}, err
	}
	first := JobStatus{State: JobRestoring, Kind: kind, Name: name, Message: "Waiting for other maintenance jobs to finish."}
	if !skipSafety {
		first = JobStatus{State: JobBackingUp, Kind: KindBeforeRestore, Name: name, Message: "Waiting for other maintenance jobs to finish."}
	}
	st, err := s.begin(first)
	if err != nil {
		return JobStatus{}, err
	}
	go s.restoreJob(name, path, kind, actor, skipSafety, keepHostSettings)
	return st, nil
}

// check reads the manifest of the backup at path and refuses, before any
// job starts, a backup that belongs to another key or a newer version. The
// restore checks the whole file again, signature included.
func (s *Service) check(path string) (Kind, error) {
	f, err := os.Open(path) //nolint:gosec // path comes from Dir.Path
	if err != nil {
		return "", fmt.Errorf("restore backup: %w", err)
	}
	defer func() { _ = f.Close() }()
	m, err := ReadManifest(f)
	if err != nil {
		return "", fmt.Errorf("restore backup: %w", err)
	}
	if err := checkManifest(m, s.o.KeyFingerprint); err != nil {
		return "", fmt.Errorf("restore backup: %w", err)
	}
	return m.Kind, nil
}

func (s *Service) restoreJob(name, path string, kind Kind, actor string, skipSafety, keepHostSettings bool) {
	ctx := s.ctx
	var safety string
	err := s.guard(ctx, func() error {
		release, err := s.lock(ctx, true)
		if err != nil {
			return err
		}
		defer release()
		if !skipSafety {
			s.update(func(st *JobStatus) { st.Message = "Backing up the current data before the restore." })
			info, err := s.takeBackup(ctx, KindBeforeRestore, actor)
			if err = s.stopped(err); err != nil {
				s.failed(ctx, KindBeforeRestore, actor, err)
				if errors.Is(err, errStopped) {
					return err
				}
				return fmt.Errorf("the backup of the current data failed, so nothing was restored: %s", message(err))
			}
			safety = info.Name
		}
		s.progress.Store(nil)
		s.update(func(st *JobStatus) {
			st.State, st.Kind, st.Progress, st.Message = JobRestoring, kind, Progress{}, "Restoring the backup."
		})
		s.restoring.Store(true)
		defer s.restoring.Store(false)
		if err := s.restore(ctx, s.o.Pool, path, RestoreOptions{
			KeepHostSettings: keepHostSettings, Actor: actor, KeyFingerprint: s.o.KeyFingerprint,
			SigningKey: s.o.Dumper.SigningKey, Progress: s.setProgress,
		}); err != nil {
			return err
		}
		s.o.Log.InfoContext(ctx, "backup restored", "name", name, "actor", actor, "backup_before", safety)
		if s.o.AfterRestore != nil {
			if err := s.o.AfterRestore(ctx); err != nil {
				s.o.Log.ErrorContext(ctx, "the backup was restored, but the server could not reload its state", "name", name, "error", err)
				return &afterError{err}
			}
		}
		return nil
	})
	err = s.stopped(err)
	if err != nil {
		var after *afterError
		if !errors.As(err, &after) {
			s.o.Log.ErrorContext(ctx, "restore failed", "name", name, "actor", actor, "error", err)
			s.o.Audit.Record(context.WithoutCancel(ctx), actor, audit.ActionBackupRestoreFailed, map[string]any{"name": name, "error": message(err)})
		}
	}
	s.finish(func(st *JobStatus) {
		st.Kind, st.Name = kind, name
		if err != nil {
			st.State, st.Message = JobFailed, message(err)
			return
		}
		st.State, st.Message = JobDone, "Restore finished."
		if safety != "" {
			st.Message += " The data before the restore was saved as " + safety + "."
		}
	})
}

// afterError is a failure after the data was restored.
type afterError struct{ err error }

func (e *afterError) Error() string {
	return fmt.Sprintf("the backup was restored, but the server could not reload its settings (%v); restart Scanopsis", e.err)
}

// lock takes the job's advisory lock, and for a restore the locks of the
// other maintenance jobs too, on a database connection of its own outside
// the pool. The returned function releases them all by closing that
// connection. A lock of another job is waited for up to lockWait.
func (s *Service) lock(ctx context.Context, exclusive bool) (func(), error) {
	conn, err := pgx.ConnectConfig(ctx, s.o.Pool.Config().ConnConfig.Copy())
	if err != nil {
		return nil, fmt.Errorf("connect to the database: %w", err)
	}
	release := func() {
		// Closing the connection drops its locks, also when the database
		// cannot be reached in time.
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
		defer cancel()
		_ = conn.Close(cctx)
	}
	var ok bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", AdvisoryLockKey).Scan(&ok); err != nil {
		release()
		return nil, fmt.Errorf("take the backup lock: %w", err)
	}
	if !ok {
		release()
		return nil, errOtherInstance
	}
	if !exclusive {
		return release, nil
	}
	deadline := time.Now().Add(s.lockWait)
	for _, key := range s.o.ExclusiveLocks {
		// lock_timeout bounds the wait of pg_advisory_lock; the pool's
		// statement timeout must not cut it shorter.
		left := max(time.Until(deadline).Milliseconds(), 1)
		for _, q := range []string{"SET statement_timeout = 0", fmt.Sprintf("SET lock_timeout = %d", left)} {
			if _, err := conn.Exec(ctx, q); err != nil {
				release()
				return nil, fmt.Errorf("wait for other maintenance jobs: %w", err)
			}
		}
		if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
			release()
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "55P03" { // lock_not_available
				return nil, ErrMaintenance
			}
			return nil, fmt.Errorf("wait for other maintenance jobs: %w", err)
		}
	}
	return release, nil
}

// wait blocks until no job runs or ctx ends.
func (s *Service) wait(ctx context.Context) error {
	s.mu.Lock()
	idle := s.idle
	s.mu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// plan is what the schedule depends on.
type plan struct {
	enabled bool
	times   string
	zone    string
}

func planOf(snap settings.Snapshot) plan {
	return plan{enabled: snap.BackupScheduleEnabled, times: strings.Join(snap.BackupScheduleTimes, ","), zone: snap.BackupTimeZone}
}

// next returns the first run of p after now, or the zero time when the
// schedule is off.
func (s *Service) next(p plan, now time.Time) time.Time {
	if !p.enabled {
		return time.Time{}
	}
	loc, err := time.LoadLocation(p.zone)
	if err != nil { // the settings validate the zone, so this is not expected
		s.o.Log.Warn("unknown backup time zone, using UTC", "zone", p.zone, "error", err)
		loc = time.UTC
	}
	return NextRun(now, strings.Split(p.times, ","), loc)
}

// Run runs the backup schedule until ctx ends. The end of ctx also cancels
// a running job, scheduled or not, and Run returns once it has stopped
// (Close); a job stopped that way is not recorded as failed. Run follows the
// settings: a change of the schedule takes effect at once. A time missed
// while the server was down is not caught up. A time that comes while
// another job runs waits for it; times that pass during a long backup lead
// to one more backup, not to one per time, unless the schedule changed
// meanwhile.
func (s *Service) Run(ctx context.Context) {
	defer s.Close()
	// The end of ctx also stops a job that is running, scheduled or not.
	defer context.AfterFunc(ctx, s.stop)()
	changed := make(chan struct{}, 1)
	s.o.Settings.OnChange(func(settings.Snapshot) {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	cur := planOf(s.o.Settings.Current())
	next := s.next(cur, s.now())
	for {
		if ctx.Err() != nil {
			return
		}
		if !next.IsZero() && !s.now().Before(next) {
			s.scheduled(ctx)
			// A change during the run applies before any catch-up run, so
			// a schedule turned off meanwhile stays off.
			select {
			case <-changed:
				if p := planOf(s.o.Settings.Current()); p != cur {
					cur = p
					next = s.next(cur, s.now())
					continue
				}
			default:
			}
			after := s.next(cur, next)
			if now := s.now(); !after.IsZero() && !after.After(now) {
				after = now
			}
			next = after
			continue
		}
		var fire <-chan time.Time
		var t timer
		if !next.IsZero() {
			t = s.newTimer(min(next.Sub(s.now()), maxWait))
			fire = t.C()
		}
		select {
		case <-ctx.Done():
		case <-changed:
			if p := planOf(s.o.Settings.Current()); p != cur {
				cur = p
				next = s.next(cur, s.now())
			}
		case <-fire:
		}
		if t != nil {
			t.Stop()
		}
	}
}

// scheduled takes one scheduled backup, waiting for a running job first,
// then deletes expired scheduled backups and records the outcome.
func (s *Service) scheduled(ctx context.Context) {
	at := s.now().UTC()
	if err := s.usable(); err != nil {
		s.failed(ctx, KindScheduled, ScheduleActor, err)
		s.recordSchedule(at, err)
		return
	}
	for {
		if err := s.wait(ctx); err != nil {
			return
		}
		_, err := s.begin(JobStatus{State: JobBackingUp, Kind: KindScheduled, Message: "Taking a scheduled backup."})
		if err == nil {
			break
		}
		if !errors.Is(err, ErrBusy) {
			return
		}
	}
	_, err := s.backupJob(KindScheduled, ScheduleActor, s.expire)
	if errors.Is(err, errStopped) {
		return // shutting down: not a failure of the schedule
	}
	s.recordSchedule(at, err)
}

// expire deletes the scheduled backups older than the keep-days setting.
// It runs only after a successful scheduled backup, so failing backups
// never thin out the ones that are left, and inside that backup's job, so
// no restore can start from a file it deletes.
func (s *Service) expire(ctx context.Context) {
	list, err := s.o.Dir.List()
	if err != nil {
		s.o.Log.WarnContext(ctx, "could not list backups to delete expired ones", "error", err)
		return
	}
	expired := Expired(list, s.now(), s.o.Settings.Current().BackupKeepDays)
	if s.expiring != nil {
		s.expiring(expired)
	}
	for _, name := range expired {
		if err := s.o.Dir.Delete(name); err != nil {
			s.o.Log.WarnContext(ctx, "could not delete an expired backup", "name", name, "error", err)
			continue
		}
		s.o.Log.InfoContext(ctx, "expired backup deleted", "name", name)
	}
}

// recordSchedule keeps the outcome of a scheduled backup in memory and in
// the folder.
func (s *Service) recordSchedule(at time.Time, err error) {
	st := ScheduleStatus{LastRunAt: &at, OK: err == nil}
	if err != nil {
		st.Error = message(err)
	}
	s.mu.Lock()
	s.sched = st
	s.mu.Unlock()
	if s.o.Dir == nil {
		return
	}
	if err := writeFileAtomic(filepath.Join(s.o.Dir.Root(), statusFile), st); err != nil {
		s.o.Log.Warn("could not save the backup schedule status", "error", err)
	}
}

// writeFileAtomic writes v as JSON to path through a temporary file, so a
// reader never sees half a file.
func writeFileAtomic(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
