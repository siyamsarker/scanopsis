// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/siyamsarker/scanopsis/internal/store"
)

func TestMessage(t *testing.T) {
	d := &Dir{path: "/var/lib/scanopsis/backups"}
	partialFile := "/var/lib/scanopsis/backups/scanopsis-backup-20261123T141800Z-scheduled-126gsh.tar.gz.partial"
	denied := &os.PathError{Op: "open", Path: partialFile, Err: syscall.EACCES}
	full := &os.PathError{Op: "write", Path: partialFile, Err: syscall.ENOSPC}
	for err, want := range map[error]string{
		d.folderError(denied): "Scanopsis cannot write to the backup folder. Make it writable for the user the server runs as (uid 65532 in the container image), and check that the disk has free space and is not read-only.",
		d.folderError(full):   "The backup folder is full. Free some space, then try again.",
		d.writeError(denied):  "Scanopsis cannot write to the backup folder. Make it writable for the user the server runs as (uid 65532 in the container image), and check that the disk has free space and is not read-only.",
		errors.New("take backup: copy table findings: ERROR: canceling statement due to conflict with recovery (SQLSTATE 40001)"): "Something unexpected stopped the job. The server log has the details.",
		fmt.Errorf("restore backup: %w", corruptf("the file ends too early")):                                                     "The file is damaged: the file ends too early.",
		fmt.Errorf("take backup: copy: %w", context.Canceled):                                                                     "Scanopsis stopped before the job finished; nothing was changed.",
		ErrMaintenance: "Another maintenance job is running; try again in a few minutes.",
		fmt.Errorf("take backup: %w", &spaceError{need: 2 << 30, have: 1 << 30}): "Not enough free space for a backup: a backup needs about 2.0 GiB and the backup folder has 1.0 GiB free.",
		fmt.Errorf("restore backup: %w", &store.BlockedError{Msg: "table audit_log belongs to role other, not to scanopsis: the database user in secrets/database_url must own all Scanopsis tables"}):                                                                                            "Table audit_log belongs to role other, not to scanopsis: the database user in secrets/database_url must own all Scanopsis tables.",
		fmt.Errorf("restore backup: %w", &store.BlockedError{Msg: "another database object depends on a Scanopsis table (view v depends on table services); remove it or restore into a database used only by Scanopsis", Err: errors.New("ERROR: cannot drop table services (SQLSTATE 2BP01)")}): "Another database object depends on a Scanopsis table (view v depends on table services); remove it or restore into a database used only by Scanopsis.",
	} {
		got := message(err)
		if got != want {
			t.Errorf("message(%v)\n got %q\nwant %q", err, got, want)
		}
		if strings.Contains(got, ".partial") || strings.Contains(got, "permission denied") {
			t.Errorf("message(%v) = %q names a temporary file or a system error", err, got)
		}
	}
}

func TestFolderErrorKeepsTheDetails(t *testing.T) {
	d := &Dir{path: "/srv/backups"}
	err := d.folderError(&os.PathError{Op: "open", Path: "/srv/backups/x.partial", Err: syscall.EACCES})
	if !errors.Is(err, ErrFolder) || !errors.Is(err, syscall.EACCES) {
		t.Errorf("err = %v; want ErrFolder and the cause", err)
	}
	for _, s := range []string{"/srv/backups", "x.partial", "permission denied"} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("the log text %q does not have %q", err.Error(), s)
		}
	}
}

func TestFailedBackupSaysWhyInASentence(t *testing.T) {
	dir, err := OpenDir(t.TempDir(), "fp")
	if err != nil {
		t.Fatal(err)
	}
	rec := &recAudit{}
	s := NewService(ServiceOptions{Dir: dir, Log: discard, Audit: rec})
	t.Cleanup(s.Close)
	s.dump = func(context.Context, Kind, string, func(Progress)) (Info, error) {
		return Info{}, dir.folderError(&os.PathError{Op: "open", Path: dir.Root() + "/a.partial", Err: syscall.EACCES})
	}
	s.lockFn = func(context.Context, bool) (func(), error) { return func() {}, nil }
	if _, err := s.StartBackup(KindManual, "local:alice"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for s.Status().State != JobFailed && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	want := "Scanopsis cannot write to the backup folder. Make it writable for the user the server runs as (uid 65532 in the container image), and check that the disk has free space and is not read-only."
	if got := s.Status().Message; got != want {
		t.Errorf("job message = %q, want %q", got, want)
	}
	if ev := rec.of("backup.failed"); len(ev) != 1 || ev[0].detail["error"] != want {
		t.Errorf("audit = %+v", ev)
	}
}
