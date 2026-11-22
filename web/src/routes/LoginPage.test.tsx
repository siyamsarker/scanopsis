// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { fireEvent, screen } from '@testing-library/react';
import { createQueryClient } from '../api/queryClient';
import {
  apiError,
  json,
  makeUser,
  mockFetch,
  noContent,
  renderApp,
  type Handler,
} from '../test-utils';

function anonymous(methods = { gitlab: true, local: true }, extra?: Handler): Handler {
  return async (call) => {
    const custom = await extra?.(call);
    if (custom) return custom;
    if (call.path === '/api/v1/me') return apiError(401, 'unauthenticated', 'Sign in first.');
    if (call.path === '/api/v1/auth/methods') return json(200, methods);
    return undefined;
  };
}

function fillAndSubmit(username = 'admin', password = 'secret') {
  fireEvent.change(screen.getByLabelText('Username'), { target: { value: username } });
  fireEvent.change(screen.getByLabelText('Password'), { target: { value: password } });
  fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
}

describe('login page methods', () => {
  it('shows both methods when both are enabled', async () => {
    mockFetch(anonymous());
    renderApp('/login');
    expect(await screen.findByRole('link', { name: 'Sign in with GitLab' })).toBeInTheDocument();
    expect(screen.getByLabelText('Username')).toBeInTheDocument();
    expect(screen.getByLabelText('Password')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeInTheDocument();
  });

  it('shows only GitLab when local sign-in is disabled', async () => {
    mockFetch(anonymous({ gitlab: true, local: false }));
    renderApp('/login');
    expect(await screen.findByRole('link', { name: 'Sign in with GitLab' })).toBeInTheDocument();
    expect(screen.queryByLabelText('Username')).not.toBeInTheDocument();
  });

  it('shows only the local form when GitLab is disabled', async () => {
    mockFetch(anonymous({ gitlab: false, local: true }));
    renderApp('/login');
    expect(await screen.findByLabelText('Username')).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'Sign in with GitLab' })).not.toBeInTheDocument();
  });

  it('explains when no method is enabled', async () => {
    mockFetch(anonymous({ gitlab: false, local: false }));
    renderApp('/login');
    expect(await screen.findByText(/No sign-in method is enabled/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Sign in' })).not.toBeInTheDocument();
  });

  it('encodes return_to in the GitLab link', async () => {
    mockFetch(anonymous());
    renderApp('/login?return_to=%2Fadmin%2Fusers%3Fa%3D1%26b%3D2');
    const link = await screen.findByRole('link', { name: 'Sign in with GitLab' });
    expect(link).toHaveAttribute(
      'href',
      '/api/v1/auth/gitlab/login?return_to=%2Fadmin%2Fusers%3Fa%3D1%26b%3D2',
    );
  });

  it('redirects away from the login page when already signed in', async () => {
    mockFetch(async (call) => {
      if (call.path === '/api/v1/me') return json(200, makeUser());
      if (call.path === '/api/v1/auth/methods') return json(200, { gitlab: true, local: true });
      return undefined;
    });
    renderApp('/login?return_to=%2Fadmin%2Fusers');
    expect(await screen.findByRole('heading', { name: 'Users' })).toBeInTheDocument();
    expect(screen.getByTestId('location')).toHaveTextContent('/admin/users');
  });
});

describe('login page theme', () => {
  it('offers the theme switch', async () => {
    mockFetch(anonymous());
    renderApp('/login');
    expect(await screen.findByRole('group', { name: 'Theme' })).toBeInTheDocument();
  });
});

describe('login page return_to', () => {
  it.each(['//evil.com', 'https://evil.com', '/\\evil.com'])(
    'falls back to / for %s',
    async (value) => {
      mockFetch(anonymous());
      renderApp(`/login?return_to=${encodeURIComponent(value)}`);
      const link = await screen.findByRole('link', { name: 'Sign in with GitLab' });
      expect(link).toHaveAttribute('href', '/api/v1/auth/gitlab/login?return_to=%2F');
    },
  );

  it.each(['//evil.com', 'https://evil.com', '/\\evil.com'])(
    'does not navigate to %s after local sign-in',
    async (value) => {
      let signedIn = false;
      mockFetch(
        anonymous(undefined, (call) => {
          if (call.path === '/api/v1/auth/local/login') {
            signedIn = true;
            return noContent();
          }
          if (call.path === '/api/v1/me' && signedIn) return json(200, makeUser());
          return undefined;
        }),
      );
      renderApp(`/login?return_to=${encodeURIComponent(value)}`);
      await screen.findByLabelText('Username');
      fillAndSubmit();
      expect(await screen.findByRole('heading', { name: 'Portfolio' })).toBeInTheDocument();
      expect(screen.getByTestId('location')).toHaveTextContent(/^\/$/);
    },
  );
});

