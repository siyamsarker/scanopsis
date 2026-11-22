// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import type { MySession } from '../api/hooks';
import {
  apiError,
  json,
  makeUser,
  mockFetch,
  noContent,
  renderApp,
  type Handler,
} from '../test-utils';
import { describeAgent } from './AccountPage';

const firefox = 'Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0';
const iphone =
  'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1';

function session(overrides: Partial<MySession>): MySession {
  return {
    id: 1,
    current: false,
    created_at: '2026-11-18T10:00:00Z',
    last_seen_at: '2026-11-18T11:00:00Z',
    expires_at: '2026-11-18T22:00:00Z',
    mfa_verified: true,
    client_ip: '192.0.2.1',
    user_agent: firefox,
    ...overrides,
  };
}

const current = session({ id: 7, current: true, client_ip: '2001:db8::1' });
const phone = session({ id: 8, user_agent: iphone, client_ip: '198.51.100.4' });
const pending = session({ id: 9, mfa_verified: false, user_agent: '', client_ip: '' });

function api(sessions: MySession[], extra?: Handler): Handler {
  return async (call) => {
    const custom = await extra?.(call);
    if (custom) return custom;
    if (call.path === '/api/v1/setup') return json(200, { required: false });
    if (call.path === '/api/v1/me') return json(200, makeUser());
    if (call.method === 'GET' && call.path === '/api/v1/me/sessions')
      return json(200, { items: sessions });
    return undefined;
  };
}

describe('describeAgent', () => {
  it.each([
    [firefox, 'Firefox on Linux', false],
    [iphone, 'Safari on iOS', true],
    [
      'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36 Edg/140.0',
      'Edge on Windows',
      false,
    ],
    ['curl/8.11.0', 'curl', false],
    ['', 'Unknown browser', false],
  ])('names %j as %j', (ua, label, mobile) => {
    expect(describeAgent(ua)).toEqual({ label, mobile });
  });
});

describe('account page', () => {
  it('is linked from the header', async () => {
    mockFetch(api([current]));
    renderApp('/account');
    expect(
      await screen.findByRole('link', { name: 'Your account: Ada Lovelace, Admin' }),
    ).toHaveAttribute('href', '/account');
  });

  it('lists the sessions and marks this browser', async () => {
    mockFetch(api([current, phone, pending]));
    renderApp('/account');
    const list = await screen.findByRole('list', { name: 'Sessions' });
    const rows = within(list).getAllByRole('listitem');
    expect(rows).toHaveLength(3);
    expect(within(rows[0]).getByText('This browser')).toBeInTheDocument();
    expect(within(rows[0]).getByText('2001:db8::1')).toBeInTheDocument();
    expect(within(rows[0]).queryByRole('button')).not.toBeInTheDocument();
    expect(within(rows[1]).getByText('Safari on iOS')).toBeInTheDocument();
    expect(within(rows[2]).getByText('Second factor not completed')).toBeInTheDocument();
    expect(within(rows[2]).getByText('Address not recorded')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Account' })).toBeInTheDocument();
  });

  it('ends one session', async () => {
    let items = [current, phone];
    const { calls } = mockFetch(
      api([], (call) => {
        if (call.method === 'DELETE' && call.path === '/api/v1/me/sessions/8') {
          items = [current];
          return noContent();
        }
        if (call.method === 'GET' && call.path === '/api/v1/me/sessions')
          return json(200, { items });
        return undefined;
      }),
    );
    renderApp('/account');
    fireEvent.click(
      await screen.findByRole('button', { name: 'End session Safari on iOS, 198.51.100.4' }),
    );
    await waitFor(() => expect(screen.getAllByRole('listitem')).toHaveLength(1));
    expect(screen.getByText('2001:db8::1')).toBeInTheDocument();
    expect(calls.filter((c) => c.method === 'DELETE')).toHaveLength(1);
  });

  it('ends all other sessions after confirmation and reports how many', async () => {
    const { calls } = mockFetch(
      api([current, phone, pending], (call) =>
        call.method === 'DELETE' && call.path === '/api/v1/me/sessions'
          ? json(200, { ended: 2 })
          : undefined,
      ),
    );
    renderApp('/account');
    await screen.findByRole('list', { name: 'Sessions' });
    fireEvent.click(screen.getByRole('button', { name: 'End all other sessions' }));
    const dialog = screen.getByRole('alertdialog', { name: 'End all other sessions?' });
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false);
    fireEvent.click(within(dialog).getByRole('button', { name: 'End other sessions' }));
    expect(await screen.findByText('Ended 2 other sessions.')).toBeInTheDocument();
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
  });

  it('cannot end others when this is the only session', async () => {
    mockFetch(api([current]));
    renderApp('/account');
    await screen.findByRole('list', { name: 'Sessions' });
    expect(await screen.findByRole('button', { name: 'End all other sessions' })).toBeDisabled();
  });

  it('explains a failed load', async () => {
    mockFetch(
      api([], (call) =>
        call.path === '/api/v1/me/sessions' ? apiError(500, 'internal_error', 'Boom.') : undefined,
      ),
    );
    renderApp('/account');
    expect(await screen.findByText('Your sessions could not be loaded')).toBeInTheDocument();
    expect(screen.getByText('Boom.')).toBeInTheDocument();
  });
});
