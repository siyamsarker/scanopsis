// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { apiError, json, makeUser, mockFetch, renderApp, type Handler } from '../test-utils';

const codes = Array.from({ length: 10 }, (_, i) => `abcd${i}-efgh${i}-ijkm${i}-npqr${i}`);

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

  it('starts enrolment once and shows the QR code and the secret in groups of four', async () => {
    const { handler } = enrolApi();
    const { calls } = mockFetch(handler);
    renderApp('/2fa/setup');
    const heading = await screen.findByRole('heading', { name: 'Set up two-factor sign-in' });
    expect(heading).toHaveFocus();
    const qr = await screen.findByRole('img', { name: 'QR code for your authenticator app' });
    expect(qr.querySelector('path')?.getAttribute('d')?.length).toBeGreaterThan(100);
    const secret = screen.getByLabelText('Secret');
    expect(secret).toHaveAttribute('data-secret', 'JBSWY3DPEHPK3PXP');
    expect(secret).toHaveClass('font-mono');
    // Screen readers get the secret in one piece; the eye gets groups of four.
    expect(secret.querySelector('.sr-only')).toHaveTextContent(/^JBSWY3DPEHPK3PXP$/);
    expect(
      [...secret.querySelectorAll('span[aria-hidden="true"]')].map((g) => g.textContent),
    ).toEqual(['JBSW', 'Y3DP', 'EHPK', '3PXP']);
    expect(calls.filter((c) => c.path === '/api/v1/auth/mfa/enrollment')).toHaveLength(1);
  });

  it('shows the enrolment steps', async () => {
    mockFetch(enrolApi().handler);
    renderApp('/2fa/setup');
    await screen.findByRole('heading', { name: 'Set up two-factor sign-in' });
    const progress = screen.getByRole('navigation', { name: 'Progress' });
    expect(progress).toHaveTextContent('Step 1 of 3');
    expect(
      within(progress)
        .getAllByRole('listitem')
        .map((li) => li.textContent),
    ).toEqual(['Scan the code', 'Enter a code', 'Save recovery codes']);
    await toCodeStep();
    expect(progress).toHaveTextContent('Step 2 of 3');
    expect(within(progress).getByText('Enter a code').closest('li')).toHaveAttribute(
      'aria-current',
      'step',
    );
  });

  it('copies the secret', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    mockFetch(enrolApi().handler);
    renderApp('/2fa/setup');
    fireEvent.click(await screen.findByRole('button', { name: 'Copy key' }));
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
    const { calls } = mockFetch(
      enrolApi({ confirm: () => apiError(401, 'invalid_code', 'Wrong code.') }).handler,
    );
    renderApp('/2fa/setup');
    fireEvent.change(await toCodeStep(), { target: { value: '000000' } });
    expect(await screen.findByRole('alert')).toHaveTextContent('That code is not correct');
    expect(screen.getByLabelText('6-digit code')).toBeInTheDocument();
    // Confirm sends the same code again on request.
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }));
    await waitFor(() => expect(calls.filter((c) => c.path.endsWith('/confirm'))).toHaveLength(2));
  });

  it('goes back to the QR code without starting again', async () => {
    const { calls } = mockFetch(enrolApi().handler);
    renderApp('/2fa/setup');
    await toCodeStep();
    fireEvent.click(screen.getByRole('button', { name: 'Back to the QR code' }));
    expect(
      await screen.findByRole('img', { name: 'QR code for your authenticator app' }),
    ).toBeInTheDocument();
    expect(screen.getByLabelText('Secret')).toHaveAttribute('data-secret', 'JBSWY3DPEHPK3PXP');
    expect(calls.filter((c) => c.path === '/api/v1/auth/mfa/enrollment')).toHaveLength(1);
  });

  it('shows the recovery codes once as a numbered sheet, with copy, download and a required checkbox', async () => {
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
    // The sixth digit confirms; spaces are dropped from the code that is sent.
    fireEvent.change(await toCodeStep(), { target: { value: '123 456' } });

    const heading = await screen.findByRole('heading', { name: 'Save your recovery codes' });
    // Focus moves to the heading after the page renders.
    await waitFor(() => expect(heading).toHaveFocus());
    expect(calls.find((c) => c.path.endsWith('/confirm'))?.body).toEqual({ code: '123456' });
    const list = screen.getByRole('list', { name: 'Recovery codes' });
    expect(list.tagName).toBe('OL');
    expect([...list.querySelectorAll('li')].map((li) => li.textContent)).toEqual(codes);
    expect(list).toHaveClass('font-mono');
    expect(calls.filter((c) => c.path.endsWith('/confirm'))).toHaveLength(1);
    // The sheet names the account it belongs to.
    expect(screen.getByText('ada')).toBeVisible();
    expect(screen.queryByLabelText('Secret')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Copy all' }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(codes.join('\n')));

    expect(await screen.findByRole('button', { name: 'Copied' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Download as text file' }));
    expect(click).toHaveBeenCalled();
    const text = (await blob?.text()) ?? '';
    expect(text).toContain('Scanopsis recovery codes');
    expect(text).toContain('Account: ada');
    expect(text).toContain(` 4. ${codes[3]}`);
    expect(text).toContain(`10. ${codes[9]}`);
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:codes');

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
