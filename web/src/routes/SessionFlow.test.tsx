// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { screen, waitFor } from '@testing-library/react';
import { apiError, json, makeUser, mockFetch, renderApp, type Handler } from '../test-utils';
import type { Me } from '../api/client';

const enrollment = { secret: 'JBSWY3DPEHPK3PXP', otpauth_uri: 'otpauth://totp/Scanopsis:ada' };

function server(opts: { setup?: boolean; me?: Partial<Me> | null; extra?: Handler }): Handler {
  return async (call) => {
    const custom = await opts.extra?.(call);
    if (custom) return custom;
    if (call.path === '/api/v1/setup' && call.method === 'GET')
      return json(200, { required: opts.setup === true });
    if (call.path === '/api/v1/me') {
      return opts.me === null
        ? apiError(401, 'unauthenticated', 'Sign in first.')
        : json(200, makeUser(opts.me));
    }
    if (call.path === '/api/v1/auth/methods') return json(200, { gitlab: true, local: true });
    if (call.path === '/api/v1/auth/mfa/enrollment') return json(200, enrollment);
    if (call.path.startsWith('/api/v1/users?')) return json(200, { items: [], next_after: null });
    return undefined;
  };
}

const where = () => screen.getByTestId('location');

describe('session routing', () => {
  it.each(['/', '/admin/users', '/login', '/2fa', '/settings'])(
    'shows the setup page on %s while no administrator exists',
    async (path) => {
      mockFetch(server({ setup: true }));
      renderApp(path);
      expect(await screen.findByRole('heading', { name: 'Set up Scanopsis' })).toBeInTheDocument();
      expect(where()).toHaveTextContent(/^\/setup$/);
    },
  );

  it('sends the setup page to sign-in once setup is complete', async () => {
    mockFetch(server({ me: null }));
    renderApp('/setup');
    expect(await screen.findByRole('heading', { name: 'Sign in' })).toBeInTheDocument();
    expect(where()).toHaveTextContent(/^\/login$/);
  });

  it('sends a user who has not enrolled to the enrolment page and keeps the destination', async () => {
    mockFetch(server({ me: { mfa_enrolled: false, mfa_verified: false } }));
    renderApp('/admin/users');
    expect(
      await screen.findByRole('heading', { name: 'Set up two-factor sign-in' }),
    ).toBeInTheDocument();
    expect(where()).toHaveTextContent('/2fa/setup?return_to=%2Fadmin%2Fusers');
  });

  it('sends an enrolled but unverified user to the code page', async () => {
    mockFetch(server({ me: { mfa_verified: false } }));
    renderApp('/');
    expect(await screen.findByRole('heading', { name: 'Enter your code' })).toBeInTheDocument();
    expect(where()).toHaveTextContent(/^\/2fa$/);
  });

  it('sends a verified user with a temporary password to the password page', async () => {
    mockFetch(server({ me: { must_change_password: true } }));
    renderApp('/');
    expect(
      await screen.findByRole('heading', { name: 'Choose a new password' }),
    ).toBeInTheDocument();
    expect(where()).toHaveTextContent(/^\/password$/);
  });

  it('does not render or request application pages for an incomplete session', async () => {
    const { calls } = mockFetch(server({ me: { mfa_verified: false } }));
    renderApp('/admin/users');
    await screen.findByRole('heading', { name: 'Enter your code' });
    expect(screen.queryByRole('heading', { name: 'Users' })).not.toBeInTheDocument();
    expect(screen.queryByRole('navigation', { name: 'Main' })).not.toBeInTheDocument();
    expect(calls.some((c) => c.path.startsWith('/api/v1/users'))).toBe(false);
  });

  it('sends a complete session away from a step page', async () => {
    mockFetch(server({ me: {} }));
    renderApp('/2fa');
    expect(await screen.findByRole('heading', { name: 'Portfolio' })).toBeInTheDocument();
    expect(where()).toHaveTextContent(/^\/$/);
  });

  it('sends an anonymous visitor on a step page to sign-in', async () => {
    mockFetch(server({ me: null }));
    renderApp('/password');
    expect(await screen.findByRole('heading', { name: 'Sign in' })).toBeInTheDocument();
    expect(where()).toHaveTextContent('/login?return_to=%2Fpassword');
  });

  it.each([
    ['mfa_required', { mfa_verified: false }, 'Enter your code', '/2fa'],
    [
      'mfa_enrollment_required',
      { mfa_enrolled: false, mfa_verified: false },
      'Set up two-factor sign-in',
      '/2fa/setup',
    ],
    [
      'password_change_required',
      { must_change_password: true },
      'Choose a new password',
      '/password',
    ],
  ])('routes a 403 with %s to the right step', async (code, after, heading, path) => {
    let changed = false;
    mockFetch(
      server({
        me: {},
        extra: (call) => {
          if (call.path === '/api/v1/me' && changed) return json(200, makeUser(after));
          if (call.path.startsWith('/api/v1/users?')) {
            changed = true;
            return apiError(403, code, 'Finish signing in first.');
          }
          return undefined;
        },
      }),
    );
    renderApp('/admin/users');
    expect(await screen.findByRole('heading', { name: heading })).toBeInTheDocument();
    await waitFor(() => expect(where()).toHaveTextContent(path));
  });
});
