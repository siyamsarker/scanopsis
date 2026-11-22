// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import type { User } from '../api/client';
import { apiError, json, makeUser, mockFetch, noContent, openMenu, renderApp } from '../test-utils';

const admin = makeUser({ id: 1, username: 'ada', role: 'admin' });

function user(overrides: Partial<User>): User {
  return {
    ...makeUser({ id: 2, username: 'grace', name: 'Grace', email: 'grace@example.com' }),
    role: 'viewer',
    ...overrides,
  };
}

/** Opens the row menu of a user and chooses an item. */
async function act(username: string, item: string) {
  const menu = await openMenu(`Actions for ${username}`);
  fireEvent.click(within(menu).getByRole('menuitem', { name: item }));
}

describe('disable, enable and delete users', () => {
  it('disables after a danger confirmation and shows the badge', async () => {
    let grace = user({});
    const { calls } = mockFetch((call) => {
      if (call.path === '/api/v1/me') return json(200, admin);
      if (call.path.startsWith('/api/v1/users?'))
        return json(200, { items: [admin, grace], next_after: null });
      if (call.method === 'PATCH' && call.path === '/api/v1/users/2') {
        grace = user({ disabled_at: '2026-11-20T10:00:00Z' });
        return json(200, grace);
      }
      return undefined;
    });
    renderApp('/admin/users');
    await act('grace', 'Disable user');
    const dialog = screen.getByRole('alertdialog', { name: 'Disable Grace?' });
    expect(within(dialog).getByText(/cannot sign in, with a password or with/)).toBeInTheDocument();
    expect(calls.some((c) => c.method === 'PATCH')).toBe(false);
    const confirm = within(dialog).getByRole('button', { name: 'Disable user' });
    expect(confirm.className).toMatch(/bg-fail/);
    fireEvent.click(confirm);
    expect(await screen.findByText('User disabled')).toBeInTheDocument();
    expect(
      screen.getByText('Grace was signed out everywhere and cannot sign in.'),
    ).toBeInTheDocument();
    expect(calls.find((c) => c.method === 'PATCH')?.body).toEqual({ disabled: true });
    const row = screen.getByRole('row', { name: /Grace/ });
    expect(await within(row).findByText('Disabled')).toBeInTheDocument();
    const menu = await openMenu('Actions for grace');
    expect(within(menu).getByRole('menuitem', { name: 'Enable user' })).toBeInTheDocument();
  });

  it('enables without a confirmation', async () => {
    const { calls } = mockFetch((call) => {
      if (call.path === '/api/v1/me') return json(200, admin);
      if (call.path.startsWith('/api/v1/users?'))
        return json(200, {
          items: [admin, user({ disabled_at: '2026-11-20T10:00:00Z' })],
          next_after: null,
        });
      if (call.method === 'PATCH') return json(200, user({}));
      return undefined;
    });
    renderApp('/admin/users');
    await act('grace', 'Enable user');
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
    expect(await screen.findByText('User enabled')).toBeInTheDocument();
    expect(screen.getByText('Grace can sign in again.')).toBeInTheDocument();
    expect(calls.find((c) => c.method === 'PATCH')?.body).toEqual({ disabled: false });
  });

  it('deletes after confirmation and warns about GitLab users', async () => {
    let items = [admin, user({})];
    const { calls } = mockFetch((call) => {
      if (call.path === '/api/v1/me') return json(200, admin);
      if (call.path.startsWith('/api/v1/users?')) return json(200, { items, next_after: null });
      if (call.method === 'DELETE' && call.path === '/api/v1/users/2') {
        items = [admin];
        return noContent();
      }
      return undefined;
    });
    renderApp('/admin/users');
    await act('grace', 'Delete user');
    const dialog = screen.getByRole('alertdialog', { name: 'Delete Grace?' });
    expect(within(dialog).getByText(/comes back as a new viewer/)).toBeInTheDocument();
    expect(within(dialog).getByText(/The audit log keeps their entries/)).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete user' }));
    expect(await screen.findByText('User deleted')).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.queryByRole('row', { name: /Grace/ })).not.toBeInTheDocument(),
    );
    expect(calls.filter((c) => c.method === 'DELETE')).toHaveLength(1);
  });

  it('does not warn about returning for local users', async () => {
    mockFetch((call) => {
      if (call.path === '/api/v1/me') return json(200, admin);
      if (call.path.startsWith('/api/v1/users?'))
        return json(200, { items: [admin, user({ source: 'local' })], next_after: null });
      return undefined;
    });
    renderApp('/admin/users');
    await act('grace', 'Delete user');
    const dialog = screen.getByRole('alertdialog', { name: 'Delete Grace?' });
    expect(within(dialog).queryByText(/comes back as a new viewer/)).not.toBeInTheDocument();
  });

  it('offers no disable or delete for your own account', async () => {
    mockFetch((call) => {
      if (call.path === '/api/v1/me') return json(200, admin);
      if (call.path.startsWith('/api/v1/users?'))
        return json(200, { items: [admin, user({})], next_after: null });
      return undefined;
    });
    renderApp('/admin/users');
    const menu = await openMenu('Actions for ada');
    expect(
      within(menu).getByText('You cannot disable or delete your own account.'),
    ).toBeInTheDocument();
    expect(within(menu).getByRole('menuitem', { name: 'Disable user' })).toHaveAttribute(
      'aria-disabled',
      'true',
    );
    expect(within(menu).getByRole('menuitem', { name: 'Delete user' })).toHaveAttribute(
      'aria-disabled',
      'true',
    );
    // Another person's menu offers both.
    fireEvent.keyDown(menu, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('menu')).not.toBeInTheDocument());
    const other = await openMenu('Actions for grace');
    expect(within(other).getByRole('menuitem', { name: 'Delete user' })).not.toHaveAttribute(
      'aria-disabled',
    );
    expect(
      within(other).queryByText('You cannot disable or delete your own account.'),
    ).not.toBeInTheDocument();
  });

  it('shows why the server refused in a toast', async () => {
    mockFetch((call) => {
      if (call.path === '/api/v1/me') return json(200, admin);
      if (call.path.startsWith('/api/v1/users?'))
        return json(200, { items: [admin, user({ role: 'admin' })], next_after: null });
      if (call.method === 'DELETE')
        return apiError(409, 'last_admin', 'At least one enabled administrator must remain.');
      return undefined;
    });
    renderApp('/admin/users');
    await act('grace', 'Delete user');
    fireEvent.click(
      within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Delete user' }),
    );
    expect(await screen.findByText('Grace could not be deleted')).toBeInTheDocument();
    expect(screen.getByText('At least one enabled administrator must remain.')).toBeInTheDocument();
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
    expect(screen.getByRole('row', { name: /Grace/ })).toBeInTheDocument();
  });
});
