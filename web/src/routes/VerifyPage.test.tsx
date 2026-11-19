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

function verifyApi(verify: Handler) {
  const state = { verified: false, out: false };
  const handler: Handler = async (call) => {
    if (call.path === '/api/v1/setup') return json(200, { required: false });
    if (call.path === '/api/v1/me') {
      return state.out
        ? apiError(401, 'unauthenticated', 'Sign in first.')
        : json(200, makeUser({ mfa_verified: state.verified }));
    }
    if (call.path === '/api/v1/auth/mfa/verify') {
      const response = await verify(call);
      if (response?.ok) state.verified = true;
      return response;
    }
    if (call.path === '/api/v1/auth/logout') {
      state.out = true;
      return noContent();
    }
    if (call.path === '/api/v1/auth/methods') return json(200, { gitlab: true, local: true });
    return undefined;
  };
  return handler;
}

describe('two-factor code page', () => {
  it('verifies an authenticator code and continues to the destination', async () => {
    const { calls } = mockFetch(verifyApi(() => noContent()));
    renderApp('/2fa?return_to=%2Fadmin%2Fusers');
    const input = await screen.findByLabelText('6-digit code');
    expect(screen.getByRole('heading', { name: 'Enter your code' })).toHaveFocus();
    expect(input).toHaveAttribute('inputmode', 'numeric');
    expect(input).toHaveAttribute('autocomplete', 'one-time-code');
    fireEvent.change(input, { target: { value: '123456' } });
    fireEvent.click(screen.getByRole('button', { name: 'Verify' }));
    expect(await screen.findByRole('heading', { name: 'Users' })).toBeInTheDocument();
    expect(calls.find((c) => c.path === '/api/v1/auth/mfa/verify')?.body).toEqual({
      code: '123456',
    });
  });

  it('switches to a recovery code and sends it', async () => {
    const { calls } = mockFetch(verifyApi(() => noContent()));
    renderApp('/2fa');
    fireEvent.click(await screen.findByRole('button', { name: 'Use a recovery code' }));
    expect(screen.getByRole('heading', { name: 'Use a recovery code' })).toHaveFocus();
    const input = screen.getByLabelText('Recovery code');
    expect(input).toHaveClass('font-mono');
    fireEvent.change(input, { target: { value: 'abcde-fghij' } });
    fireEvent.click(screen.getByRole('button', { name: 'Verify' }));
    expect(await screen.findByRole('heading', { name: 'Services' })).toBeInTheDocument();
    expect(calls.find((c) => c.path === '/api/v1/auth/mfa/verify')?.body).toEqual({
      code: 'abcde-fghij',
    });
  });

  it('shows an error for a wrong code', async () => {
    mockFetch(verifyApi(() => apiError(401, 'invalid_code', 'Wrong code.')));
    renderApp('/2fa');
    fireEvent.change(await screen.findByLabelText('6-digit code'), { target: { value: '000000' } });
    fireEvent.click(screen.getByRole('button', { name: 'Verify' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('That code is not correct');
    expect(screen.getByRole('button', { name: 'Verify' })).toBeEnabled();
  });

  it('shows the wait from Retry-After on a 429', async () => {
    mockFetch(
      verifyApi(() => apiError(429, 'rate_limited', 'Slow down.', { 'Retry-After': '30' })),
    );
    renderApp('/2fa');
    fireEvent.change(await screen.findByLabelText('6-digit code'), { target: { value: '000000' } });
    fireEvent.click(screen.getByRole('button', { name: 'Verify' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Too many attempts. Try again in 30 seconds.',
    );
  });

  it('signs out from the page', async () => {
    const { calls } = mockFetch(verifyApi(() => noContent()));
    renderApp('/2fa');
    fireEvent.click(await screen.findByRole('button', { name: 'Sign out' }));
    expect(await screen.findByRole('heading', { name: 'Sign in' })).toBeInTheDocument();
    expect(calls.some((c) => c.path === '/api/v1/auth/logout')).toBe(true);
  });
});
