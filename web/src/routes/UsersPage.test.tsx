// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { fireEvent, screen, within } from '@testing-library/react';
import { formatRelative } from './UsersPage';
import { apiError, json, makeUser, mockFetch, renderApp, type Handler } from '../test-utils';

const admin = makeUser({ id: 1, username: 'ada', role: 'admin' });
const grace = makeUser({
  id: 2,
  username: 'grace',
  name: 'Grace Hopper',
  email: 'grace@example.com',
  role: 'viewer',
  source: 'local',
  last_login_at: null,
});

function usersApi(extra?: Handler): Handler {
  return async (call) => {
    const custom = await extra?.(call);
    if (custom) return custom;
    if (call.path === '/api/v1/me') return json(200, admin);
    if (call.path.startsWith('/api/v1/users?')) {
      return json(200, { items: [admin, grace], next_after: null });
    }
    return undefined;
  };
}

describe('users page', () => {
  it('renders a row per user with sign-in method and Never for no sign-in', async () => {
    mockFetch(usersApi());
    renderApp('/admin/users');
    const table = await screen.findByRole('table');
    expect(
      within(table)
        .getAllByRole('columnheader')
        .map((h) => h.textContent),
    ).toEqual(['Username', 'Name', 'E-mail', 'Sign-in', 'Role', 'Last sign-in']);
    const row = within(table).getByRole('row', { name: /grace/ });
    expect(within(row).getByText('Grace Hopper')).toBeInTheDocument();
    expect(within(row).getByText('grace@example.com')).toBeInTheDocument();
    expect(within(row).getByText('Local')).toBeInTheDocument();
    expect(within(row).getByText('Never')).toBeInTheDocument();
    const adminRow = within(table).getByRole('row', { name: /ada/ });
    expect(within(adminRow).getByText('GitLab')).toBeInTheDocument();
    expect(within(adminRow).getByText(/ago$/)).toHaveAttribute(
      'title',
      new Date(admin.last_login_at!).toLocaleString(),
    );
  });

  it('sends a PATCH with the new role and shows the result', async () => {
    let saved = false;
    const { calls } = mockFetch(
      usersApi((call) => {
        if (call.method === 'PATCH' && call.path === '/api/v1/users/2') {
          saved = true;
          return json(200, { ...grace, role: 'admin' });
        }
        if (saved && call.path.startsWith('/api/v1/users?')) {
          return json(200, { items: [admin, { ...grace, role: 'admin' }], next_after: null });
        }
        return undefined;
      }),
    );
    renderApp('/admin/users');
    const select = await screen.findByLabelText('Role for grace');
    expect(select).toHaveValue('viewer');
    fireEvent.change(select, { target: { value: 'admin' } });
    expect(await screen.findByText('Role changed to Admin.')).toBeInTheDocument();
    expect(screen.getByLabelText('Role for grace')).toHaveValue('admin');
    expect(screen.getByLabelText('Role for grace')).toBeEnabled();
    expect(calls.find((c) => c.method === 'PATCH')).toMatchObject({
      path: '/api/v1/users/2',
      body: { role: 'admin' },
    });
  });

  it('disables the control while saving', async () => {
    let release: (r: Response) => void = () => {};
    mockFetch(
      usersApi((call) =>
        call.method === 'PATCH'
          ? new Promise<Response>((resolve) => (release = resolve))
          : undefined,
      ),
    );
    renderApp('/admin/users');
    const select = await screen.findByLabelText('Role for grace');
    fireEvent.change(select, { target: { value: 'admin' } });
    expect(await screen.findByText('Saving…')).toBeInTheDocument();
    expect(screen.getByLabelText('Role for grace')).toBeDisabled();
    release(json(200, { ...grace, role: 'admin' }));
    expect(await screen.findByText('Role changed to Admin.')).toBeInTheDocument();
  });

  it('shows the server message inline on a 409 and keeps the saved role', async () => {
    mockFetch(
      usersApi((call) =>
        call.method === 'PATCH'
          ? apiError(
              409,
              'last_admin',
              'You cannot remove the last administrator. Make another user an admin first.',
            )
          : undefined,
      ),
    );
    renderApp('/admin/users');
    const select = await screen.findByLabelText('Role for ada');
    fireEvent.change(select, { target: { value: 'viewer' } });
    expect(
      await screen.findByText(
        'You cannot remove the last administrator. Make another user an admin first.',
      ),
    ).toBeInTheDocument();
    expect(screen.getByLabelText('Role for ada')).toHaveValue('admin');
    expect(screen.getByLabelText('Role for ada')).toBeEnabled();
  });

  it('tells viewers they need the admin role without requesting users', async () => {
    const { calls } = mockFetch((call) =>
      call.path === '/api/v1/me' ? json(200, makeUser({ role: 'viewer' })) : undefined,
    );
    renderApp('/admin/users');
    expect(await screen.findByText('You need the admin role to see this page.')).toBeVisible();
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
    expect(calls.some((c) => c.path.startsWith('/api/v1/users'))).toBe(false);
  });

  it('loads more users with next_after', async () => {
    const third = makeUser({ id: 3, username: 'linus', name: 'Linus' });
    const { calls } = mockFetch(async (call) => {
      if (call.path === '/api/v1/me') return json(200, admin);
      if (call.path === '/api/v1/users?after=0') {
        return json(200, { items: [admin, grace], next_after: 2 });
      }
      if (call.path === '/api/v1/users?after=2') {
        return json(200, { items: [third], next_after: null });
      }
      return undefined;
    });
    renderApp('/admin/users');
    expect(screen.queryByText('linus')).not.toBeInTheDocument();
    fireEvent.click(await screen.findByRole('button', { name: 'Load more' }));
    expect(await screen.findByText('linus')).toBeInTheDocument();
    expect(screen.getByText('grace')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Load more' })).not.toBeInTheDocument();
    expect(calls.map((c) => c.path)).toContain('/api/v1/users?after=2');
  });

  it('shows the server message when the list cannot be loaded', async () => {
    mockFetch((call) => {
      if (call.path === '/api/v1/me') return json(200, admin);
      return apiError(500, 'internal', 'The user list could not be read.');
    });
    renderApp('/admin/users');
    expect(await screen.findByText(/The user list could not be read\./)).toBeVisible();
  });
});

describe('formatRelative', () => {
  const now = Date.parse('2026-11-19T12:00:00Z');
  it.each([
    ['2026-11-19T11:59:58Z', 'just now'],
    ['2026-11-19T11:58:00Z', '2 minutes ago'],
    ['2026-11-19T09:00:00Z', '3 hours ago'],
    ['2026-11-18T12:00:00Z', 'yesterday'],
    ['2026-11-12T12:00:00Z', '7 days ago'],
    ['2025-11-19T12:00:00Z', 'last year'],
  ])('formats %s as %s', (iso, expected) => {
    expect(formatRelative(iso, now)).toBe(expected);
  });
});
