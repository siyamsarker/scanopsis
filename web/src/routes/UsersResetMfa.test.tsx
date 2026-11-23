// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import {
  apiError,
  json,
  makeUser,
  mockFetch,
  noContent,
  openMenu,
  renderApp,
  type Handler,
} from '../test-utils';

const admin = makeUser({ id: 1, username: 'ada', role: 'admin' });
const grace = makeUser({
  id: 2,
  username: 'grace',
  name: 'Grace',
  role: 'viewer',
  source: 'local',
});

function api(extra?: Handler): Handler {
  return async (call) => {
    const custom = await extra?.(call);
    if (custom) return custom;
    if (call.path === '/api/v1/setup') return json(200, { required: false });
    if (call.path === '/api/v1/me') return json(200, admin);
    if (call.path.startsWith('/api/v1/users?'))
      return json(200, { items: [admin, grace], next_after: null });
    if (call.path === '/api/v1/auth/methods') return json(200, { gitlab: true, local: true });
    return undefined;
  };
}

/** Opens the row menu and asks for a two-factor reset. */
async function askReset(username: string) {
  const menu = await openMenu(`Actions for ${username}`);
  fireEvent.click(within(menu).getByRole('menuitem', { name: 'Reset two-factor' }));
  return screen.findByRole('alertdialog');
}

describe('reset two-factor', () => {
  it('asks for confirmation, then resets and says so', async () => {
    const { calls } = mockFetch(
      api((call) => (call.method === 'DELETE' ? noContent() : undefined)),
    );
    renderApp('/admin/users');
    const dialog = await askReset('grace');
    expect(dialog).toHaveAccessibleName('Reset two-factor for Grace?');
    expect(within(dialog).getByText(/signs them out everywhere/)).toBeInTheDocument();
    expect(within(dialog).queryByText(/you will be signed out now/)).not.toBeInTheDocument();
    expect(within(dialog).getByRole('button', { name: 'Cancel' })).toHaveFocus();
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false);
    const confirm = within(dialog).getByRole('button', { name: 'Reset two-factor' });
    expect(confirm.className).toMatch(/bg-fail/);
    fireEvent.click(confirm);
    expect(await screen.findByText('Two-factor reset')).toBeInTheDocument();
    expect(screen.getByText('Grace sets it up again at the next sign-in.')).toBeInTheDocument();
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
    expect(calls.find((c) => c.method === 'DELETE')?.path).toBe('/api/v1/users/2/mfa');
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Actions for grace' })).toHaveFocus(),
    );
  });

  it('cancels with the button and with Escape without sending anything', async () => {
    const { calls } = mockFetch(api());
    renderApp('/admin/users');
    let dialog = await askReset('grace');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument());
    dialog = await askReset('grace');
    fireEvent.keyDown(dialog, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument());
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false);
  });

  it('shows the server message when the reset fails', async () => {
    mockFetch(
      api((call) =>
        call.method === 'DELETE' ? apiError(404, 'not_found', 'No such user.') : undefined,
      ),
    );
    renderApp('/admin/users');
    const dialog = await askReset('grace');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Reset two-factor' }));
    expect(await screen.findByText('Two-factor could not be reset')).toBeInTheDocument();
    expect(screen.getByText('No such user.')).toBeInTheDocument();
  });

  it('signs the admin out after resetting their own two-factor', async () => {
    let out = false;
    mockFetch(
      api((call) => {
        if (call.method === 'DELETE') {
          out = true;
          return noContent();
        }
        if (call.path === '/api/v1/me' && out)
          return apiError(401, 'unauthenticated', 'Sign in first.');
        return undefined;
      }),
    );
    renderApp('/admin/users');
    const dialog = await askReset('ada');
    expect(within(dialog).getByText(/you will be signed out now/)).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Reset two-factor' }));
    expect(await screen.findByRole('heading', { name: 'Sign in' })).toBeInTheDocument();
    expect(screen.getByTestId('location')).toHaveTextContent(/^\/login$/);
  });
});
