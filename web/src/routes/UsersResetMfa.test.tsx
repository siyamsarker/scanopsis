// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { fireEvent, screen } from '@testing-library/react';
import {
  apiError,
  json,
  makeUser,
  mockFetch,
  noContent,
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

describe('reset two-factor', () => {
  it('asks for confirmation, then resets and says so', async () => {
    const { calls } = mockFetch(
      api((call) => (call.method === 'DELETE' ? noContent() : undefined)),
    );
    renderApp('/admin/users');
    fireEvent.click(await screen.findByRole('button', { name: 'Reset two-factor for grace' }));
    const dialog = screen.getByRole('alertdialog', { name: 'Reset two-factor for grace?' });
    expect(dialog).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Cancel' })).toHaveFocus();
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false);
    fireEvent.click(screen.getAllByRole('button', { name: 'Reset two-factor' })[0]);
    expect(
      await screen.findByText('Two-factor reset. grace sets it up again at the next sign-in.'),
    ).toBeInTheDocument();
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
    expect(calls.find((c) => c.method === 'DELETE')?.path).toBe('/api/v1/users/2/mfa');
    expect(screen.getByRole('button', { name: 'Reset two-factor for grace' })).toHaveFocus();
  });

  it('cancels with the button and with Escape without sending anything', async () => {
    const { calls } = mockFetch(api());
    renderApp('/admin/users');
    fireEvent.click(await screen.findByRole('button', { name: 'Reset two-factor for grace' }));
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Reset two-factor for grace' }));
    fireEvent.keyDown(screen.getByRole('alertdialog'), { key: 'Escape' });
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false);
  });

  it('shows the server message when the reset fails', async () => {
    mockFetch(
      api((call) =>
        call.method === 'DELETE' ? apiError(404, 'not_found', 'No such user.') : undefined,
      ),
    );
    renderApp('/admin/users');
    fireEvent.click(await screen.findByRole('button', { name: 'Reset two-factor for grace' }));
    fireEvent.click(screen.getAllByRole('button', { name: 'Reset two-factor' })[0]);
    expect(await screen.findByText('No such user.')).toBeInTheDocument();
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
    fireEvent.click(await screen.findByRole('button', { name: 'Reset two-factor for ada' }));
    expect(screen.getByText(/you will be signed out now/)).toBeInTheDocument();
    fireEvent.click(screen.getAllByRole('button', { name: 'Reset two-factor' })[0]);
    expect(await screen.findByRole('heading', { name: 'Sign in' })).toBeInTheDocument();
    expect(screen.getByTestId('location')).toHaveTextContent(/^\/login$/);
  });
});
