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
	"slices"
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
	"github.com/siyamsarker/scanopsis/internal/store"
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

// restartMessage is the job message after a restore that committed, or
// may have committed, but could not finish.
const restartMessage = "The backup was restored, but Scanopsis must restart to finish. Restart it, for example with docker compose restart scanopsis."

// uncertainRestartMessage replaces restartMessage when PostgreSQL did not
// confirm the commit (ErrCommitUncertain): the restore may not have been
// saved, but the server must restart all the same.
const uncertainRestartMessage = "The restore may have finished, but Scanopsis must restart to finish. Restart it, for example with docker compose restart scanopsis."

// dbMessage says that the database could not be reached.
const dbMessage = "Scanopsis could not connect to the database. The server log has the details."

// closeTimeout bounds the closing of the lock connection.
const closeTimeout = 5 * time.Second

// maxWait is the longest the scheduler sleeps before it looks at the clock
// again, so a clock that is set or jumps is noticed within a minute.
const maxWait = time.Minute

var (
	// ErrBusy means a backup or restore job is already running.
	ErrBusy = errors.New("a backup or restore is already running")
	// ErrInUse means the backup is the one the running restore reads.
	ErrInUse = errors.New("the backup is being restored; wait until the restore has finished")
	// ErrMaintenance means another maintenance job held its lock for longer
	// than a restore waits.
	ErrMaintenance = errors.New("another maintenance job is running; try again in a few minutes")
	// errOtherInstance means another instance runs a backup or restore.
	errOtherInstance = errors.New("another Scanopsis instance is running a backup or restore on this database; try again when it has finished")
	// errClosed means the server is shutting down.
	errClosed = errors.New("the server is shutting down")
	// errInternal replaces a panic of a job.
	errInternal = errors.New("an unexpected error stopped the job; see the server log for details")
	// errNeedRestart means a restore committed but could not finish.
	errNeedRestart = errors.New("a restore is waiting for Scanopsis to restart; restart it first")
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

// JobType tells a backup job from a restore job.
type JobType string

// Job types.
const (
	JobTypeBackup  JobType = "backup"
	JobTypeRestore JobType = "restore"
)

// JobStatus describes the current or last job. For a backup, Kind and Name
// describe the new file once it is written; for a restore, they describe
// the backup being restored (while the backup before the restore runs,
// Kind is KindBeforeRestore). Message says what happens or went wrong in a
// sentence for people.
type JobStatus struct {
	State      JobState   `json:"state"`
	Type       JobType    `json:"type,omitempty"`
	Kind       Kind       `json:"kind,omitempty"`
	Name       string     `json:"name,omitempty"`
	Progress   Progress   `json:"progress"`
	Percent    int        `json:"percent"`
	Message    string     `json:"message,omitempty"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

// ScheduleStatus is the outcome of the last scheduled backup. It is kept in
// the backup folder, so it survives a restart and a restore. Dismissed is
// set when an administrator dismissed the failure; the next scheduled run
// clears it. Enabled is not kept: it tells whether the schedule is on now.
type ScheduleStatus struct {
	LastRunAt *time.Time `json:"last_run_at"`
	OK        bool       `json:"ok"`
	Error     string     `json:"error"`
	Dismissed bool       `json:"dismissed"`
	Enabled   bool       `json:"enabled"`
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
	// lockFn takes the locks of a job (see lock); tests replace it.
	lockFn func(ctx context.Context, exclusive bool) (func(), error)
	// verifyFile checks the whole backup file at path; tests replace it.
	verifyFile func(ctx context.Context, path string) error
	// onProgress, if not nil, is called after every progress report; tests
	// set it.
	onProgress func()
	// expiring, if not nil, is called with the names expiry is about to
	// delete.
	expiring func(names []string)

	ctx    context.Context
	cancel context.CancelFunc
	jobs   sync.WaitGroup
	// checks runs one background check (an upload, a preview) at a time;
	// checking counts the checks that were started or wait for their turn.
	checks   checkSlot
	checking sync.WaitGroup

	restoring atomic.Bool
	// needRestart is set when a restore committed but could not finish;
	// restoring then stays true and no job starts until a restart.
	needRestart atomic.Bool
	progress    atomic.Pointer[Progress]

	// saveMu orders the writes of the schedule status file, so the file
	// always ends with the latest status.
	saveMu sync.Mutex

	// listedChecks holds the names of the files checked for the list,
	// waiting or running.
	listedMu     sync.Mutex
	listedChecks map[string]bool

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
	s.lockFn = s.lock
	s.verifyFile = s.verifyWhole
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

// Close cancels the running job and the background checks, if any, and
// waits for them to end. A cancelled backup leaves no file; a restore
// cancelled before its commit changes nothing (see StartRestore).
func (s *Service) Close() {
	s.stop()
	s.jobs.Wait()
	s.checking.Wait()
}

// checkPriority orders the background checks that wait for their turn.
type checkPriority int

const (
	// checkUpload checks an upload: someone waits to use the file.
	checkUpload checkPriority = iota
	// checkPreview computes the preview of a backup in the folder, which
	// only informs a decision; it waits while an upload check waits.
	checkPreview
	// checkListed checks a backup that appeared in the folder without
	// being written or checked here, so the list can mark a damaged one;
	// nobody waits for it.
	checkListed
	checkPriorities
)

// checkSlot lets one background check run at a time. When it frees up, the
// check waiting longest with the highest priority goes next. A check for
// the list, which nobody waits for, is stopped when an upload check or a
// preview starts to wait (see take).
type checkSlot struct {
	mu      sync.Mutex
	busy    bool
	waiting [checkPriorities][]chan struct{}
	// yield stops the running check when it is one that gives way, else
	// nil.
	yield context.CancelFunc
}

// take waits for the slot and reports whether it got it; false means ctx
// ended first. yield, if not nil, stops the check that takes the slot: it
// is called when a check of a higher priority than checkListed waits.
func (c *checkSlot) take(ctx context.Context, p checkPriority, yield context.CancelFunc) bool {
	c.mu.Lock()
	if !c.busy {
		c.busy, c.yield = true, yield
		c.mu.Unlock()
		return true
	}
	if p < checkListed && c.yield != nil {
		// Someone waits for this check; the list check is tried again later.
		c.yield()
		c.yield = nil
	}
	turn := make(chan struct{})
	c.waiting[p] = append(c.waiting[p], turn)
	c.mu.Unlock()
	select {
	case <-turn:
		c.mu.Lock()
		c.yield = yield
		c.mu.Unlock()
		return true
	case <-ctx.Done():
	}
	c.mu.Lock()
	for i, w := range c.waiting[p] {
		if w == turn {
			c.waiting[p] = slices.Delete(c.waiting[p], i, i+1)
			c.mu.Unlock()
			return false
		}
	}
	c.mu.Unlock()
	// The turn came as ctx ended: pass it on.
	c.give()
	return false
}

// give hands the slot to the next waiting check, or frees it.
func (c *checkSlot) give() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.yield = nil
	for p := range c.waiting {
		if len(c.waiting[p]) > 0 {
			turn := c.waiting[p][0]
			c.waiting[p] = c.waiting[p][1:]
			close(turn)
			return
		}
	}
	c.busy = false
}

// background runs fn in the background once no other check runs, so the
// server reads at most one backup at a time for a check; when the slot
// frees up, an upload check that waits goes before a preview (p). ctx ends
// the wait for that turn; fn is then still called, and is expected to see
// ctx ended and return at once. It refuses, with errClosed, once the
// service closes; Close waits for fn to return. A panic in fn is logged; fn
// should turn its own failures into an outcome.
func (s *Service) background(ctx context.Context, p checkPriority, fn func()) error {
	return s.backgroundYielding(ctx, p, nil, fn)
}

// backgroundYielding is background for a check that gives way: yield stops
// it, and ctx should end with it.
func (s *Service) backgroundYielding(ctx context.Context, p checkPriority, yield context.CancelFunc, fn func()) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx.Err() != nil {
		return errClosed
	}
	s.checking.Add(1)
	go func() {
		defer s.checking.Done()
		if s.checks.take(ctx, p, yield) {
			defer s.checks.give()
		}
		_ = s.guard(ctx, "a background check stopped with a panic", func() error { fn(); return nil })
	}()
	return nil
}

// baseContext is the context of the service's background work; Close ends it.
func (s *Service) baseContext() context.Context { return s.ctx }

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
	if st.State == JobDone {
		st.Percent = 100
	} else {
		st.Percent = st.Progress.percent()
	}
	return st
}

// Restoring reports whether a restore is replacing the data right now, or
// replaced it and waits for a restart. The server turns requests away while
// it is true.
func (s *Service) Restoring() bool { return s.restoring.Load() }

// The messages of RestoreStatus. They name no file, account or folder,
// since anyone may read them.
const (
	restoreBackingUpMessage = "Saving the current data before the restore."
	restoreRunningMessage   = "Restoring a backup."
	restoreDoneMessage      = "Restore finished. Sign in with an account from the backup."
	restoreFailedMessage    = "The restore failed and nothing was changed. Admins see the reason on the Backups page."
)

// RestoreStatus is the state of the current or last restore, as anyone may
// see it: no names, accounts or folders.
type RestoreStatus struct {
	State      JobState   `json:"state"`
	Percent    int        `json:"percent"`
	Message    string     `json:"message"`
	FinishedAt *time.Time `json:"finished_at"`
	// RestartRequired is true only when a restore replaced the data but
	// Scanopsis must restart to finish it (state failed); until then the
	// API stays in restore mode.
	RestartRequired bool `json:"restart_required"`
}

// RestoreStatus returns the state of the current or last restore. When the
// last job was a backup, or there was none, the state is idle.
func (s *Service) RestoreStatus() RestoreStatus {
	return publicRestore(s.Status(), s.needRestart.Load())
}

func publicRestore(st JobStatus, needRestart bool) RestoreStatus {
	if st.Type != JobTypeRestore {
		return RestoreStatus{State: JobIdle}
	}
	out := RestoreStatus{State: st.State, Percent: st.Percent, FinishedAt: st.FinishedAt}
	switch {
	case st.State == JobBackingUp:
		out.Message = restoreBackingUpMessage
	case st.State == JobRestoring:
		out.Message = restoreRunningMessage
	case st.State == JobDone:
		out.Message = restoreDoneMessage
	case needRestart && st.Message == uncertainRestartMessage:
		out.Message, out.RestartRequired = uncertainRestartMessage, true
	case needRestart:
		out.Message, out.RestartRequired = restartMessage, true
	default:
		out.Message = restoreFailedMessage
	}
	return out
}

// Delete deletes the backup called name. It refuses, with ErrInUse, the
// backup the running restore reads. A file still being written has a name
// of its own (it ends in .partial) and is never listed.
func (s *Service) Delete(name string) error {
	if s.o.Dir == nil {
		return s.usable()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running && s.status.Type == JobTypeRestore && s.status.Name == name {
		return ErrInUse
	}
	return s.o.Dir.Delete(name)
}

// CheckSpace refuses, with ErrNoSpace and the numbers, a backup that would
// not fit the folder now.
func (s *Service) CheckSpace(ctx context.Context) error {
	if err := s.usable(); err != nil {
		return err
	}
	return s.o.Dumper.CheckSpace(ctx)
}

// Schedule returns the outcome of the last scheduled backup, and whether
// the schedule is on.
func (s *Service) Schedule() ScheduleStatus {
	s.mu.Lock()
	st := s.sched
	s.mu.Unlock()
	if s.o.Settings != nil {
		st.Enabled = s.o.Settings.Current().BackupScheduleEnabled
	}
	return st
}

// DismissScheduleFailure marks the failure of the last scheduled backup as
// seen, in memory and in the folder, so the banner about it goes away. It
// reports false when the last scheduled backup did not fail or the failure
// was dismissed already. The next scheduled run replaces the status.
func (s *Service) DismissScheduleFailure() (ScheduleStatus, bool) {
	s.mu.Lock()
	if s.sched.LastRunAt == nil || s.sched.OK || s.sched.Dismissed {
		st := s.sched
		s.mu.Unlock()
		return st, false
	}
	s.sched.Dismissed = true
	st := s.sched
	s.mu.Unlock()
	s.saveSchedule()
	return st, true
}

// availableLocked reports why no job can start now, or nil. The caller
// holds mu.
func (s *Service) availableLocked() error {
	switch {
	case s.ctx.Err() != nil:
		return errClosed
	case s.needRestart.Load():
		return errNeedRestart
	case s.running:
		return ErrBusy
	}
	return nil
}

// begin claims the job slot. It returns ErrBusy when a job runs.
func (s *Service) begin(st JobStatus) (JobStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.availableLocked(); err != nil {
		return JobStatus{}, err
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

func (s *Service) setProgress(p Progress) {
	s.progress.Store(&p)
	if s.onProgress != nil {
		s.onProgress()
	}
}

// usable reports why no job can run, or nil.
func (s *Service) usable() error {
	if s.o.Dir == nil || s.dump == nil {
		return &publicError{
			msg: "The backup folder could not be used when Scanopsis started. The server log says why; fix it, then restart Scanopsis.",
			err: ErrFolder,
		}
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
	st, err := s.begin(JobStatus{State: JobBackingUp, Type: JobTypeBackup, Kind: kind, Message: "Taking a backup."})
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
	err := s.guard(ctx, "a backup job stopped with a panic", func() error {
		release, err := s.lockFn(ctx, false)
		if err != nil {
			return err
		}
		defer release()
		if info, err = s.takeBackup(ctx, kind, actor, s.setProgress); err != nil {
			return err
		}
		if then != nil {
			_ = s.guard(ctx, "deleting expired scheduled backups stopped with a panic", func() error { then(ctx); return nil })
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

// guard runs fn and turns a panic into errInternal, logged as what with its
// stack.
// The deferred calls of fn, which release the locks, run on the way.
func (s *Service) guard(ctx context.Context, what string, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			s.o.Log.ErrorContext(ctx, what, "panic", r, "stack", string(debug.Stack()))
			err = errInternal
		}
	}()
	return fn()
}

// stopped replaces err with errStopped when it is the cancellation that
// a closed service caused: a shutdown is not a failure of the job. Any
// other error, even during a shutdown, stays a failure.
func (s *Service) stopped(err error) error {
	if s.ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return errStopped
	}
	return err
}

// takeBackup writes a backup under the job lock and audits it. The caller
// records a failure.
func (s *Service) takeBackup(ctx context.Context, kind Kind, actor string, progress func(Progress)) (Info, error) {
	info, err := s.dump(ctx, kind, actor, progress)
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

// unexpectedMessage is the sentence for an error that has none for people;
// the server log has the error.
const unexpectedMessage = "Something unexpected stopped the job. The server log has the details."

// message is the reason of err in a sentence for people. Errors that carry
// one (publicError) give it; the errors of this package whose text is
// written for people give that text without the package's "take backup: ",
// "restore backup: " and "check backup: " prefixes. Any other error, such as
// one of the database or the file system, could name temporary files or
// internals, so it gives a sentence that points to the server log.
func message(err error) string {
	var pub *publicError
	var blocked *store.BlockedError
	switch {
	case errors.Is(err, context.Canceled):
		return "Scanopsis stopped before the job finished; nothing was changed."
	case errors.As(err, &pub):
		return pub.msg
	case errors.As(err, &blocked):
		// Something in the database the operator must change.
		return sentence(blocked.Msg)
	case errors.Is(err, ErrFolder):
		return "The backup folder cannot be used. The server log says why."
	case errors.Is(err, os.ErrNotExist):
		return "The backup file is no longer in the backup folder."
	}
	for _, known := range []error{
		ErrBusy, ErrInUse, ErrMaintenance, ErrNoSpace, ErrKeyMismatch, ErrNewerSchema,
		ErrCorrupt, ErrNotBackup, ErrFormat, ErrSignature, ErrCommitUncertain,
		errOtherInstance, errClosed, errInternal, errNeedRestart,
	} {
		if errors.Is(err, known) {
			m := err.Error()
			for _, p := range []string{"take backup: ", "restore backup: ", "check backup: "} {
				m = strings.TrimPrefix(m, p)
			}
			return sentence(m)
		}
	}
	return unexpectedMessage
}

// CheckRestore tells whether a restore of the backup called name can start
// now, without starting it: it refuses with the error StartRestore would
// give when a job runs, the server waits for a restart or shuts down, or the
// manifest of the backup belongs to another key or a newer version. A
// request checks this before it spends anything, such as a two-factor code.
func (s *Service) CheckRestore(name string) error {
	if err := s.usable(); err != nil {
		return err
	}
	path, err := s.o.Dir.Path(name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	err = s.availableLocked()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if fi, err := os.Stat(path); err == nil {
		if problem := s.o.Dir.problemOf(name, fi); problem != "" {
			return &publicError{msg: problem, err: fmt.Errorf("%w: %s", ErrCorrupt, problem)}
		}
	}
	_, err = s.check(path)
	return err
}

// verifyWhole checks the whole backup file at path, as Restore does before
// it writes anything: every checksum and the signature of this key.
func (s *Service) verifyWhole(ctx context.Context, path string) error {
	f, err := os.Open(path) //nolint:gosec // path comes from Dir.Path
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	_, _, err = VerifyLimited(ctxReader{ctx, f}, s.o.Dumper.SigningKey, nil, expansionLimit(fi.Size()))
	return err
}

// checkListed checks, in the background and after every other check, the
// whole of each listed backup that could be restored here but was neither
// written nor checked here in its current form: a file copied into the
// folder by hand, or one that changed. A damaged one is then listed as such
// (see Dir.markChecked). A check that cannot tell, because the file went
// away or the server stops, is tried again at a later listing.
func (s *Service) checkListed(list []Info) {
	if s.o.Dir == nil || s.o.Dumper == nil {
		return
	}
	for _, info := range list {
		if !info.checkable() {
			continue
		}
		name := info.Name
		s.listedMu.Lock()
		if s.listedChecks[name] {
			s.listedMu.Unlock()
			continue
		}
		if s.listedChecks == nil {
			s.listedChecks = map[string]bool{}
		}
		s.listedChecks[name] = true
		s.listedMu.Unlock()
		done := func() {
			s.listedMu.Lock()
			delete(s.listedChecks, name)
			s.listedMu.Unlock()
		}
		// The check gives way to an upload check or a preview that waits,
		// and is tried again at a later listing.
		ctx, stop := context.WithCancel(s.ctx)
		err := s.backgroundYielding(ctx, checkListed, stop, func() {
			defer stop()
			defer done()
			if ctx.Err() != nil {
				return
			}
			s.recordCheck(ctx, name, s.verifyFile)
		})
		if err != nil {
			stop()
			done()
		}
	}
}

// recordCheck runs check on the backup called name and keeps the outcome
// for the list, when it is one that lasts as long as the file stays the
// same.
func (s *Service) recordCheck(ctx context.Context, name string, check func(context.Context, string) error) {
	path, err := s.o.Dir.Path(name)
	if err != nil {
		return
	}
	fi, err := os.Stat(path)
	if err != nil {
		return
	}
	err = check(ctx, path)
	s.markOutcome(ctx, name, fi, err)
}

// markOutcome keeps the outcome err of a check of the whole backup called
// name, as the file fi was before the check, when it lasts: a pass, or a
// file whose content is damaged, not a backup or not signed with this key.
// A read that failed, such as an error of the disk, is checked again later.
func (s *Service) markOutcome(ctx context.Context, name string, fi os.FileInfo, err error) {
	switch {
	case err == nil:
		s.o.Dir.markChecked(name, fi, "")
	case lasting(err) && !errors.Is(err, os.ErrNotExist):
		s.o.Log.WarnContext(ctx, "a backup in the folder is damaged", "name", name, "error", err)
		s.o.Dir.markChecked(name, fi, message(err))
	}
}

// StartRestore starts restoring the backup called name in the background
// and returns at once. The restore checks the whole file first; then,
// unless skipSafety is set, a backup of the current data (kind
// before-restore) is taken. keepHostSettings keeps this server's backup
// settings (see RestoreOptions). The restore then replaces the data in one
// transaction; Restoring is true
// from the start of that transaction until AfterRestore has finished. When
// the transaction committed, or its commit failed so that it may have
// committed (ErrCommitUncertain), and the restore could not finish, the
// server may hold state from before the restore: Restoring stays true, no
// new job starts, the job fails with a message asking for a restart, and
// backup.restore_incomplete is audited.
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
	st, err := s.begin(JobStatus{State: JobRestoring, Type: JobTypeRestore, Kind: kind, Name: name, Message: "Waiting for other maintenance jobs to finish."})
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
	// safetyTaken tells the progress of the load, reported on a goroutine
	// of the database driver, that the backup of the current data ran.
	var safetyTaken atomic.Bool
	before, _ := os.Stat(path)
	// committed is set once the restore transaction has committed, or may
	// have: from then on the database may hold the backup's data, whatever
	// happens next.
	var committed bool
	err := s.guard(ctx, "a restore job stopped with a panic", func() error {
		release, err := s.lockFn(ctx, true)
		if err != nil {
			return err
		}
		defer release()
		s.update(func(st *JobStatus) { st.Message = "Checking the backup before the restore." })
		// Restore mode ends here only when the data was not replaced, or
		// when the server has reloaded everything it keeps in memory.
		defer func() {
			if !committed {
				s.restoring.Store(false)
			}
		}()
		err = s.restore(ctx, s.o.Pool, path, RestoreOptions{
			KeepHostSettings: keepHostSettings, Actor: actor, KeyFingerprint: s.o.KeyFingerprint,
			SigningKey: s.o.Dumper.SigningKey,
			Progress:   func(p Progress) { s.setProgress(restoreJobWork(p, safetyTaken.Load())) },
			// The whole file was checked first: the backup of the current
			// data comes right before the replacement, so that little can
			// change in between, and a damaged file costs no backup.
			Verified: func(ctx context.Context) error {
				if skipSafety {
					return nil
				}
				s.update(func(st *JobStatus) {
					st.State, st.Kind, st.Message = JobBackingUp, KindBeforeRestore, "Backing up the current data before the restore."
				})
				// The bar goes on from the check: one scale for the job.
				info, err := s.takeBackup(ctx, KindBeforeRestore, actor, func(p Progress) { s.setProgress(safetyWork(p)) })
				if err = s.stopped(err); err != nil {
					s.failed(ctx, KindBeforeRestore, actor, err)
					if errors.Is(err, errStopped) {
						return err
					}
					return &publicError{msg: "The backup of the current data failed, so nothing was restored. " + message(err), err: err}
				}
				safety = info.Name
				safetyTaken.Store(true)
				s.setProgress(safetyWork(Progress{work: 1, workTotal: 1}))
				s.update(func(st *JobStatus) {
					st.State, st.Kind, st.Message = JobRestoring, kind, "Restoring the backup."
				})
				return nil
			},
			// Restore mode starts with the transaction that replaces the
			// data.
			BeforeReplace: func() {
				s.update(func(st *JobStatus) { st.Message = "Restoring the backup." })
				s.restoring.Store(true)
			},
		})
		if errors.Is(err, ErrCommitUncertain) {
			committed = true
		}
		if err != nil {
			return err
		}
		committed = true
		s.o.Log.InfoContext(ctx, "backup restored", "name", name, "actor", actor, "backup_before", safety)
		if s.o.AfterRestore != nil {
			if err := s.o.AfterRestore(ctx); err != nil {
				return err
			}
		}
		s.restoring.Store(false)
		return nil
	})
	if committed && err != nil {
		s.incomplete(ctx, name, kind, actor, err)
		return
	}
	err = s.stopped(err)
	if err != nil && before != nil && lasting(err) {
		// The list then shows the file as damaged.
		s.markOutcome(ctx, name, before, err)
	}
	if err != nil {
		s.o.Log.ErrorContext(ctx, "restore failed", "name", name, "actor", actor, "error", err)
		s.o.Audit.Record(context.WithoutCancel(ctx), actor, audit.ActionBackupRestoreFailed, map[string]any{"name": name, "error": message(err)})
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

// incomplete ends a restore whose transaction committed but whose
// follow-up (AfterRestore) failed, panicked or was cancelled, or whose
// commit failed in a way that leaves open whether it committed. The server
// may hold settings and caches from before the restore, so it must not
// serve again: restore mode stays on and no new job starts until the
// process is restarted, which reads everything from the restored
// database.
func (s *Service) incomplete(ctx context.Context, name string, kind Kind, actor string, err error) {
	s.needRestart.Store(true)
	msg := restartMessage
	if errors.Is(err, ErrCommitUncertain) {
		msg = uncertainRestartMessage
	}
	s.o.Log.ErrorContext(ctx, "the restore must be finished by a restart of Scanopsis", "name", name, "actor", actor, "error", err)
	s.o.Audit.Record(context.WithoutCancel(ctx), actor, audit.ActionBackupRestoreIncomplete, map[string]any{"name": name, "error": err.Error()})
	s.finish(func(st *JobStatus) {
		st.Kind, st.Name = kind, name
		st.State, st.Message = JobFailed, msg
	})
}

// lock takes the job's advisory lock, and for a restore the locks of the
// other maintenance jobs too, on a database connection of its own outside
// the pool. The returned function releases them all by closing that
// connection. A lock of another job is waited for up to lockWait.
func (s *Service) lock(ctx context.Context, exclusive bool) (func(), error) {
	conn, err := pgx.ConnectConfig(ctx, s.o.Pool.Config().ConnConfig.Copy())
	if err != nil {
		return nil, &publicError{msg: dbMessage, err: fmt.Errorf("connect to the database: %w", err)}
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
// (Close). A backup stopped that way leaves no file and is neither audited
// as backup.failed nor recorded as a failed scheduled run. A restore stopped
// before its transaction committed is rolled back and audited as
// backup.restore_failed; one stopped after the commit is audited as
// backup.restore_incomplete and keeps restore mode on until a restart (see
// StartRestore). Run follows the
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
		_, err := s.begin(JobStatus{State: JobBackingUp, Type: JobTypeBackup, Kind: KindScheduled, Message: "Taking a scheduled backup."})
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
	s.saveSchedule()
}

// saveSchedule writes the current schedule status to the folder.
func (s *Service) saveSchedule() {
	if s.o.Dir == nil {
		return
	}
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	s.mu.Lock()
	st := s.sched
	s.mu.Unlock()
	st.Enabled = false // not kept: it comes from the settings
	if err := writeFileAtomic(filepath.Join(s.o.Dir.Root(), statusFile), st); err != nil {
		s.o.Log.Warn("could not save the backup schedule status", "error", err)
	}
}

// writeFileAtomic writes v as JSON to path through a temporary file, so a
// reader never sees half a file.
// writeTemp writes the temporary file of writeFileAtomic; tests replace it.
var writeTemp = os.WriteFile

func writeFileAtomic(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := writeTemp(tmp, b, 0o600); err != nil {
		// A write that failed half way, as on a full disk, leaves nothing.
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