describe('local sign-in', () => {
  it('posts the credentials and navigates to return_to', async () => {
    let signedIn = false;
    const { calls } = mockFetch(
      anonymous(undefined, (call) => {
        if (call.path === '/api/v1/auth/local/login') {
          signedIn = true;
          return noContent();
        }
        if (call.path === '/api/v1/me' && signedIn) return json(200, makeUser());
        return undefined;
      }),
    );
    renderApp('/login?return_to=%2Fadmin%2Fusers');
    await screen.findByLabelText('Username');
    fillAndSubmit('admin', 'hunter2');
    expect(await screen.findByRole('heading', { name: 'Users' })).toBeInTheDocument();
    expect(screen.getByTestId('location')).toHaveTextContent('/admin/users');
    const login = calls.find((c) => c.path === '/api/v1/auth/local/login');
    expect(login).toMatchObject({
      method: 'POST',
      body: { username: 'admin', password: 'hunter2' },
    });
  });

  it('says the credentials are wrong on 401', async () => {
    mockFetch(
      anonymous(undefined, (call) =>
        call.path === '/api/v1/auth/local/login'
          ? apiError(401, 'invalid_credentials', 'Wrong.')
          : undefined,
      ),
    );
    renderApp('/login');
    await screen.findByLabelText('Username');
    fillAndSubmit();
    expect(await screen.findByText('Wrong username or password.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeEnabled();
  });

  it('shows the wait time from Retry-After on 429', async () => {
    mockFetch(
      anonymous(undefined, (call) =>
        call.path === '/api/v1/auth/local/login'
          ? apiError(429, 'rate_limited', 'Slow down.', { 'Retry-After': '30' })
          : undefined,
      ),
    );
    renderApp('/login');
    await screen.findByLabelText('Username');
    fillAndSubmit();
    expect(await screen.findByText('Too many attempts. Try again in 30 seconds.')).toBeVisible();
  });

  it('shows a plain message when the network fails', async () => {
    mockFetch(async (call) => {
      if (call.path === '/api/v1/auth/local/login') throw new TypeError('Failed to fetch');
      return anonymous()(call);
    });
    renderApp('/login');
    await screen.findByLabelText('Username');
    fillAndSubmit();
    expect(
      await screen.findByText(
        'Scanopsis could not reach the server. Check your connection and try again.',
      ),
    ).toBeVisible();
  });

  it('shows the server message for other errors', async () => {
    mockFetch(
      anonymous(undefined, (call) =>
        call.path === '/api/v1/auth/local/login'
          ? apiError(404, 'local_disabled', 'Local sign-in is turned off. Use GitLab instead.')
          : undefined,
      ),
    );
    renderApp('/login');
    await screen.findByLabelText('Username');
    fillAndSubmit();
    expect(
      await screen.findByText('Local sign-in is turned off. Use GitLab instead.'),
    ).toBeVisible();
  });
});

describe('login page error codes', () => {
  it.each([
    ['access_denied', 'You cancelled the sign-in on GitLab.'],
    ['account_disabled', 'Your account is disabled. Ask an administrator to enable it.'],
    [
      'group_not_allowed',
      'Your GitLab account is not in a group that may use Scanopsis. Ask an administrator to add your group.',
    ],
    ['login_expired', 'The sign-in took too long or was started in another tab. Try again.'],
    [
      'login_failed',
      'Sign-in with GitLab failed. Try again, and contact an administrator if it keeps failing.',
    ],
  ])('shows the message for %s', async (code, message) => {
    mockFetch(anonymous());
    renderApp(`/login?error=${code}`);
    expect(await screen.findByText(message)).toBeVisible();
  });

  it('shows no message for an unknown code', async () => {
    mockFetch(anonymous());
    renderApp('/login?error=whatever');
    await screen.findByLabelText('Username');
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it.each(['__proto__', 'constructor', 'toString', 'hasOwnProperty'])(
    'shows no message and does not crash for the inherited name %s',
    async (code) => {
      mockFetch(anonymous());
      renderApp(`/login?error=${code}`);
      await screen.findByLabelText('Username');
      expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    },
  );
});

describe('local sign-in cache', () => {
  it('clears data cached before sign-in', async () => {
    let signedIn = false;
    mockFetch(
      anonymous(undefined, (call) => {
        if (call.path === '/api/v1/auth/local/login') {
          signedIn = true;
          return noContent();
        }
        if (call.path === '/api/v1/me' && signedIn) return json(200, makeUser());
        return undefined;
      }),
    );
    const client = createQueryClient();
    client.setQueryData(['users'], { pages: ['previous principal'] });
    renderApp('/login', client);
    await screen.findByLabelText('Username');
    fillAndSubmit();
    expect(await screen.findByRole('heading', { name: 'Portfolio' })).toBeInTheDocument();
    expect(client.getQueryData(['users'])).toBeUndefined();
  });
});
