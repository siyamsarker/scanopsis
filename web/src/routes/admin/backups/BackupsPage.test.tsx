// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { configure, fireEvent, screen, waitFor, within } from '@testing-library/react';
import type { BackupInfo, BackupList, JobStatus, RestorePreview } from '../../../api/backups';
import { hideRestoreOverlay } from '../../../api/restoreMode';
import type { Setting } from '../../../api/hooks';
import {
  apiError,
  chooseOption,
  json,
  makeUser,
  mockFetch,
  noContent,
  openMenu,
  renderApp,
  type Handler,
} from '../../../test-utils';

configure({ asyncUtilTimeout: 5000 });

afterEach(() => {
  hideRestoreOverlay();
  // The schedule form keeps unsaved changes for the tab.
  sessionStorage.clear();
});

const idle: JobStatus = {
  state: 'idle',
  progress: { table: '', tables_done: 0, tables_total: 0, bytes: 0 },
  percent: 0,
  started_at: null,
  finished_at: null,
};

function backup(overrides: Partial<BackupInfo>): BackupInfo {
  return {
    name: 'scanopsis-backup-20261122T020000Z-scheduled-abc123.tar.gz',
    kind: 'scheduled',
    size: 5 * 1024 * 1024,
    created_at: '2026-11-22T02:00:00Z',
    created_by: 'scheduler',
    app_version: '0.4.0',
    schema: 12,
    key_matches: true,
    readable: true,
    newer_version: false,
    uploaded_at: null,
    original_name: '',
    ...overrides,
  };
}

const scheduled = backup({});
const manual = backup({
  name: 'scanopsis-backup-20261121T100000Z-manual-def456.tar.gz',
  kind: 'manual',
  created_at: '2026-11-21T10:00:00Z',
  created_by: 'local:alice',
});
const foreign = backup({
  name: 'scanopsis-backup-20261120T100000Z-uploaded-ghi789.tar.gz',
  kind: 'uploaded',
  created_at: '2026-11-20T10:00:00Z',
  created_by: 'gitlab:bob',
  key_matches: false,
  uploaded_at: '2026-11-22T09:00:00Z',
  original_name: 'old-server.tar.gz',
});
const safety = backup({
  name: 'scanopsis-backup-20261119T100000Z-before-restore-jkl012.tar.gz',
  kind: 'before-restore',
  created_at: '2026-11-19T10:00:00Z',
  created_by: 'local:alice',
});

function list(overrides: Partial<BackupList> = {}): BackupList {
  return {
    backups: [scheduled, manual, foreign, safety],
    job: idle,
    schedule: {
      last_run_at: '2026-11-22T02:00:00Z',
      ok: true,
      error: '',
      dismissed: false,
      enabled: true,
    },
    disk: { free_bytes: 12.5 * 1024 ** 3, used_bytes: 20 * 1024 * 1024 },
    folder: { path: '/var/lib/scanopsis/backups', ok: true },
    uploads: [],
    ...overrides,
  };
}

function setting(key: string, type: Setting['type'], value: Setting['value']): Setting {
  return {
    key,
    type,
    secret: false,
    source: 'default',
    value,
    is_set: true,
    env_var: '',
    description: '',
    updated_by: null,
    updated_at: null,
  };
}

const scheduleSettings: Setting[] = [
  setting('retention.days', 'integer', 90),
  setting('backup.schedule_enabled', 'boolean', true),
  setting('backup.schedule_times', 'string_list', ['02:00']),
  setting('backup.time_zone', 'string', 'UTC'),
  setting('backup.keep_days', 'integer', 7),
];

const preview = (overrides: Partial<RestorePreview> = {}): RestorePreview => ({
  info: manual,
  schema_ok: true,
  key_matches: true,
  scans_removed_by_retention: 0,
  oldest_scan_at: '2025-01-01T00:00:00Z',
  retention_days: 90,
  ...overrides,
});

function api(extra?: Handler): Handler {
  return async (call) => {
    const custom = await extra?.(call);
    if (custom) return custom;
    if (call.path === '/api/v1/me') return json(200, makeUser());
    if (call.path === '/api/v1/setup') return json(200, { required: false });
    if (call.path === '/api/v1/admin/backups' && call.method === 'GET') return json(200, list());
    if (call.path === '/api/v1/settings' && call.method === 'GET') {
      return json(200, { items: scheduleSettings });
    }
    return undefined;
  };
}

async function rowOf(text: string) {
  const table = await screen.findByRole('table', { name: 'Backups' });
  return within(table).getByText(text).closest('tr')!;
}

