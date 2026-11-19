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

function setupApi(post: Handler, signedIn = { value: false }): Handler {
  return async (call) => {
    if (call.path === '/api/v1/setup' && call.method === 'GET')
      return json(200, { required: !signedIn.value });
    if (call.path === '/api/v1/setup' && call.method === 'POST') return post(call);
    if (call.path === '/api/v1/me') {
      return signedIn.value
        ? json(200, makeUser({ mfa_enrolled: false, mfa_verified: false }))
        : apiError(401, 'unauthenticated', 'Sign in first.');
    }
    if (call.path === '/api/v1/auth/mfa/enrollment') {
      return json(200, { secret: 'ABCDEFGH', otpauth_uri: 'otpauth://totp/x?secret=ABCDEFGH' });
    }
    if (call.path === '/api/v1/auth/methods') return json(200, { gitlab: false, local: true });
    return undefined;
  };
}

function fill(token = 'tok-123', password = 'correct horse battery', confirm = password) {
  fireEvent.change(screen.getByLabelText('Setup token'), { target: { value: token } });
  fireEvent.change(screen.getByLabelText('Username'), { target: { value: 'root' } });
  fireEvent.change(screen.getByLabelText('Password'), { target: { value: password } });
  fireEvent.change(screen.getByLabelText('Confirm password'), { target: { value: confirm } });
  fireEvent.click(screen.getByRole('button', { name: 'Create administrator' }));
}

describe('setup page', () => {
  it('explains where to find the token and the password rules', async () => {
    mockFetch(setupApi(() => noContent()));
    renderApp('/');
    expect(await screen.findByRole('heading', { name: 'Set up Scanopsis' })).toHaveFocus();
    expect(screen.getByText(/Find the setup token in the server log/)).toHaveTextContent(
      'docker logs <container>',
    );
    expect(screen.getByText('At least 12 characters, and not your username.')).toBeVisible();
    expect(screen.getByLabelText('Setup token')).toHaveClass('font-mono');
  });

  it('creates the administrator and continues to two-factor enrolment', async () => {
    const state = { value: false };
    const { calls } = mockFetch(
      setupApi(() => {
        state.value = true;
        return noContent();
      }, state),
    );
    renderApp('/setup');
    await screen.findByRole('heading', { name: 'Set up Scanopsis' });
    fill();
    expect(
      await screen.findByRole('heading', { name: 'Set up two-factor sign-in' }),
    ).toBeInTheDocument();
    expect(screen.getByTestId('location')).toHaveTextContent(/^\/2fa\/setup$/);
    expect(calls.find((c) => c.method === 'POST' && c.path === '/api/v1/setup')?.body).toEqual({
      token: 'tok-123',
      username: 'root',
      password: 'correct horse battery',
    });
  });

  it('does not send the form when the passwords differ', async () => {
    const { calls } = mockFetch(setupApi(() => noContent()));
    renderApp('/setup');
    await screen.findByRole('heading', { name: 'Set up Scanopsis' });
    fill('t', 'correct horse battery', 'something else entirely');
    expect(await screen.findByText('The passwords do not match.')).toBeVisible();
    expect(calls.some((c) => c.method === 'POST')).toBe(false);
  });

  it.each([
    [
      'invalid_setup_token',
      403,
      'Wrong token.',
      'The setup token is not correct. Copy it again from the server log.',
    ],
    [
      'weak_password',
      400,
      'The password must not be a common password.',
      'The password must not be a common password.',
    ],
  ])('shows a message for %s', async (code, status, message, shown) => {
    mockFetch(setupApi(() => apiError(status, code, message)));
    renderApp('/setup');
    await screen.findByRole('heading', { name: 'Set up Scanopsis' });
    fill();
    expect(await screen.findByRole('alert')).toHaveTextContent(shown);
    expect(screen.getByRole('button', { name: 'Create administrator' })).toBeEnabled();
  });

  it('goes to sign-in when setup is already complete', async () => {
    let done = false;
    mockFetch(async (call) => {
      if (call.path === '/api/v1/setup' && call.method === 'GET')
        return json(200, { required: !done });
      if (call.path === '/api/v1/setup') {
        done = true;
        return apiError(409, 'setup_complete', 'Setup is already complete.');
      }
      return setupApi(() => undefined)(call);
    });
    renderApp('/setup');
    await screen.findByRole('heading', { name: 'Set up Scanopsis' });
    fill();
    expect(await screen.findByRole('heading', { name: 'Sign in' })).toBeInTheDocument();
    expect(screen.getByTestId('location')).toHaveTextContent(/^\/login$/);
  });

  it('shows the wait on a 429', async () => {
    mockFetch(setupApi(() => apiError(429, 'rate_limited', 'Slow down.', { 'Retry-After': '42' })));
    renderApp('/setup');
    await screen.findByRole('heading', { name: 'Set up Scanopsis' });
    fill();
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Too many attempts. Try again in 42 seconds.',
    );
  });
});
