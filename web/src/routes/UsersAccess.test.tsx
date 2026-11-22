// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import type { User } from '../api/client';
import { apiError, json, makeUser, mockFetch, noContent, renderApp } from '../test-utils';

const admin = makeUser({ id: 1, username: 'ada', role: 'admin' });

function user(overrides: Partial<User>): User {
  return {
    ...makeUser({ id: 2, username: 'grace', name: 'Grace', email: 'grace@example.com' }),
    role: 'viewer',
    ...overrides,
  };
}

describe('disable, enable and delete users', () => {
  it('disables after confirmation and shows the badge', async () => {
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
    fireEvent.click(await screen.findByRole('button', { name: 'Disable grace' }));
    const dialog = screen.getByRole('alertdialog', { name: 'Disable grace?' });
    expect(within(dialog).getByText(/cannot sign in, with a password or with/)).toBeInTheDocument();
    expect(calls.some((c) => c.method === 'PATCH')).toBe(false);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Disable' }));
    expect(await screen.findByText('grace is disabled and was signed out.')).toBeInTheDocument();
    expect(calls.find((c) => c.method === 'PATCH')?.body).toEqual({ disabled: true });
    const row = screen.getByRole('row', { name: /grace/ });
    expect(await within(row).findByText('Disabled')).toBeInTheDocument();
    // The badge is read as a separate word after the name.
    expect(screen.getByRole('row', { name: /^grace Disabled / })).toBe(row);
    expect(within(row).getByRole('button', { name: 'Enable grace' })).toBeInTheDocument();
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
    fireEvent.click(await screen.findByRole('button', { name: 'Enable grace' }));
    expect(await screen.findByText('grace can sign in again.')).toBeInTheDocument();
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
    fireEvent.click(await screen.findByRole('button', { name: 'Delete grace' }));
    const dialog = screen.getByRole('alertdialog', { name: 'Delete grace?' });
    expect(within(dialog).getByText(/comes back as a new viewer/)).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete user' }));
    await waitFor(() =>
      expect(screen.queryByRole('row', { name: /grace/ })).not.toBeInTheDocument(),
    );
    expect(calls.filter((c) => c.method === 'DELETE')).toHaveLength(1);
  });

  it('offers no access actions for your own account', async () => {
    mockFetch((call) => {
      if (call.path === '/api/v1/me') return json(200, admin);
      if (call.path.startsWith('/api/v1/users?'))
        return json(200, { items: [admin, user({})], next_after: null });
      return undefined;
    });
    renderApp('/admin/users');
    const row = await screen.findByRole('row', { name: /^ada / });
    expect(within(row).getByText('Your account')).toBeInTheDocument();
    expect(within(row).queryByRole('button', { name: /Disable|Delete/ })).not.toBeInTheDocument();
  });

  it('shows why the server refused', async () => {
    mockFetch((call) => {
      if (call.path === '/api/v1/me') return json(200, admin);
      if (call.path.startsWith('/api/v1/users?'))
        return json(200, { items: [admin, user({ role: 'admin' })], next_after: null });
      if (call.method === 'DELETE')
        return apiError(409, 'last_admin', 'At least one enabled administrator must remain.');
      return undefined;
    });
    renderApp('/admin/users');
    fireEvent.click(await screen.findByRole('button', { name: 'Delete grace' }));
    fireEvent.click(screen.getByRole('button', { name: 'Delete user' }));
    expect(
      await screen.findByText('At least one enabled administrator must remain.'),
    ).toBeInTheDocument();
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
  });
});
