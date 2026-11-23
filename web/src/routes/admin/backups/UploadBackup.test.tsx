// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { configure, fireEvent, screen, waitFor, within } from '@testing-library/react';
import type { BackupInfo } from '../../../api/backups';
import { apiError, json, makeUser, mockFetch, renderApp, type Handler } from '../../../test-utils';

configure({ asyncUtilTimeout: 5000 });

const uploaded: BackupInfo = {
  name: 'scanopsis-backup-20261123T120000Z-uploaded-abc123.tar.gz',
  kind: 'uploaded',
  size: 10,
  created_at: '2026-11-01T02:00:00Z',
  created_by: 'scheduler',
  app_version: '0.4.0',
  schema: 12,
  key_matches: true,
  readable: true,
  uploaded_at: '2026-11-23T12:00:00Z',
  original_name: 'from-staging.tar.gz',
};

function api(extra?: Handler): Handler {
  return async (call) => {
    const custom = await extra?.(call);
    if (custom) return custom;
    if (call.path === '/api/v1/me') return json(200, makeUser());
    if (call.path === '/api/v1/setup') return json(200, { required: false });
    if (call.path === '/api/v1/settings') return json(200, { items: [] });
    if (call.path === '/api/v1/admin/backups') {
      return json(200, {
        backups: [],
        job: {
          state: 'idle',
          progress: { table: '', tables_done: 0, tables_total: 0, bytes: 0 },
          percent: 0,
          started_at: null,
          finished_at: null,
        },
        schedule: { last_run_at: null, ok: true, error: '' },
        disk: { free_bytes: 1024 ** 3, used_bytes: 0 },
        folder: { path: '/var/lib/scanopsis/backups', ok: true },
      });
    }
    const status = { id: 'u1', file_name: 'from-staging.tar.gz', size: 10, chunk_size: 4 };
    if (call.path === '/api/v1/admin/backup-uploads' && call.method === 'POST') {
      return json(201, { ...status, received: 0 });
    }
    const part = /^\/api\/v1\/admin\/backup-uploads\/u1\?offset=(\d+)$/.exec(call.path);
    if (part && call.method === 'PUT') {
      return json(200, { ...status, received: Math.min(10, Number(part[1]) + 4) });
    }
    if (call.path === '/api/v1/admin/backup-uploads/u1/complete') return json(201, uploaded);
    return undefined;
  };
}

async function chooseFile() {
  renderApp('/admin/backups');
  const open = await screen.findByRole('button', { name: 'Upload a backup' });
  // It waits for the folder to be known.
  await waitFor(() => expect(open).toBeEnabled());
  fireEvent.click(open);
  const dialog = await screen.findByRole('dialog', { name: 'Upload a backup' });
  const input = within(dialog).getByLabelText('Backup file');
  fireEvent.change(input, {
    target: { files: [new File(['1234567890'], 'from-staging.tar.gz')] },
  });
  return dialog;
}

describe('upload a backup', () => {
  it('sends the file in parts and lists it', async () => {
    const { calls } = mockFetch(api());
    await chooseFile();
    expect(await screen.findByText('Backup uploaded')).toBeInTheDocument();
    expect(calls.filter((c) => c.method === 'PUT').map((c) => c.path)).toEqual([
      '/api/v1/admin/backup-uploads/u1?offset=0',
      '/api/v1/admin/backup-uploads/u1?offset=4',
      '/api/v1/admin/backup-uploads/u1?offset=8',
    ]);
    expect(calls.find((c) => c.path === '/api/v1/admin/backup-uploads')?.body).toEqual({
      file_name: 'from-staging.tar.gz',
      size: 10,
    });
    expect(screen.queryByRole('dialog', { name: 'Upload a backup' })).not.toBeInTheDocument();
  });

  it('says why a file was refused', async () => {
    mockFetch(
      api((call) =>
        call.path.endsWith('/complete')
          ? apiError(422, 'key_mismatch', 'This backup was made with another SCANOPSIS_SECRET_KEY.')
          : undefined,
      ),
    );
    const dialog = await chooseFile();
    expect(
      await within(dialog).findByText('This backup was made with another SCANOPSIS_SECRET_KEY.'),
    ).toBeVisible();
    expect(within(dialog).getByRole('button', { name: 'Choose another file' })).toBeVisible();
  });
});

describe('upload a backup, leaving early', () => {
  it('cancels an upload the server refused when the dialog closes', async () => {
    const { calls } = mockFetch(
      api((call) =>
        call.path.endsWith('/complete')
          ? apiError(507, 'no_space', 'The backup folder has too little free space.')
          : call.method === 'DELETE'
            ? new Response(null, { status: 204 })
            : undefined,
      ),
    );
    const dialog = await chooseFile();
    await within(dialog).findByText('The backup folder has too little free space.');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Close' }));
    await waitFor(() =>
      expect(
        calls.some((c) => c.method === 'DELETE' && c.path === '/api/v1/admin/backup-uploads/u1'),
      ).toBe(true),
    );
  });

  it('reloads the list once when cancelled while the server checks the file', async () => {
    const { calls } = mockFetch(
      api((call) => {
        if (call.path.endsWith('/complete')) {
          return new Promise<Response>((resolve) =>
            setTimeout(() => resolve(apiError(409, 'upload_busy', 'Busy.')), 200),
          );
        }
        if (call.method === 'DELETE') return new Response(null, { status: 204 });
        return undefined;
      }),
    );
    const dialog = await chooseFile();
    await within(dialog).findByText(/Checking the backup/);
    const listed = calls.filter((c) => c.path === '/api/v1/admin/backups').length;
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel upload' }));
    expect(await screen.findByText('Upload cancelled')).toBeInTheDocument();
    expect(
      screen.getByText(
        'The server was already checking the file, so it may still appear in the list.',
      ),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(calls.filter((c) => c.path === '/api/v1/admin/backups').length).toBe(listed + 1),
    );
  });
});
