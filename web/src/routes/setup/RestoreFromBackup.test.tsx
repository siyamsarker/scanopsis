// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { configure, fireEvent, screen, waitFor, within } from '@testing-library/react';
import type { RestorePreview } from '../../api/backups';
import { hideRestoreOverlay } from '../../api/restoreMode';
import { apiError, json, mockFetch, renderApp, type Handler } from '../../test-utils';

configure({ asyncUtilTimeout: 5000 });

afterEach(() => hideRestoreOverlay());

const preview: RestorePreview = {
  info: {
    name: 'scanopsis-backup-20261101T020000Z-uploaded-abc123.tar.gz',
    kind: 'uploaded',
    size: 10,
    created_at: '2026-11-01T02:00:00Z',
    created_by: 'scheduler',
    app_version: '0.4.0',
    schema: 12,
    key_matches: true,
    readable: true,
    uploaded_at: '2026-11-23T12:00:00Z',
    original_name: 'old-host.tar.gz',
  },
  schema_ok: true,
  key_matches: true,
  scans_removed_by_retention: 0,
  oldest_scan_at: null,
  retention_days: 90,
};

const upload = { id: 'u1', file_name: 'old-host.tar.gz', size: 10, chunk_size: 4 };

interface World {
  required: boolean;
  start?: Handler;
  complete?: Handler;
  restore?: Handler;
  restoreStatus?: Handler;
}

function api(world: World): Handler {
  return async (call) => {
    if (call.path === '/api/v1/setup' && call.method === 'GET') {
      return json(200, { required: world.required });
    }
    if (call.path === '/api/v1/me') return apiError(401, 'unauthenticated', 'Sign in first.');
    if (call.path === '/api/v1/auth/methods') return json(200, { gitlab: false, local: true });
    if (call.path === '/api/v1/setup/restore/uploads' && call.method === 'POST') {
      return world.start ? world.start(call) : json(201, { ...upload, received: 0 });
    }
    const part = /^\/api\/v1\/setup\/restore\/uploads\/u1\?offset=(\d+)$/.exec(call.path);
    if (part && call.method === 'PUT') {
      return json(200, { ...upload, received: Math.min(10, Number(part[1]) + 4) });
    }
    if (call.path === '/api/v1/setup/restore/uploads/u1/complete') {
      return world.complete ? world.complete(call) : json(201, preview);
    }
    if (call.path === '/api/v1/setup/restore' && call.method === 'POST') {
      return world.restore?.(call) ?? json(202, status('restoring'));
    }
    if (call.path === '/api/v1/restore/status') {
      return world.restoreStatus?.(call) ?? json(200, status('restoring'));
    }
    return undefined;
  };
}

function status(state: string) {
  return {
    state,
    percent: state === 'done' ? 100 : 30,
    message:
      state === 'done'
        ? 'Restore finished. Sign in with an account from the backup.'
        : 'Restoring a backup.',
    finished_at: null,
    restart_required: false,
  };
}

async function openRestore() {
  fireEvent.click(await screen.findByRole('button', { name: /Restore from a backup/ }));
  await screen.findByRole('heading', { name: 'Restore from a backup' });
}

function enterToken(token = 'tok-123') {
  fireEvent.change(screen.getByLabelText('Setup token'), { target: { value: token } });
  fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
}

async function sendFile() {
  await screen.findByRole('heading', { name: 'Choose the backup file' });
  fireEvent.change(screen.getByLabelText('Backup file'), {
    target: { files: [new File(['1234567890'], 'old-host.tar.gz')] },
  });
}

