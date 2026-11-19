// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { fireEvent, screen, waitFor } from '@testing-library/react';
import {
  apiError,
  json,
  makeUser,
  mockFetch,
  noContent,
  renderApp,
  type Handler,
} from '../test-utils';

function passwordApi(change: Handler) {
  const state = { must: true };
  const handler: Handler = async (call) => {
    if (call.path === '/api/v1/setup') return json(200, { required: false });
    if (call.path === '/api/v1/me')
      return json(200, makeUser({ must_change_password: state.must }));
    if (call.path === '/api/v1/me/password') {
      const response = await change(call);
      if (response?.ok) state.must = false;
      return response;
    }
    return undefined;
  };
  return handler;
}

function fill(next = 'a much longer password', confirm = next) {
  fireEvent.change(screen.getByLabelText('Current password'), { target: { value: 'temporary-1' } });
  fireEvent.change(screen.getByLabelText('New password'), { target: { value: next } });
  fireEvent.change(screen.getByLabelText('Confirm new password'), { target: { value: confirm } });
  fireEvent.click(screen.getByRole('button', { name: 'Change password' }));
}

describe('password page', () => {
  it('shows the rules, changes the password and continues to the destination', async () => {
    const { calls } = mockFetch(passwordApi(() => noContent()));
    renderApp('/password?return_to=%2Fadmin%2Fusers');
    const heading = await screen.findByRole('heading', { name: 'Choose a new password' });
    // Focus moves to the heading after the page renders.
    await waitFor(() => expect(heading).toHaveFocus());
    expect(screen.getByText('At least 12 characters, and not your username.')).toBeVisible();
    fill();
    expect(await screen.findByRole('heading', { name: 'Users' })).toBeInTheDocument();
    expect(calls.find((c) => c.path === '/api/v1/me/password')?.body).toEqual({
      current_password: 'temporary-1',
      new_password: 'a much longer password',
    });
  });

  it('does not send the form when the passwords differ', async () => {
    const { calls } = mockFetch(passwordApi(() => noContent()));
    renderApp('/password');
    await screen.findByRole('heading', { name: 'Choose a new password' });
    fill('a much longer password', 'another long password');
    expect(await screen.findByText('The passwords do not match.')).toBeVisible();
    expect(calls.some((c) => c.method === 'POST')).toBe(false);
  });

  it('explains a wrong current password', async () => {
    mockFetch(passwordApi(() => apiError(401, 'invalid_credentials', 'Wrong password.')));
    renderApp('/password');
    await screen.findByRole('heading', { name: 'Choose a new password' });
    fill();
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'The current password is not correct',
    );
  });

  it('shows the server message for a weak password', async () => {
    mockFetch(passwordApi(() => apiError(400, 'weak_password', 'The password is too common.')));
    renderApp('/password');
    await screen.findByRole('heading', { name: 'Choose a new password' });
    fill();
    expect(await screen.findByRole('alert')).toHaveTextContent('The password is too common.');
  });
});
