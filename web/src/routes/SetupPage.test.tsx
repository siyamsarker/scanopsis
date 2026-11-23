// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { fireEvent, screen, waitFor, within } from '@testing-library/react';
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

/** Takes the first choice of the setup screen, the new installation. */
async function startNew() {
  fireEvent.click(await screen.findByRole('button', { name: /Set up a new installation/ }));
  return screen.findByRole('heading', { name: 'Set up Scanopsis' });
}

function fill(token = 'tok-123', password = 'correct horse battery', confirm = password) {
  fireEvent.change(screen.getByLabelText('Setup token'), { target: { value: token } });
  fireEvent.change(screen.getByLabelText('Username'), { target: { value: 'root' } });
  fireEvent.change(screen.getByLabelText('Password'), { target: { value: password } });
  fireEvent.change(screen.getByLabelText('Confirm password'), { target: { value: confirm } });
  fireEvent.click(screen.getByRole('button', { name: 'Create administrator' }));
}

describe('setup page', () => {
  it('explains where to find the token and checks the password rules as you type', async () => {
    mockFetch(setupApi(() => noContent()));
    renderApp('/');
    const heading = await startNew();
    // Focus moves to the heading after the page renders.
    await waitFor(() => expect(heading).toHaveFocus());
    const token = screen.getByLabelText('Setup token');
    expect(token).toHaveClass('font-mono');
    expect(token).toHaveAccessibleDescription(
      'Printed in the server log at start-up. In deploy/compose, run docker compose logs scanopsis and copy the setup_token value.',
    );

    const rules = () =>
      ['At least 12 characters', 'Not your username', 'Not one character repeated'].map(
        (rule) => screen.getByText(rule).textContent,
      );
    expect(rules()).toEqual([
      'At least 12 characters: not yet',
      'Not your username: not yet',
      'Not one character repeated: not yet',
    ]);
    fireEvent.change(screen.getByLabelText('Username'), { target: { value: 'root' } });
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'ROOT' } });
    expect(rules()).toEqual([
      'At least 12 characters: not yet',
      'Not your username: not yet',
      'Not one character repeated: met',
    ]);
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'aaaaaaaaaaaa' } });
    expect(rules()).toEqual([
      'At least 12 characters: met',
      'Not your username: met',
      'Not one character repeated: not yet',
    ]);
    fireEvent.change(screen.getByLabelText('Password'), {
      target: { value: 'correct horse battery' },
    });
    expect(rules().every((text) => text?.endsWith(': met'))).toBe(true);
  });

  it('says what is missing in the page, not in a browser bubble', async () => {
    const { calls } = mockFetch(setupApi(() => noContent()));
    renderApp('/');
    await startNew();
    const button = screen.getByRole('button', { name: 'Create administrator' });
    expect(button.closest('form')).toHaveAttribute('novalidate');
    fireEvent.click(button);
    expect(await screen.findByText('Enter the setup token.')).toBeVisible();
    expect(screen.getByLabelText('Setup token')).toHaveAttribute('aria-invalid', 'true');
    expect(screen.getByText('Enter a username.')).toBeVisible();
    expect(screen.getByLabelText('Setup token')).toHaveFocus();
    expect(calls.some((c) => c.method === 'POST' && c.path === '/api/v1/setup')).toBe(false);
  });

  it('shows where setup is in the steps to the first sign-in', async () => {
    mockFetch(setupApi(() => noContent()));
    renderApp('/setup');
    await startNew();
    const progress = screen.getByRole('navigation', { name: 'Progress' });
    expect(progress).toHaveTextContent('Step 1 of 3');
    expect(
      within(progress)
        .getAllByRole('listitem')
        .map((li) => li.textContent),
    ).toEqual(['Administrator', 'Two-factor', 'Recovery codes']);
    expect(within(progress).getByText('Administrator').closest('li')).toHaveAttribute(
      'aria-current',
      'step',
    );
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
    await startNew();
    // The token is sent without surrounding spaces.
    fill('  tok-123 ');
    expect(
      await screen.findByRole('heading', { name: 'Set up two-factor sign-in' }),
    ).toBeInTheDocument();
    expect(screen.getByTestId('location')).toHaveTextContent(/^\/2fa\/setup$/);
    // Enrolment continues the setup steps.
    const progress = screen.getByRole('navigation', { name: 'Progress' });
    expect(progress).toHaveTextContent('Step 2 of 3');
    expect(within(progress).getByText('Two-factor').closest('li')).toHaveAttribute(
      'aria-current',
      'step',
    );
    expect(calls.find((c) => c.method === 'POST' && c.path === '/api/v1/setup')?.body).toEqual({
      token: 'tok-123',
      username: 'root',
      password: 'correct horse battery',
    });
  });

  it('does not send the form when the passwords differ', async () => {
    const { calls } = mockFetch(setupApi(() => noContent()));
    renderApp('/setup');
    await startNew();
    fill('t', 'correct horse battery', 'something else entirely');
    expect(await screen.findByText('The passwords do not match.')).toBeVisible();
    expect(screen.getByLabelText('Confirm password')).toHaveAttribute('aria-invalid', 'true');
    expect(calls.some((c) => c.method === 'POST')).toBe(false);
    // Typing again clears the message.
    fireEvent.change(screen.getByLabelText('Confirm password'), {
      target: { value: 'correct horse battery' },
    });
    expect(screen.queryByText('The passwords do not match.')).not.toBeInTheDocument();
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
    await startNew();
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
    await startNew();
    fill();
    expect(await screen.findByRole('heading', { name: 'Sign in' })).toBeInTheDocument();
    expect(screen.getByTestId('location')).toHaveTextContent(/^\/login$/);
  });

  it('shows the wait on a 429', async () => {
    mockFetch(setupApi(() => apiError(429, 'rate_limited', 'Slow down.', { 'Retry-After': '42' })));
    renderApp('/setup');
    await startNew();
    fill();
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Too many attempts. Try again in 42 seconds.',
    );
  });

  it('steps back in the history to the choice, leaving no second entry', async () => {
    mockFetch(setupApi(() => noContent()));
    renderApp('/setup');
    await startNew();
    expect(screen.getByTestId('location')).toHaveTextContent('/setup?start=new');
    fireEvent.click(screen.getByRole('button', { name: 'Back' }));
    expect(await screen.findByRole('button', { name: /Restore from a backup/ })).toBeVisible();
    expect(screen.getByTestId('location')).toHaveTextContent(/^\/setup$/);
    // The entry of the choice was left, not covered by a new one.
    expect(screen.getByTestId('navigation')).toHaveTextContent('POP');
  });

  it('replaces a choice opened from its address, which has no choice before it', async () => {
    mockFetch(setupApi(() => noContent()));
    renderApp('/setup?start=restore');
    await screen.findByRole('heading', { name: 'Restore from a backup' });
    fireEvent.click(screen.getByRole('button', { name: 'Back' }));
    expect(await screen.findByRole('button', { name: /Set up a new installation/ })).toBeVisible();
    expect(screen.getByTestId('location')).toHaveTextContent(/^\/setup$/);
    expect(screen.getByTestId('navigation')).toHaveTextContent('REPLACE');
  });
});