describe('restore from the setup screen', () => {
  it('offers both ways to start', async () => {
    mockFetch(api({ required: true }));
    renderApp('/setup');
    expect(await screen.findByRole('button', { name: /Set up a new installation/ })).toBeVisible();
    const restore = screen.getByRole('button', { name: /Restore from a backup/ });
    expect(restore).toHaveTextContent(
      'Moving Scanopsis to a new host? Upload a backup from the old one. Use the same secret key file as the old installation.',
    );
  });

  it('sends the token with every upload request and goes back to it when it is wrong', async () => {
    const { calls } = mockFetch(
      api({
        required: true,
        start: () => apiError(403, 'invalid_setup_token', 'Wrong token.'),
      }),
    );
    renderApp('/setup');
    await openRestore();
    enterToken('nope');
    await sendFile();
    expect(
      await screen.findByText('The setup token is not correct. Copy it again from the server log.'),
    ).toBeVisible();
    expect(screen.getByLabelText('Setup token')).toHaveValue('nope');
    const start = calls.find((c) => c.path === '/api/v1/setup/restore/uploads');
    expect(start?.body).toEqual({ token: 'nope', file_name: 'old-host.tar.gz', size: 10 });
  });

  it('says how long to wait after too many wrong tokens', async () => {
    mockFetch(
      api({
        required: true,
        start: () => apiError(429, 'too_many_attempts', 'Slow down.', { 'Retry-After': '30' }),
      }),
    );
    renderApp('/setup');
    await openRestore();
    enterToken();
    await sendFile();
    expect(await screen.findByText('Too many attempts. Try again in 30 seconds.')).toBeVisible();
  });

  it('blocks a backup made with another secret key', async () => {
    mockFetch(
      api({
        required: true,
        complete: () =>
          json(201, {
            ...preview,
            key_matches: false,
            scans_removed_by_retention: 0,
            info: { ...preview.info, key_matches: false },
          }),
      }),
    );
    renderApp('/setup');
    await openRestore();
    enterToken();
    await sendFile();
    expect(
      await screen.findByText(
        'This backup was made with another secret key. Copy secrets/secret_key from the old installation and restart Scanopsis.',
      ),
    ).toBeVisible();
    expect(screen.queryByRole('button', { name: 'Restore backup' })).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/Type RESTORE/)).not.toBeInTheDocument();
    expect(screen.queryByText(/scans? older than/)).not.toBeInTheDocument();
    expect(screen.queryByText(/0 scans/)).not.toBeInTheDocument();
  });

  it('blocks a backup from a newer version and says to install it', async () => {
    mockFetch(
      api({
        required: true,
        complete: () =>
          json(201, {
            ...preview,
            schema_ok: false,
            info: { ...preview.info, app_version: '0.9.0' },
          }),
      }),
    );
    renderApp('/setup');
    await openRestore();
    enterToken();
    await sendFile();
    expect(
      await screen.findByText(
        'This backup was made by a newer version of Scanopsis (0.9.0). Install that version first, then restore it.',
      ),
    ).toBeVisible();
    expect(screen.queryByRole('button', { name: 'Restore backup' })).not.toBeInTheDocument();
  });

  it('warns about scans the clean-up removes, at most', async () => {
    mockFetch(
      api({
        required: true,
        complete: () => json(201, { ...preview, scans_removed_by_retention: 12 }),
      }),
    );
    renderApp('/setup');
    await openRestore();
    enterToken();
    await sendFile();
    expect(await screen.findByText(/removes up to 12 scans older than 90 days/)).toBeVisible();
  });

  it('uploads, previews, confirms with RESTORE and ends at the sign-in button', async () => {
    const world: World = { required: true };
    world.restore = () => {
      world.required = false;
      return json(202, status('restoring'));
    };
    let polls = 0;
    world.restoreStatus = () => json(200, status(++polls > 1 ? 'done' : 'restoring'));
    const { calls } = mockFetch(api(world));
    renderApp('/setup');
    await openRestore();
    enterToken('tok-123');
    await sendFile();

    expect(await screen.findByRole('heading', { name: 'Review the backup' })).toBeVisible();
    expect(screen.getByText('0.4.0')).toBeVisible();
    expect(screen.getByText('The same secret key as this installation')).toBeVisible();
    expect(
      screen.getByText(
        'Scanopsis takes over everything in this backup: users, settings, services, scans and the audit log.',
      ),
    ).toBeVisible();
    expect(screen.getByText(/Pipeline uploads are turned away/)).toBeVisible();
    expect(screen.queryByText(/backs up the current state/)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/Two-factor code/)).not.toBeInTheDocument();

    const restore = screen.getByRole('button', { name: 'Restore backup' });
    expect(restore).toBeDisabled();
    fireEvent.change(screen.getByLabelText('Type RESTORE to confirm'), {
      target: { value: 'restore' },
    });
    expect(restore).toBeDisabled();
    fireEvent.change(screen.getByLabelText('Type RESTORE to confirm'), {
      target: { value: 'RESTORE' },
    });
    expect(restore).toBeEnabled();
    fireEvent.click(restore);

    const overlay = await screen.findByRole('alertdialog', { name: 'Restoring a backup' });
    expect(calls.find((c) => c.path === '/api/v1/setup/restore')?.body).toEqual({
      token: 'tok-123',
      upload_id: 'u1',
      confirm: 'RESTORE',
    });
    fireEvent.click(await within(overlay).findByRole('button', { name: 'Go to sign-in' }));
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument());
    expect(await screen.findByRole('heading', { name: 'Sign in' })).toBeVisible();
  });

  it('points to sign-in when an administrator exists by now', async () => {
    mockFetch(
      api({
        required: true,
        start: () => apiError(409, 'setup_done', 'Setup is done.'),
      }),
    );
    renderApp('/setup');
    await openRestore();
    enterToken();
    await sendFile();
    expect(
      await screen.findByText('This installation is already set up. Sign in instead.'),
    ).toBeVisible();
    fireEvent.click(screen.getByRole('link', { name: 'Go to sign-in' }));
    expect(await screen.findByRole('heading', { name: 'Sign in' })).toBeVisible();
  });

  it('goes back to the two choices', async () => {
    mockFetch(api({ required: true }));
    renderApp('/setup');
    await openRestore();
    fireEvent.click(screen.getByRole('button', { name: 'Back' }));
    expect(await screen.findByRole('button', { name: /Set up a new installation/ })).toBeVisible();
  });
});
