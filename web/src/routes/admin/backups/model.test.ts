// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import type { BackupInfo, JobStatus, RestorePreview, RestoreStatus } from '../../../api/backups';
import {
  blockedReason,
  canRestore,
  folderProblem,
  scheduleFailure,
  isRunning,
  jobDetail,
  keepDaysProblem,
  kindLabel,
  madeBy,
  newTimeProblem,
  overlayView,
  restoreBlocker,
  retentionWarning,
  scheduleAlertShown,
  scheduleProblems,
  timeZoneOptions,
} from './model';

const info: BackupInfo = {
  name: 'scanopsis-backup-20261123T020000Z-scheduled-abc123.tar.gz',
  kind: 'scheduled',
  size: 1024,
  created_at: '2026-11-23T02:00:00Z',
  created_by: 'scheduler',
  app_version: '0.4.0',
  schema: 12,
  key_matches: true,
  readable: true,
  newer_version: false,
  uploaded_at: null,
  original_name: '',
};

const preview: RestorePreview = {
  info,
  schema_ok: true,
  key_matches: true,
  scans_removed_by_retention: 0,
  oldest_scan_at: null,
  retention_days: 90,
};

const job = (overrides: Partial<JobStatus>): JobStatus => ({
  state: 'idle',
  progress: { table: '', tables_done: 0, tables_total: 0, bytes: 0 },
  percent: 0,
  started_at: null,
  finished_at: null,
  ...overrides,
});

describe('backup labels', () => {
  it('names each kind', () => {
    expect(kindLabel('manual')).toBe('Manual');
    expect(kindLabel('scheduled')).toBe('Scheduled');
    expect(kindLabel('uploaded')).toBe('Uploaded');
    expect(kindLabel('before-restore')).toBe('Before restore');
  });

  it('says who made a backup in words', () => {
    expect(madeBy('scheduler')).toBe('Schedule');
    expect(madeBy('setup')).toBe('Setup screen');
    expect(madeBy('local:alice')).toBe('alice');
    expect(madeBy('gitlab:bob')).toBe('bob');
    expect(madeBy('')).toBe('–');
  });
});

describe('jobs', () => {
  it('knows when a job runs', () => {
    expect(isRunning(job({ state: 'backing_up' }))).toBe(true);
    expect(isRunning(job({ state: 'restoring' }))).toBe(true);
    expect(isRunning(job({ state: 'done' }))).toBe(false);
    expect(isRunning(undefined)).toBe(false);
  });

  it('describes progress: current table, tables done of total and size so far', () => {
    expect(
      jobDetail(
        job({
          state: 'backing_up',
          progress: { table: 'scans', tables_done: 3, tables_total: 12, bytes: 3 * 1024 * 1024 },
        }),
      ),
    ).toBe('Copying scans, 3 of 12 tables, 3.0 MiB so far');
    expect(jobDetail(job({ state: 'backing_up' }))).toBe('Starting');
  });
});

describe('schedule validation', () => {
  it('accepts 24-hour times only', () => {
    expect(newTimeProblem('02:00', [])).toBeUndefined();
    expect(newTimeProblem('23:59', [])).toBeUndefined();
    expect(newTimeProblem('24:00', [])).toBe('Use a 24-hour time such as 02:00.');
    expect(newTimeProblem('2:00', [])).toBe('Use a 24-hour time such as 02:00.');
    expect(newTimeProblem('', [])).toBe('Use a 24-hour time such as 02:00.');
  });

  it('refuses a time twice and more than 24 times', () => {
    expect(newTimeProblem('02:00', ['02:00'])).toBe('02:00 is already in the list.');
    const full = Array.from({ length: 24 }, (_, h) => `${String(h).padStart(2, '0')}:30`);
    expect(newTimeProblem('02:00', full)).toBe('A day can have at most 24 backup times.');
  });

  it('keeps scheduled backups from 1 to 365 days', () => {
    expect(keepDaysProblem('7')).toBeUndefined();
    expect(keepDaysProblem('365')).toBeUndefined();
    for (const bad of ['0', '366', '1.5', '', 'seven']) {
      expect(keepDaysProblem(bad)).toBe('Use a whole number from 1 to 365.');
    }
  });

  it('needs at least one time', () => {
    expect(scheduleProblems({ times: [], keepDays: '7' })).toEqual({
      times: 'Add at least one time.',
    });
    expect(scheduleProblems({ times: ['02:00'], keepDays: '0' })).toEqual({
      keepDays: 'Use a whole number from 1 to 365.',
    });
    expect(scheduleProblems({ times: ['02:00'], keepDays: '7' })).toEqual({});
  });

  it('lists time zones with UTC first, the current one and the browser zone marked', () => {
    const options = timeZoneOptions('Etc/Unknown', 'Europe/Berlin');
    expect(options[0]).toMatchObject({ value: 'UTC' });
    expect(options.find((o) => o.value === 'Europe/Berlin')).toMatchObject({
      hint: 'Your time zone',
    });
    expect(options.some((o) => o.value === 'Etc/Unknown')).toBe(true);
    expect(new Set(options.map((o) => o.value)).size).toBe(options.length);
  });
});