describe('backups page', () => {
  it('tells viewers they need the admin role and requests nothing admin', async () => {
    const { calls } = mockFetch((call) =>
      call.path === '/api/v1/me' ? json(200, makeUser({ role: 'viewer' })) : undefined,
    );
    renderApp('/admin/backups');
    expect(await screen.findByText('You need the admin role to see this page.')).toBeVisible();
    expect(calls.some((c) => c.path.includes('/admin/'))).toBe(false);
  });

  it('lists backups with their kind and who made them', async () => {
    mockFetch(api());
    renderApp('/admin/backups');
    expect(await screen.findByRole('heading', { name: 'Backups', level: 1 })).toBeVisible();
    expect(
      screen.getByText(
        'Back up everything in Scanopsis, keep backups on a schedule, and restore one when you need to.',
      ),
    ).toBeVisible();
    const table = await screen.findByRole('table', { name: 'Backups' });
    const headers = within(table)
      .getAllByRole('columnheader')
      .map((th) => th.textContent);
    expect(headers.slice(0, 5)).toEqual(['Created', 'Kind', 'Size', 'Version', 'Made by']);
    for (const kind of ['Scheduled', 'Manual', 'Uploaded', 'Before restore']) {
      expect(within(table).getByText(kind)).toBeInTheDocument();
    }
    const row = (await rowOf('Manual')) as HTMLElement;
    expect(within(row).getByText('alice')).toBeInTheDocument();
    expect(within(row).getByText('5.0 MiB')).toBeInTheDocument();
    expect(within(row).getByText('0.4.0')).toBeInTheDocument();
    expect(within(table).getByText('old-server.tar.gz')).toBeInTheDocument();
    expect(screen.getByText(/12\.5 GiB free in/)).toHaveTextContent(
      '12.5 GiB free in /var/lib/scanopsis/backups',
    );
  });

  it('marks a backup made with another key and does not offer to restore it', async () => {
    mockFetch(api());
    renderApp('/admin/backups');
    const row = (await rowOf('Uploaded')) as HTMLElement;
    expect(within(row).getByText('Made with another secret key')).toBeInTheDocument();
    const menu = await openMenu(/Actions for the backup from 20 Nov 2026/);
    expect(within(menu).getByRole('menuitem', { name: 'Restore' })).toHaveAttribute(
      'aria-disabled',
      'true',
    );
    expect(within(menu).getByRole('menuitem', { name: 'Download' })).toHaveAttribute(
      'href',
      `/api/v1/admin/backups/${foreign.name}/download`,
    );
  });

  it('offers the first backup when there is none', async () => {
    mockFetch(
      api((call) =>
        call.path === '/api/v1/admin/backups' ? json(200, list({ backups: [] })) : undefined,
      ),
    );
    renderApp('/admin/backups');
    expect(await screen.findByRole('heading', { name: 'No backups yet' })).toBeVisible();
    expect(screen.getAllByRole('button', { name: 'Back up now' })).toHaveLength(2);
  });

  it('names the folder and the fix when the folder cannot be used', async () => {
    const error =
      'the backup folder cannot be used: /var/lib/scanopsis/backups does not exist; create it and make it writable for the user the server runs as (uid 65532 in the container image), or mount a volume there';
    mockFetch(
      api((call) =>
        call.path === '/api/v1/admin/backups'
          ? json(
              200,
              list({
                backups: [],
                folder: { path: '/var/lib/scanopsis/backups', ok: false, error },
              }),
            )
          : undefined,
      ),
    );
    renderApp('/admin/backups');
    const alert = await screen.findByRole('region', { name: 'The backup folder cannot be used' });
    expect(alert).toHaveTextContent('/var/lib/scanopsis/backups');
    expect(alert).toHaveTextContent('mount a volume there');
    expect(screen.getAllByRole('button', { name: 'Back up now' })[0]).toBeDisabled();
  });

  it('shows the failed scheduled backup', async () => {
    mockFetch(
      api((call) =>
        call.path === '/api/v1/admin/backups'
          ? json(
              200,
              list({
                schedule: {
                  last_run_at: '2026-11-23T02:00:00Z',
                  ok: false,
                  error: 'disk full',
                  dismissed: false,
                  enabled: true,
                },
              }),
            )
          : undefined,
      ),
    );
    renderApp('/admin/backups');
    expect(
      await screen.findByText(
        /^The scheduled backup at 23 Nov 2026, \d\d:\d\d .+ failed\. Disk full\.$/,
      ),
    ).toBeVisible();
    // The line next to the schedule switch says so too, and may wrap.
    const form = await screen.findByRole('region', { name: 'Scheduled backups' });
    const line = await within(form).findByText(/^Last scheduled backup failed/);
    expect(line.closest('.whitespace-nowrap')).toBeNull();
  });

  it('dismisses the failed scheduled backup until the next run', async () => {
    let dismissed = false;
    const { calls } = mockFetch(
      api((call) => {
        if (call.path === '/api/v1/admin/backups/schedule-status/dismiss') {
          dismissed = true;
          return noContent();
        }
        if (call.path === '/api/v1/admin/backups') {
          return json(
            200,
            list({
              schedule: {
                last_run_at: '2026-11-23T02:00:00Z',
                ok: false,
                error: 'disk full',
                dismissed,
                enabled: true,
              },
            }),
          );
        }
        return undefined;
      }),
    );
    renderApp('/admin/backups');
    const notice = await screen.findByRole('region', { name: 'Scheduled backup failed' });
    expect(notice).toHaveTextContent('Dismissing hides this until the next scheduled backup.');
    fireEvent.click(within(notice).getByRole('button', { name: 'Dismiss' }));
    await waitFor(() =>
      expect(screen.queryByRole('region', { name: 'Scheduled backup failed' })).toBeNull(),
    );
    expect(
      calls.some(
        (c) => c.method === 'POST' && c.path === '/api/v1/admin/backups/schedule-status/dismiss',
      ),
    ).toBe(true);
  });

  it('hides the failed scheduled backup while the schedule is off', async () => {
    mockFetch(
      api((call) =>
        call.path === '/api/v1/admin/backups'
          ? json(
              200,
              list({
                schedule: {
                  last_run_at: '2026-11-23T02:00:00Z',
                  ok: false,
                  error: 'disk full',
                  dismissed: false,
                  enabled: false,
                },
              }),
            )
          : undefined,
      ),
    );
    renderApp('/admin/backups');
    await screen.findByRole('table', { name: 'Backups' });
    expect(screen.queryByRole('region', { name: 'Scheduled backup failed' })).toBeNull();
  });

  it('marks a backup made by a newer version and does not offer to restore it', async () => {
    const newer = backup({
      name: 'scanopsis-backup-20261118T100000Z-uploaded-mno345.tar.gz',
      kind: 'uploaded',
      created_at: '2026-11-18T10:00:00Z',
      readable: false,
      newer_version: true,
    });
    mockFetch(
      api((call) =>
        call.path === '/api/v1/admin/backups' ? json(200, list({ backups: [newer] })) : undefined,
      ),
    );
    renderApp('/admin/backups');
    const table = await screen.findByRole('table', { name: 'Backups' });
    expect(within(table).getByText('Made by a newer version of Scanopsis')).toBeInTheDocument();
    expect(within(table).queryByText('Cannot be read')).toBeNull();
    const menu = await openMenu(/Actions for the backup from 18 Nov 2026/);
    expect(within(menu).getByRole('menuitem', { name: 'Restore' })).toHaveAttribute(
      'aria-disabled',
      'true',
    );
    expect(menu).toHaveTextContent('Only that version of Scanopsis or a later one can restore it.');
  });

  it('marks a damaged backup, says why, and offers only download and delete', async () => {
    const damaged = backup({
      name: 'scanopsis-backup-20261118T100000Z-manual-trc123.tar.gz',
      kind: 'manual',
      created_at: '2026-11-18T10:00:00Z',
      readable: false,
      problem: 'The file is damaged: the file ends too early.',
    });
    mockFetch(
      api((call) =>
        call.path === '/api/v1/admin/backups' ? json(200, list({ backups: [damaged] })) : undefined,
      ),
    );
    renderApp('/admin/backups');
    const table = await screen.findByRole('table', { name: 'Backups' });
    expect(within(table).getByText('Cannot be read')).toBeInTheDocument();
    expect(within(table).getByText('The file is damaged: the file ends too early.')).toBeVisible();
    const menu = await openMenu(/Actions for the backup from 18 Nov 2026/);
    expect(within(menu).getByRole('menuitem', { name: 'Restore' })).toHaveAttribute(
      'aria-disabled',
      'true',
    );
    expect(within(menu).getByRole('menuitem', { name: 'Download' })).not.toHaveAttribute(
      'aria-disabled',
    );
    expect(menu).toHaveTextContent('Only a readable backup can be restored.');
  });

  it('lists unfinished uploads and cancels one', async () => {
    const minute = 60 * 1000;
    const { calls } = mockFetch(
      api((call) => {
        if (call.path === '/api/v1/admin/backup-uploads/u1' && call.method === 'DELETE') {
          return noContent();
        }
        return call.path === '/api/v1/admin/backups'
          ? json(
              200,
              list({
                uploads: [
                  {
                    id: 'u1',
                    file_name: 'left-behind.tar.gz',
                    size: 100 * 1024 * 1024,
                    received: 40 * 1024 * 1024,
                    state: 'uploading',
                    updated_at: new Date(Date.now() - 12 * minute).toISOString(),
                  },
                  {
                    id: 'u2',
                    file_name: 'being-checked.tar.gz',
                    size: 1024,
                    received: 1024,
                    state: 'checking',
                    updated_at: new Date(Date.now() - 2 * minute).toISOString(),
                  },
                ],
              }),
            )
          : undefined;
      }),
    );
    renderApp('/admin/backups');
    const region = await screen.findByRole('region', { name: 'Unfinished uploads' });
    expect(region).toHaveTextContent('left-behind.tar.gz');
    expect(region).toHaveTextContent('40.0 MiB of 100.0 MiB received, last part 12 minutes ago');
    expect(region).toHaveTextContent('being-checked.tar.gz');
    expect(region).toHaveTextContent('Being checked, since 2 minutes ago');
    fireEvent.click(
      within(region).getByRole('button', { name: 'Cancel the upload of left-behind.tar.gz' }),
    );
    expect(await screen.findByText('Upload cancelled')).toBeInTheDocument();
    expect(
      calls.some((c) => c.method === 'DELETE' && c.path === '/api/v1/admin/backup-uploads/u1'),
    ).toBe(true);
  });

  it('reports a failed restore of a scheduled backup', async () => {
    mockFetch(
      api((call) =>
        call.path === '/api/v1/admin/backups'
          ? json(
              200,
              list({
                job: {
                  ...idle,
                  state: 'failed',
                  type: 'restore',
                  kind: 'scheduled',
                  name: scheduled.name,
                  message: 'The file is damaged: the file ends too early.',
                },
              }),
            )
          : undefined,
      ),
    );
    renderApp('/admin/backups');
    const notice = await screen.findByRole('region', { name: 'Last job failed' });
    expect(notice).toHaveTextContent('The last restore did not finish. Nothing was changed.');
    expect(notice).toHaveTextContent('The file is damaged: the file ends too early.');
  });

  it('backs up now, shows the progress and then the new backup', async () => {
    const created = backup({
      name: 'scanopsis-backup-20261123T120000Z-manual-zzz999.tar.gz',
      kind: 'manual',
      created_at: '2026-11-23T12:00:00Z',
      created_by: 'gitlab:ada',
      app_version: '0.4.1',
    });
    const running: JobStatus = {
      ...idle,
      state: 'backing_up',
      type: 'backup',
      kind: 'manual',
      progress: { table: 'findings', tables_done: 4, tables_total: 18, bytes: 2 * 1024 * 1024 },
      percent: 22,
      started_at: '2026-11-23T12:00:00Z',
    };
    let started = false;
    let polls = 0;
    const { calls } = mockFetch(
      api((call) => {
        if (call.path !== '/api/v1/admin/backups') return undefined;
        if (call.method === 'POST') {
          started = true;
          return json(202, { ...running, progress: { ...idle.progress }, percent: 0 });
        }
        if (!started) return json(200, list());
        polls += 1;
        if (polls === 1) return json(200, list({ job: running }));
        return json(
          200,
          list({
            backups: [created, scheduled, manual, foreign, safety],
            job: { ...running, state: 'done', name: created.name, percent: 100 },
          }),
        );
      }),
    );
    renderApp('/admin/backups');
    await screen.findByRole('table', { name: 'Backups' });
    fireEvent.click(screen.getByRole('button', { name: 'Back up now' }));
    expect(await screen.findByText('Backup started')).toBeInTheDocument();
    const progress = await screen.findByRole('progressbar', { name: 'Backup progress' });
    expect(progress).toHaveAttribute('aria-valuenow', '22');
    expect(screen.getByText('Copying findings, 4 of 18 tables, 2.0 MiB so far')).toBeVisible();
    expect(await screen.findByText('Backup finished')).toBeInTheDocument();
    expect(await screen.findByText('0.4.1')).toBeInTheDocument();
    expect(screen.queryByRole('progressbar', { name: 'Backup progress' })).not.toBeInTheDocument();
    expect(calls.filter((c) => c.method === 'POST')).toHaveLength(1);
  });

  it('starts one backup on a double click', async () => {
    const { calls } = mockFetch(
      api((call) =>
        call.path === '/api/v1/admin/backups' && call.method === 'POST'
          ? json(202, { ...idle, state: 'backing_up', type: 'backup', kind: 'manual' })
          : undefined,
      ),
    );
    renderApp('/admin/backups');
    await screen.findByRole('table', { name: 'Backups' });
    const button = screen.getByRole('button', { name: 'Back up now' });
    fireEvent.click(button);
    fireEvent.click(button);
    expect(await screen.findByText('Backup started')).toBeInTheDocument();
    expect(calls.filter((c) => c.method === 'POST')).toHaveLength(1);
    expect(screen.queryByText('The backup could not start')).toBeNull();
  });

  it('says why Restore is off while a backup runs', async () => {
    mockFetch(
      api((call) =>
        call.path === '/api/v1/admin/backups'
          ? json(
              200,
              list({ job: { ...idle, state: 'backing_up', type: 'backup', kind: 'manual' } }),
            )
          : undefined,
      ),
    );
    renderApp('/admin/backups');
    await screen.findByRole('table', { name: 'Backups' });
    const menu = await openMenu(/Actions for the backup from 21 Nov 2026/);
    expect(within(menu).getByRole('menuitem', { name: 'Restore' })).toHaveAttribute(
      'aria-disabled',
      'true',
    );
    expect(menu).toHaveTextContent('A backup or restore is running. Restore once it has finished.');
  });

  it('explains a refused backup', async () => {
    mockFetch(
      api((call) =>
        call.path === '/api/v1/admin/backups' && call.method === 'POST'
          ? apiError(
              507,
              'no_space',
              'A backup needs about 2.0 GiB and the folder has 1.0 GiB free.',
            )
          : undefined,
      ),
    );
    renderApp('/admin/backups');
    await screen.findByRole('table', { name: 'Backups' });
    fireEvent.click(screen.getByRole('button', { name: 'Back up now' }));
    expect(await screen.findByText('The backup could not start')).toBeInTheDocument();
    expect(
      screen.getByText('A backup needs about 2.0 GiB and the folder has 1.0 GiB free.'),
    ).toBeInTheDocument();
  });

  it('deletes a backup after confirming', async () => {
    const { calls } = mockFetch(
      api((call) =>
        call.method === 'DELETE' && call.path === `/api/v1/admin/backups/${manual.name}`
          ? noContent()
          : undefined,
      ),
    );
    renderApp('/admin/backups');
    const menu = await openMenu(/Actions for the backup from 21 Nov 2026/);
    fireEvent.click(within(menu).getByRole('menuitem', { name: 'Delete' }));
    const dialog = await screen.findByRole('alertdialog', {
      name: /^Delete the backup from 21 Nov 2026/,
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete backup' }));
    expect(await screen.findByText('Backup deleted')).toBeInTheDocument();
    expect(calls.some((c) => c.method === 'DELETE')).toBe(true);
  });
});

describe('scheduled backups', () => {
  it('validates times and keep days before saving', async () => {
    const { calls } = mockFetch(
      api((call) =>
        call.path === '/api/v1/settings' && call.method === 'PATCH'
          ? json(200, { items: scheduleSettings })
          : undefined,
      ),
    );
    renderApp('/admin/backups');
    const form = await screen.findByRole('region', { name: 'Scheduled backups' });
    expect(await within(form).findByRole('switch', { name: 'Scheduled backups' })).toBeChecked();
    expect(within(form).getByText('The newest scheduled backup is always kept.')).toBeVisible();

    const time = within(form).getByRole('textbox', { name: 'Add a time' });
    fireEvent.change(time, { target: { value: '25:00' } });
    fireEvent.click(within(form).getByRole('button', { name: 'Add time' }));
    expect(within(form).getByText('Use a 24-hour time such as 02:00.')).toBeVisible();

    fireEvent.change(time, { target: { value: '02:00' } });
    fireEvent.keyDown(time, { key: 'Enter' });
    expect(within(form).getByText('02:00 is already in the list.')).toBeVisible();

    fireEvent.change(time, { target: { value: '14:30' } });
    fireEvent.click(within(form).getByRole('button', { name: 'Add time' }));
    expect(within(form).getByRole('button', { name: 'Remove 14:30' })).toBeVisible();

    const keep = within(form).getByRole('spinbutton', { name: 'Keep scheduled backups for' });
    fireEvent.change(keep, { target: { value: '0' } });
    fireEvent.click(within(form).getByRole('button', { name: 'Save schedule' }));
    expect(within(form).getByText('Use a whole number from 1 to 365.')).toBeVisible();
    expect(calls.some((c) => c.method === 'PATCH')).toBe(false);

    fireEvent.change(keep, { target: { value: '14' } });
    fireEvent.click(within(form).getByRole('button', { name: 'Remove 02:00' }));
    fireEvent.click(within(form).getByRole('button', { name: 'Save schedule' }));
    expect(await screen.findByText('Schedule saved')).toBeInTheDocument();
    expect(calls.find((c) => c.method === 'PATCH')?.body).toEqual({
      'backup.schedule_enabled': true,
      'backup.schedule_times': ['14:30'],
      'backup.time_zone': 'UTC',
      'backup.keep_days': 14,
    });
  });

  it('saves a typed time without Add time, or says what is wrong with it', async () => {
    const { calls } = mockFetch(
      api((call) =>
        call.path === '/api/v1/settings' && call.method === 'PATCH'
          ? json(200, { items: scheduleSettings })
          : undefined,
      ),
    );
    renderApp('/admin/backups');
    const form = await screen.findByRole('region', { name: 'Scheduled backups' });
    const time = await within(form).findByRole('textbox', { name: 'Add a time' });
    fireEvent.change(time, { target: { value: '7:5' } });
    fireEvent.click(within(form).getByRole('button', { name: 'Save schedule' }));
    expect(
      within(form).getByText(
        'Use a 24-hour time such as 02:00. Correct or clear this time to save the schedule.',
      ),
    ).toBeVisible();
    expect(calls.some((c) => c.method === 'PATCH')).toBe(false);
    fireEvent.change(time, { target: { value: '07:05' } });
    fireEvent.click(within(form).getByRole('button', { name: 'Save schedule' }));
    expect(await screen.findByText('Schedule saved')).toBeInTheDocument();
    expect(calls.find((c) => c.method === 'PATCH')?.body).toMatchObject({
      'backup.schedule_times': ['02:00', '07:05'],
    });
  });

  it('shows every problem when a typed time keeps the schedule from saving', async () => {
    const { calls } = mockFetch(api());
    renderApp('/admin/backups');
    const form = await screen.findByRole('region', { name: 'Scheduled backups' });
    const time = await within(form).findByRole('textbox', { name: 'Add a time' });
    fireEvent.change(time, { target: { value: '0730' } });
    const keep = within(form).getByRole('spinbutton', { name: 'Keep scheduled backups for' });
    fireEvent.change(keep, { target: { value: '0' } });
    fireEvent.click(within(form).getByRole('button', { name: 'Save schedule' }));
    expect(
      within(form).getByText(
        'Use a 24-hour time such as 02:00. Correct or clear this time to save the schedule.',
      ),
    ).toBeVisible();
    expect(within(form).getByText('Use a whole number from 1 to 365.')).toBeVisible();
    expect(time).toHaveFocus();
    expect(calls.some((c) => c.method === 'PATCH')).toBe(false);
  });

  it('keeps unsaved changes of the schedule when the page is left and opened again', async () => {
    mockFetch(api());
    renderApp('/admin/backups');
    let form = await screen.findByRole('region', { name: 'Scheduled backups' });
    const keep = await within(form).findByRole('spinbutton', {
      name: 'Keep scheduled backups for',
    });
    fireEvent.change(keep, { target: { value: '9' } });
    const leaving = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(leaving);
    expect(leaving.defaultPrevented).toBe(true);

    fireEvent.click(screen.getAllByRole('link', { name: 'Portfolio' })[0]);
    await waitFor(() =>
      expect(screen.queryByRole('region', { name: 'Scheduled backups' })).toBeNull(),
    );
    fireEvent.click(screen.getAllByRole('link', { name: 'Backups' })[0]);
    form = await screen.findByRole('region', { name: 'Scheduled backups' });
    expect(
      await within(form).findByRole('spinbutton', { name: 'Keep scheduled backups for' }),
    ).toHaveValue(9);
    expect(
      within(form).getByText('Your unsaved changes are back. Save the schedule or undo them.'),
    ).toBeVisible();
    fireEvent.click(within(form).getByRole('button', { name: 'Undo changes' }));
    expect(
      within(form).getByRole('spinbutton', { name: 'Keep scheduled backups for' }),
    ).toHaveValue(7);
  });

  it('keeps unsaved schedule changes to the admin who made them, until a sign-out', async () => {
    mockFetch(api());
    const first = renderApp('/admin/backups');
    const form = await screen.findByRole('region', { name: 'Scheduled backups' });
    fireEvent.change(
      await within(form).findByRole('spinbutton', { name: 'Keep scheduled backups for' }),
      { target: { value: '9' } },
    );
    first.unmount();

    // Another administrator in the same tab does not get them.
    mockFetch(
      api((call) =>
        call.path === '/api/v1/me' ? json(200, makeUser({ id: 2, username: 'grace' })) : undefined,
      ),
    );
    const second = renderApp('/admin/backups');
    const other = await screen.findByRole('region', { name: 'Scheduled backups' });
    expect(
      await within(other).findByRole('spinbutton', { name: 'Keep scheduled backups for' }),
    ).toHaveValue(7);
    second.unmount();

    // A session that ended (401) takes them away.
    mockFetch(
      api((call) =>
        call.path === '/api/v1/me' ? apiError(401, 'unauthenticated', 'Sign in.') : undefined,
      ),
    );
    expect(Object.keys(sessionStorage).filter((k) => k.startsWith('scanopsis.'))).not.toEqual([]);
    renderApp('/admin/backups');
    await waitFor(() =>
      expect(Object.keys(sessionStorage).filter((k) => k.startsWith('scanopsis.'))).toEqual([]),
    );
  });

  it('narrows the time zones as a filter is typed, and keeps Enter from saving', async () => {
    const { calls } = mockFetch(api());
    renderApp('/admin/backups');
    const form = await screen.findByRole('region', { name: 'Scheduled backups' });
    const filter = await within(form).findByRole('searchbox', { name: 'Filter time zones' });
    expect(filter).toHaveAttribute('placeholder', 'Type a city, such as Berlin');
    fireEvent.change(filter, { target: { value: 'tokyo' } });
    expect(
      await within(form).findByText('1 time zone matches. Press Enter to choose Asia/Tokyo.'),
    ).toBeVisible();
    expect(calls.some((c) => c.method === 'PATCH')).toBe(false);
    await chooseOption('Time zone', 'Asia/Tokyo');
    expect(within(form).getByRole('combobox', { name: 'Time zone' })).toHaveTextContent(
      'Asia/Tokyo',
    );
    fireEvent.change(filter, { target: { value: 'no such place' } });
    expect(await within(form).findByText('No time zone matches.')).toBeVisible();
  });

  it('chooses the first matching time zone with Enter in the filter, without saving', async () => {
    const { calls } = mockFetch(api());
    renderApp('/admin/backups');
    const form = await screen.findByRole('region', { name: 'Scheduled backups' });
    const filter = await within(form).findByRole('searchbox', { name: 'Filter time zones' });
    fireEvent.change(filter, { target: { value: 'kolkata' } });
    fireEvent.keyDown(filter, { key: 'Enter' });
    await waitFor(() =>
      expect(within(form).getByRole('combobox', { name: 'Time zone' })).toHaveTextContent(
        'Asia/Kolkata',
      ),
    );
    expect(within(form).getByRole('button', { name: 'Save schedule' })).toBeEnabled();
    expect(calls.some((c) => c.method === 'PATCH')).toBe(false);
  });

  it('ties the keep-days problem to its field', async () => {
    mockFetch(api());
    renderApp('/admin/backups');
    const form = await screen.findByRole('region', { name: 'Scheduled backups' });
    const keep = await within(form).findByRole('spinbutton', {
      name: 'Keep scheduled backups for',
    });
    fireEvent.change(keep, { target: { value: '400' } });
    fireEvent.click(within(form).getByRole('button', { name: 'Save schedule' }));
    expect(keep).toHaveAccessibleDescription(/Use a whole number from 1 to 365\./);
  });

  it('needs at least one time', async () => {
    mockFetch(api());
    renderApp('/admin/backups');
    const form = await screen.findByRole('region', { name: 'Scheduled backups' });
    fireEvent.click(await within(form).findByRole('button', { name: 'Remove 02:00' }));
    fireEvent.click(within(form).getByRole('button', { name: 'Save schedule' }));
    expect(within(form).getByText('Add at least one time.')).toBeVisible();
  });

  it('shows the server problem of a field', async () => {
    mockFetch(
      api((call) =>
        call.path === '/api/v1/settings' && call.method === 'PATCH'
          ? json(422, {
              error: {
                code: 'invalid_settings',
                message: 'Some settings are invalid.',
                fields: {
                  'backup.time_zone': 'Must be a time zone name such as UTC or Europe/Berlin.',
                },
              },
            })
          : undefined,
      ),
    );
    renderApp('/admin/backups');
    const form = await screen.findByRole('region', { name: 'Scheduled backups' });
    fireEvent.click(await within(form).findByRole('switch', { name: 'Scheduled backups' }));
    fireEvent.click(within(form).getByRole('button', { name: 'Save schedule' }));
    expect(
      await within(form).findByText('Must be a time zone name such as UTC or Europe/Berlin.'),
    ).toBeVisible();
  });
});

describe('restore', () => {
  async function openRestore(previewBody: RestorePreview, extra?: Handler, checks = 0) {
    let left = checks;
    const fetch = mockFetch(
      api(async (call) => {
        const custom = await extra?.(call);
        if (custom) return custom;
        if (call.path === `/api/v1/admin/backups/${manual.name}/preview`) {
          if (left > 0) {
            left -= 1;
            return json(202, { state: 'checking' });
          }
          return json(200, previewBody);
        }
        return undefined;
      }),
    );
    renderApp('/admin/backups');
    const menu = await openMenu(/Actions for the backup from 21 Nov 2026/);
    fireEvent.click(within(menu).getByRole('menuitem', { name: 'Restore' }));
    const dialog = await screen.findByRole('alertdialog', {
      name: /^Restore the backup from 21 Nov 2026/,
    });
    return { dialog, ...fetch };
  }

  it('enables the restore only for RESTORE and a 6-digit code', async () => {
    const { dialog } = await openRestore(preview());
    await within(dialog).findByText(
      'Everything in Scanopsis is replaced by this backup: users, settings, services, scans and the audit log.',
    );
    expect(dialog).toHaveTextContent('Everyone is signed out, including you.');
    expect(dialog).toHaveTextContent(
      'Pipeline uploads are turned away while the restore runs. Scans uploaded in that time are not kept.',
    );
    expect(dialog).toHaveTextContent(
      'Before the restore, Scanopsis backs up the current state, so you can go back.',
    );
    expect(dialog).not.toHaveTextContent('daily clean-up');
    // The backup's version and who made it.
    const facts = within(dialog).getByText('Scanopsis version').closest('dl')!;
    expect(facts).toHaveTextContent('Scanopsis version0.4.0');
    expect(facts).toHaveTextContent('Made byalice');
    // Without the backup of the current state there is no way back.
    fireEvent.click(within(dialog).getByRole('checkbox', { name: /Skip the backup/ }));
    expect(dialog).toHaveTextContent('Without this backup, the restore cannot be undone.');
    expect(dialog).not.toHaveTextContent('so you can go back');
    fireEvent.click(within(dialog).getByRole('checkbox', { name: /Skip the backup/ }));
    const button = within(dialog).getByRole('button', { name: 'Restore backup' });
    const phrase = within(dialog).getByRole('textbox', { name: 'Type RESTORE to confirm' });
    const code = within(dialog).getByRole('textbox', { name: 'Two-factor code' });
    expect(button).toBeDisabled();
    fireEvent.change(phrase, { target: { value: 'restore' } });
    fireEvent.change(code, { target: { value: '123456' } });
    expect(button).toBeDisabled();
    fireEvent.change(phrase, { target: { value: 'RESTORE' } });
    fireEvent.change(code, { target: { value: '12345' } });
    expect(button).toBeDisabled();
    fireEvent.change(code, { target: { value: '123456' } });
    expect(button).toBeEnabled();
  });

  it('says it checks the backup while the server reads it, then shows the preview', async () => {
    const { dialog, calls } = await openRestore(preview(), undefined, 2);
    const status = await within(dialog).findByRole('status');
    expect(status).toHaveTextContent('Checking the backup…');
    expect(within(dialog).getByRole('button', { name: 'Restore backup' })).toBeDisabled();
    await within(dialog).findByText(
      'Everything in Scanopsis is replaced by this backup: users, settings, services, scans and the audit log.',
      undefined,
      { timeout: 10_000 },
    );
    expect(calls.filter((c) => c.path.endsWith('/preview')).length).toBe(3);
  });

  it('warns about scans the clean-up removes when there are some', async () => {
    const { dialog } = await openRestore(preview({ scans_removed_by_retention: 37 }));
    expect(
      await within(dialog).findByText(
        'The next daily clean-up removes up to 37 scans older than 90 days from this backup. To keep them, raise the retention period on the Settings page right after the restore.',
      ),
    ).toBeVisible();
  });

  it('explains a backup made with another key and keeps the restore off', async () => {
    const { dialog } = await openRestore(
      preview({ key_matches: false, scans_removed_by_retention: 0 }),
    );
    expect(
      await within(dialog).findByText(
        'This backup was made with another secret key and cannot be restored into this installation. To use it, set up a new installation with that secret key and restore the backup on its setup screen. Never change the secret key of an installation in use.',
      ),
    ).toBeVisible();
    expect(dialog).not.toHaveTextContent('scans older than');
    const phrase = within(dialog).queryByRole('textbox', { name: 'Type RESTORE to confirm' });
    if (phrase) fireEvent.change(phrase, { target: { value: 'RESTORE' } });
    const code = within(dialog).queryByRole('textbox', { name: 'Two-factor code' });
    if (code) fireEvent.change(code, { target: { value: '123456' } });
    expect(within(dialog).getByRole('button', { name: 'Restore backup' })).toBeDisabled();
  });

  it('starts the restore and shows the restore screen', async () => {
    let restoring = false;
    const { dialog, calls } = await openRestore(preview(), (call) => {
      if (call.path === `/api/v1/admin/backups/${manual.name}/restore`) {
        restoring = true;
        return json(202, { ...idle, state: 'backing_up', type: 'restore', kind: 'before-restore' });
      }
      if (call.path === '/api/v1/restore/status') {
        return json(200, {
          state: 'restoring',
          percent: 60,
          message: 'Restoring a backup.',
          finished_at: null,
          restart_required: false,
        });
      }
      if (restoring && call.path !== '/api/v1/restore/status') {
        return apiError(503, 'restoring', 'A backup is being restored.', { 'Retry-After': '30' });
      }
      return undefined;
    });
    await within(dialog).findByText(/Everything that changed after/);
    fireEvent.change(within(dialog).getByRole('textbox', { name: 'Type RESTORE to confirm' }), {
      target: { value: 'RESTORE' },
    });
    fireEvent.change(within(dialog).getByRole('textbox', { name: 'Two-factor code' }), {
      target: { value: '123456' },
    });
    fireEvent.click(
      within(dialog).getByRole('checkbox', {
        name: 'Skip the backup of the current state (only if the disk has no room)',
      }),
    );
    fireEvent.click(within(dialog).getByRole('button', { name: 'Restore backup' }));
    const overlay = await screen.findByRole('alertdialog', { name: 'Restoring a backup' });
    expect(await within(overlay).findByRole('progressbar')).toHaveAttribute('aria-valuenow', '60');
    expect(calls.find((c) => c.path.endsWith('/restore'))?.body).toEqual({
      confirm: 'RESTORE',
      code: '123456',
      skip_safety_backup: true,
    });
    expect(screen.queryByText('Backup restored')).not.toBeInTheDocument();
  });

  it('shows a wrong code next to the field', async () => {
    const { dialog } = await openRestore(preview(), (call) =>
      call.path.endsWith('/restore')
        ? apiError(422, 'invalid_code', 'The two-factor code is not valid.')
        : undefined,
    );
    await within(dialog).findByText(/Everything that changed after/);
    fireEvent.change(within(dialog).getByRole('textbox', { name: 'Type RESTORE to confirm' }), {
      target: { value: 'RESTORE' },
    });
    fireEvent.change(within(dialog).getByRole('textbox', { name: 'Two-factor code' }), {
      target: { value: '000000' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Restore backup' }));
    expect(await within(dialog).findByText('The two-factor code is not valid.')).toBeVisible();
    expect(within(dialog).getByRole('textbox', { name: 'Two-factor code' })).toHaveAttribute(
      'aria-invalid',
      'true',
    );
  });

  it('sends the admin to sign in when the session ended', async () => {
    const { dialog } = await openRestore(preview(), (call) =>
      call.path.endsWith('/restore')
        ? apiError(401, 'unauthenticated', 'Sign in again.')
        : undefined,
    );
    await within(dialog).findByText(/Everything that changed after/);
    fireEvent.change(within(dialog).getByRole('textbox', { name: 'Type RESTORE to confirm' }), {
      target: { value: 'RESTORE' },
    });
    fireEvent.change(within(dialog).getByRole('textbox', { name: 'Two-factor code' }), {
      target: { value: '000000' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Restore backup' }));
    await waitFor(() =>
      expect(screen.getByTestId('location')).toHaveTextContent(
        '/login?return_to=%2Fadmin%2Fbackups',
      ),
    );
  });
});

describe('restore mode', () => {
  it('shows the restore screen once when any request answers 503 restoring', async () => {
    const { calls } = mockFetch(
      api((call) => {
        if (call.path === '/api/v1/restore/status') {
          return json(200, {
            state: 'backing_up',
            percent: 10,
            message: 'Saving the current data before the restore.',
            finished_at: null,
            restart_required: false,
          });
        }
        if (call.path === '/api/v1/admin/backups') {
          return apiError(503, 'restoring', 'A backup is being restored.', { 'Retry-After': '30' });
        }
        return undefined;
      }),
    );
    renderApp('/admin/backups');
    const overlay = await screen.findByRole('alertdialog', { name: 'Restoring a backup' });
    expect(
      await within(overlay).findByText('Saving the current data before the restore.'),
    ).toBeVisible();
    expect(screen.getAllByRole('alertdialog')).toHaveLength(1);
    expect(calls.filter((c) => c.path === '/api/v1/admin/backups').length).toBeLessThanOrEqual(2);
  });
});
