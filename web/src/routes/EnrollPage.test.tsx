// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { fireEvent, screen, waitFor } from '@testing-library/react';
import { apiError, json, makeUser, mockFetch, renderApp, type Handler } from '../test-utils';

const codes = Array.from({ length: 10 }, (_, i) => `abcd${i}-efgh${i}`);

function enrolApi(opts: { confirm?: Handler } = {}) {
  const state = { enrolled: false };
  const handler: Handler = async (call) => {
    if (call.path === '/api/v1/setup') return json(200, { required: false });
    if (call.path === '/api/v1/me') {
      return json(200, makeUser({ mfa_enrolled: state.enrolled, mfa_verified: state.enrolled }));
    }
    if (call.path === '/api/v1/auth/mfa/enrollment') {
      return json(200, {
        secret: 'JBSWY3DPEHPK3PXP',
        otpauth_uri: 'otpauth://totp/Scanopsis:ada?secret=JBSWY3DPEHPK3PXP&issuer=Scanopsis',
      });
    }
    if (call.path === '/api/v1/auth/mfa/enrollment/confirm') {
      const custom = await opts.confirm?.(call);
      if (custom) return custom;
      state.enrolled = true;
      return json(200, { recovery_codes: codes });
    }
    return undefined;
  };
  return { handler, state };
}

async function toCodeStep() {
  fireEvent.click(await screen.findByRole('button', { name: 'Next' }));
  return screen.findByLabelText('6-digit code');
}

describe('two-factor enrolment', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('starts enrolment once and shows the QR code and the secret', async () => {
    const { handler } = enrolApi();
    const { calls } = mockFetch(handler);
    renderApp('/2fa/setup');
    const heading = await screen.findByRole('heading', { name: 'Set up two-factor sign-in' });
    expect(heading).toHaveFocus();
    const qr = await screen.findByRole('img', { name: 'QR code for your authenticator app' });
    expect(qr.querySelector('path')?.getAttribute('d')?.length).toBeGreaterThan(100);
    expect(screen.getByLabelText('Secret')).toHaveTextContent('JBSWY3DPEHPK3PXP');
    expect(screen.getByLabelText('Secret')).toHaveClass('font-mono');
    expect(calls.filter((c) => c.path === '/api/v1/auth/mfa/enrollment')).toHaveLength(1);
  });

  it('copies the secret', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    mockFetch(enrolApi().handler);
    renderApp('/2fa/setup');
    fireEvent.click(await screen.findByRole('button', { name: 'Copy secret' }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('JBSWY3DPEHPK3PXP'));
    expect(await screen.findByRole('button', { name: 'Copied' })).toBeInTheDocument();
  });

  it('moves focus to the heading of the next step and asks for a numeric code', async () => {
    mockFetch(enrolApi().handler);
    renderApp('/2fa/setup');
    const input = await toCodeStep();
    expect(screen.getByRole('heading', { name: 'Enter the code from the app' })).toHaveFocus();
    expect(input).toHaveAttribute('inputmode', 'numeric');
    expect(input).toHaveAttribute('autocomplete', 'one-time-code');
  });

  it('shows an error for a wrong code and stays on the step', async () => {
    mockFetch(enrolApi({ confirm: () => apiError(401, 'invalid_code', 'Wrong code.') }).handler);
    renderApp('/2fa/setup');
    fireEvent.change(await toCodeStep(), { target: { value: '000000' } });
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('That code is not correct');
    expect(screen.getByLabelText('6-digit code')).toBeInTheDocument();
  });

  it('shows the recovery codes once, with copy, download and a required checkbox', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    let blob: Blob | undefined;
    URL.createObjectURL = vi.fn((b: Blob) => {
      blob = b;
      return 'blob:codes';
    });
    URL.revokeObjectURL = vi.fn();
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});

    const { handler } = enrolApi();
    const { calls } = mockFetch(handler);
    renderApp('/2fa/setup?return_to=%2Fadmin%2Fusers');
    fireEvent.change(await toCodeStep(), { target: { value: '123 456' } });
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }));

    const heading = await screen.findByRole('heading', { name: 'Save your recovery codes' });
    // Focus moves to the heading after the page renders.
    await waitFor(() => expect(heading).toHaveFocus());
    expect(calls.find((c) => c.path.endsWith('/confirm'))?.body).toEqual({ code: '123456' });
    const list = screen.getByRole('list', { name: 'Recovery codes' });
    expect(list.querySelectorAll('li')).toHaveLength(10);
    expect(list).toHaveClass('font-mono');
    expect(screen.queryByLabelText('Secret')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Copy all' }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(codes.join('\n')));

    fireEvent.click(screen.getByRole('button', { name: 'Download as text file' }));
    expect(click).toHaveBeenCalled();
    expect(await blob?.text()).toContain(codes[3]);

    const next = screen.getByRole('button', { name: 'Continue' });
    expect(next).toBeDisabled();
    fireEvent.click(screen.getByRole('checkbox', { name: 'I have saved these codes' }));
    expect(next).toBeEnabled();
    fireEvent.click(next);

    expect(await screen.findByRole('heading', { name: 'Users' })).toBeInTheDocument();
    expect(screen.getByTestId('location')).toHaveTextContent(/^\/admin\/users$/);
    expect(screen.queryByText(codes[0])).not.toBeInTheDocument();
  });

  it('offers to try again when enrolment cannot start', async () => {
    mockFetch(async (call) => {
      if (call.path === '/api/v1/setup') return json(200, { required: false });
      if (call.path === '/api/v1/me')
        return json(200, makeUser({ mfa_enrolled: false, mfa_verified: false }));
      return apiError(500, 'internal', 'Something broke.');
    });
    renderApp('/2fa/setup');
    expect(await screen.findByRole('alert')).toHaveTextContent('Something broke.');
    expect(screen.getByRole('button', { name: 'Try again' })).toBeInTheDocument();
  });
});