describe('restore', () => {
  it('warns about scans the clean-up removes only when there are some', () => {
    expect(retentionWarning(preview)).toBeNull();
    expect(retentionWarning({ ...preview, scans_removed_by_retention: 1204 })).toBe(
      'The next daily clean-up removes up to 1,204 scans older than 90 days from this backup. To keep them, raise the retention period on the Settings page right after the restore.',
    );
    expect(retentionWarning({ ...preview, scans_removed_by_retention: 1 })).toContain(
      'up to 1 scan older',
    );
  });

  it('never states removed scans for a backup made with another key', () => {
    expect(
      retentionWarning({ ...preview, key_matches: false, scans_removed_by_retention: 5 }),
    ).toBeNull();
  });

  it('says why a backup cannot be restored', () => {
    expect(restoreBlocker(info, preview)).toBeNull();
    expect(restoreBlocker({ ...info, key_matches: false })).toMatch(/another secret key/);
    const mismatch = restoreBlocker(info, { ...preview, key_matches: false });
    expect(mismatch).toBe(
      'This backup was made with another secret key and cannot be restored into this installation. To use it, set up a new installation with that secret key and restore the backup on its setup screen. Never change the secret key of an installation in use.',
    );
    expect(mismatch).not.toMatch(/SCANOPSIS_SECRET_KEY|secrets\/secret_key/);
    expect(restoreBlocker({ ...info, readable: false })).toMatch(/cannot be read/);
    expect(restoreBlocker(info, { ...preview, schema_ok: false })).toMatch(/newer version/);
    expect(restoreBlocker({ ...info, readable: false, newer_version: true })).toBe(
      'This backup was made by a newer version of Scanopsis. Upgrade Scanopsis to that version or later, then restore it.',
    );
    expect(restoreBlocker({ ...info, schema: 99, newer_version: true })).toMatch(
      /newer version of Scanopsis \(0\.4\.0\)/,
    );
  });

  it('tells a newer backup from an unreadable one in the list', () => {
    expect(blockedReason(info)).toBeNull();
    expect(blockedReason({ ...info, readable: false, newer_version: true })).toBe('newer');
    expect(blockedReason({ ...info, newer_version: true })).toBe('newer');
    expect(blockedReason({ ...info, readable: false })).toBe('unreadable');
    expect(blockedReason({ ...info, key_matches: false })).toBe('key');
  });

  it('enables the restore only for RESTORE and a 6-digit code', () => {
    expect(canRestore('RESTORE', '123456')).toBe(true);
    expect(canRestore('restore', '123456')).toBe(false);
    expect(canRestore('RESTORE ', '123456')).toBe(false);
    expect(canRestore('RESTORE', '12345')).toBe(false);
    expect(canRestore('RESTORE', '12345a')).toBe(false);
    expect(canRestore('RESTORE', ' 123 456 ')).toBe(true);
  });
});

describe('overlayView', () => {
  const status = (overrides: Partial<RestoreStatus>): RestoreStatus => ({
    state: 'idle',
    percent: 0,
    message: '',
    finished_at: null,
    restart_required: false,
    ...overrides,
  });

  it('maps each restore state', () => {
    expect(
      overlayView(status({ state: 'restoring', percent: 40, message: 'Restoring a backup.' })),
    ).toMatchObject({ kind: 'running', percent: 40 });
    expect(overlayView(status({ state: 'backing_up', percent: 5 })).kind).toBe('running');
    expect(overlayView(status({ state: 'done', percent: 100 })).kind).toBe('done');
    expect(overlayView(status({ state: 'idle' })).kind).toBe('idle');
  });

  it('tells a restore that needs a restart by the flag, not by its wording', () => {
    expect(overlayView(status({ state: 'failed', message: 'Any other sentence.' }))).toEqual({
      kind: 'failed',
      message: 'Any other sentence.',
    });
    expect(
      overlayView(
        status({ state: 'failed', percent: 100, message: 'Restart it.', restart_required: true }),
      ),
    ).toEqual({ kind: 'restart', message: 'Restart it.' });
  });
});

describe('folder and schedule notices', () => {
  it('turns the folder error into a sentence under its title', () => {
    expect(
      folderProblem(
        'the backup folder cannot be used: /srv/b does not exist; create it, or mount a volume there',
      ),
    ).toBe('/srv/b does not exist; create it, or mount a volume there.');
    expect(folderProblem('disk is read-only.')).toBe('Disk is read-only.');
    expect(folderProblem(undefined)).toMatch(/cannot write to it/);
  });

  it('shows a failed scheduled backup only while the schedule is on and until dismissed', () => {
    const failed = {
      last_run_at: '2026-11-23T02:00:00Z',
      ok: false,
      error: 'disk full',
      dismissed: false,
      enabled: true,
    };
    expect(scheduleAlertShown(failed)).toBe(true);
    expect(scheduleAlertShown({ ...failed, enabled: false })).toBe(false);
    expect(scheduleAlertShown({ ...failed, dismissed: true })).toBe(false);
    expect(scheduleAlertShown({ ...failed, ok: true })).toBe(false);
    expect(scheduleAlertShown({ ...failed, last_run_at: null })).toBe(false);
    expect(scheduleAlertShown(undefined)).toBe(false);
  });

  it('names the time and the reason of a failed scheduled backup', () => {
    expect(scheduleFailure('2026-11-23T02:00:00Z', 'disk full')).toMatch(
      /^The scheduled backup at 23 Nov 2026, .+ failed: disk full$/,
    );
    expect(scheduleFailure('2026-11-23T02:00:00Z', '')).toMatch(
      /failed: the reason is in the server log\.$/,
    );
  });
});
