// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { formatRelative } from './UsersPage';
import {
  apiError,
  chooseOption,
  json,
  makeUser,
  mockFetch,
  openMenu,
  renderApp,
  type Handler,
} from '../test-utils';

// Relative to the clock, so the rendered text does not change with the date.
const admin = makeUser({
  id: 1,
  username: 'ada',
  role: 'admin',
  last_login_at: new Date(Date.now() - 3 * 3600_000).toISOString(),
});
const grace = makeUser({
  id: 2,
  username: 'grace',
  name: 'Grace Hopper',
  email: 'grace@example.com',
  role: 'viewer',
  source: 'local',
  last_login_at: null,
  mfa_enrolled: false,
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

function row(name: RegExp) {
  return screen.getByRole('row', { name });
}

describe('users page', () => {
  it('renders a row per person with sign-in method, role, two-factor and last sign-in', async () => {
    mockFetch(usersApi());
    renderApp('/admin/users');
    const table = await screen.findByRole('table', { name: 'Users' });
    expect(
      within(table)
        .getAllByRole('columnheader')
        .map((h) => h.textContent),
    ).toEqual(['Person', 'Sign-in', 'Role', 'Two-factor', 'Last sign-in', 'Actions']);
    // No header is set in capitals; the words are written in sentence case.
    for (const header of within(table).getAllByRole('columnheader')) {
      expect(header.className).not.toMatch(/uppercase/);
    }

    const graceRow = row(/Grace Hopper/);
    expect(within(graceRow).getByText('grace')).toBeInTheDocument();
    expect(within(graceRow).getByText('grace@example.com')).toBeInTheDocument();
    expect(within(graceRow).getByText('Local')).toBeInTheDocument();
    expect(within(graceRow).getByText('Never')).toBeInTheDocument();
    expect(within(graceRow).getByText('Not set up')).toBeInTheDocument();
    expect(within(graceRow).getByText('GH')).toHaveAttribute('aria-hidden', 'true');
    expect(within(graceRow).getByRole('combobox', { name: 'Role for grace' })).toHaveTextContent(
      'Viewer',
    );

    const adaRow = row(/Ada Lovelace/);
    expect(within(adaRow).getByText('you')).toBeInTheDocument();
    expect(within(adaRow).getByText('GitLab')).toBeInTheDocument();
    expect(within(adaRow).getByText('On')).toBeInTheDocument();
    const time = within(adaRow).getByText('3 hours ago');
    expect(time.tagName).toBe('TIME');
    expect(time).toHaveAttribute('datetime', admin.last_login_at);
    expect(screen.getByText('2 people')).toBeInTheDocument();
  });

  it('filters by admins, people without two-factor and disabled people', async () => {
    const dave = makeUser({
      id: 3,
      username: 'dave',
      name: 'Dave',
      role: 'viewer',
      disabled_at: '2026-11-20T10:00:00Z',
    });
    mockFetch(
      usersApi((call) =>
        call.path.startsWith('/api/v1/users?')
          ? json(200, { items: [admin, grace, dave], next_after: null })
          : undefined,
      ),
    );
    renderApp('/admin/users');
    await screen.findByRole('table', { name: 'Users' });
    const filters = screen.getByRole('group', { name: 'Filter users' });
    const chip = (name: RegExp) => within(filters).getByRole('button', { name });

    expect(chip(/^Admins/)).toHaveTextContent('Admins1');
    expect(chip(/^Without two-factor/)).toHaveTextContent('1');
    expect(chip(/^Disabled/)).toHaveTextContent('1');

    fireEvent.click(chip(/^Admins/));
    expect(chip(/^Admins/)).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getAllByRole('row')).toHaveLength(2);
    expect(row(/Ada Lovelace/)).toBeInTheDocument();

    fireEvent.click(chip(/^Admins/));
    fireEvent.click(chip(/^Without two-factor/));
    expect(screen.getAllByRole('row')).toHaveLength(2);
    expect(row(/Grace Hopper/)).toBeInTheDocument();

    // Filters combine: nobody is both without two-factor and disabled.
    fireEvent.click(chip(/^Disabled/));
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
    expect(screen.getByText('Nobody matches these filters')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Clear filters' }));
    expect(screen.getAllByRole('row')).toHaveLength(4);
  });

  it('sends a PATCH with the new role and confirms with a toast', async () => {
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
    expect(await screen.findByLabelText('Role for grace')).toHaveTextContent('Viewer');
    await chooseOption('Role for grace', 'Admin');
    expect(await screen.findByText('Role changed')).toBeInTheDocument();
    expect(screen.getByText('Grace Hopper is now an admin.')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByLabelText('Role for grace')).toHaveTextContent('Admin'));
    expect(screen.getByLabelText('Role for grace')).toBeEnabled();
    expect(calls.find((c) => c.method === 'PATCH')).toMatchObject({
      path: '/api/v1/users/2',
      body: { role: 'admin' },
    });
  });

  it('changes the role from the row menu', async () => {
    const { calls } = mockFetch(
      usersApi((call) =>
        call.method === 'PATCH' ? json(200, { ...grace, role: 'admin' }) : undefined,
      ),
    );
    renderApp('/admin/users');
    const menu = await openMenu('Actions for grace');
    expect(within(menu).queryByRole('menuitem', { name: 'Make viewer' })).not.toBeInTheDocument();
    fireEvent.click(within(menu).getByRole('menuitem', { name: 'Make admin' }));
    expect(await screen.findByText('Role changed')).toBeInTheDocument();
    expect(calls.find((c) => c.method === 'PATCH')?.body).toEqual({ role: 'admin' });
  });

  it('shows the requested role and disables the control while saving', async () => {
    let release: (r: Response) => void = () => {};
    mockFetch(
      usersApi((call) =>
        call.method === 'PATCH'
          ? new Promise<Response>((resolve) => (release = resolve))
          : undefined,
      ),
    );
    renderApp('/admin/users');
    await screen.findByLabelText('Role for grace');
    await chooseOption('Role for grace', 'Admin');
    await waitFor(() => expect(screen.getByLabelText('Role for grace')).toBeDisabled());
    expect(screen.getByLabelText('Role for grace')).toHaveTextContent('Admin');
    release(json(200, { ...grace, role: 'admin' }));
    expect(await screen.findByText('Role changed')).toBeInTheDocument();
  });

  it('reports a refused role change in a toast and keeps the saved role', async () => {
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
    await screen.findByLabelText('Role for ada');
    await chooseOption('Role for ada', 'Viewer');
    expect(await screen.findByText('The role could not be changed')).toBeInTheDocument();
    expect(
      screen.getByText(
        'You cannot remove the last administrator. Make another user an admin first.',
      ),
    ).toBeInTheDocument();
    expect(screen.getByLabelText('Role for ada')).toHaveTextContent('Admin');
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
    expect(await screen.findByText('Showing 2')).toBeInTheDocument();
    expect(screen.queryByText('Linus')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
    expect(await screen.findByText('Linus')).toBeInTheDocument();
    expect(screen.getByText('Grace Hopper')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Load more' })).not.toBeInTheDocument();
    expect(calls.map((c) => c.path)).toContain('/api/v1/users?after=2');
  });

  it('shows the server message and a retry when the list cannot be loaded', async () => {
    mockFetch((call) => {
      if (call.path === '/api/v1/me') return json(200, admin);
      return apiError(500, 'internal', 'The user list could not be read.');
    });
    renderApp('/admin/users');
    expect(await screen.findByText('Users could not be loaded')).toBeVisible();
    expect(screen.getByText(/The user list could not be read\./)).toBeVisible();
    expect(screen.getByRole('button', { name: 'Try again' })).toBeInTheDocument();
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
