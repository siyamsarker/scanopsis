// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import type { MySession } from '../api/hooks';
import {
  apiError,
  json,
  makeUser,
  mockFetch,
  noContent,
  openMenu,
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

function sessionsList() {
  return screen.findByRole('list', { name: 'Sessions' });
}

describe('account page', () => {
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
    localStorage.removeItem('scanopsis.theme');
  });

  it('is linked from the account menu', async () => {
    mockFetch(api([current]));
    renderApp('/');
    const menu = await openMenu('Account: Ada Lovelace, Admin');
    fireEvent.click(within(menu).getByRole('menuitem', { name: 'Your account' }));
    expect(await sessionsList()).toBeInTheDocument();
    expect(screen.getByTestId('location')).toHaveTextContent('/account');
  });

  it('shows the profile: name, username, sign-in method, role and two-factor', async () => {
    mockFetch(api([current]));
    renderApp('/account');
    const profile = await screen.findByRole('region', { name: 'Profile' });
    expect(within(profile).getByText('Ada Lovelace')).toBeInTheDocument();
    expect(within(profile).getByText('ada')).toBeInTheDocument();
    expect(within(profile).getByText('ada@example.com')).toBeInTheDocument();
    expect(within(profile).getByText('AL')).toHaveAttribute('aria-hidden', 'true');
    const fact = (label: string) =>
      within(profile).getByText(label).closest('div')!.querySelector('dd')!;
    expect(fact('Signs in with')).toHaveTextContent('GitLab');
    expect(fact('Role')).toHaveTextContent('Admin');
    expect(fact('Two-factor')).toHaveTextContent('On');
    expect(fact('Member since').querySelector('time')).toHaveAttribute(
      'datetime',
      '2026-01-01T00:00:00Z',
    );
    expect(screen.getByRole('heading', { name: 'Account' })).toBeInTheDocument();
  });

  it('describes a local viewer without a name or e-mail', async () => {
    mockFetch(
      api([current], (call) =>
        call.path === '/api/v1/me'
          ? json(
              200,
              makeUser({
                username: 'ops',
                name: '',
                email: '',
                role: 'viewer',
                source: 'local',
              }),
            )
          : undefined,
      ),
    );
    renderApp('/account');
    const profile = await screen.findByRole('region', { name: 'Profile' });
    expect(within(profile).getByText('ops')).toBeInTheDocument();
    expect(within(profile).getByText('Local account')).toBeInTheDocument();
    expect(within(profile).getByText('Local password')).toBeInTheDocument();
    expect(within(profile).getByText('Viewer')).toBeInTheDocument();
  });

  it('lists the sessions and marks this browser', async () => {
    mockFetch(api([current, phone, pending]));
    renderApp('/account');
    const list = await sessionsList();
    const rows = within(list).getAllByRole('listitem');
    expect(rows).toHaveLength(3);
    expect(within(rows[0]).getByText('This browser')).toBeInTheDocument();
    expect(within(rows[0]).getByText('2001:db8::1')).toBeInTheDocument();
    expect(within(rows[0]).queryByRole('button')).not.toBeInTheDocument();
    expect(within(rows[1]).getByText('Safari on iOS')).toBeInTheDocument();
    expect(within(rows[1]).getByText(/^Signed in/)).toBeInTheDocument();
    expect(within(rows[1]).getByText(/^Last active/)).toBeInTheDocument();
    expect(within(rows[2]).getByText('Second factor not completed')).toBeInTheDocument();
    expect(within(rows[2]).getByText('Address not recorded')).toBeInTheDocument();
  });

  it('ends one session after confirmation', async () => {
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
      await screen.findByRole('button', { name: /^End session ?Safari on iOS, 198\.51\.100\.4$/ }),
    );
    const dialog = screen.getByRole('alertdialog', { name: 'End this session?' });
    expect(within(dialog).getByText(/Safari on iOS at 198\.51\.100\.4/)).toBeInTheDocument();
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false);
    const confirm = within(dialog).getByRole('button', { name: 'End session' });
    expect(confirm.className).toMatch(/bg-fail/);
    fireEvent.click(confirm);
    expect(await screen.findByText('Session ended')).toBeInTheDocument();
    expect(screen.getByText('Safari on iOS is signed out.')).toBeInTheDocument();
    await waitFor(() =>
      expect(
        within(screen.getByRole('list', { name: 'Sessions' })).getAllByRole('listitem'),
      ).toHaveLength(1),
    );
    expect(screen.getByText('2001:db8::1')).toBeInTheDocument();
    expect(calls.filter((c) => c.method === 'DELETE')).toHaveLength(1);
  });

  it('keeps the session when the confirmation is cancelled', async () => {
    const { calls } = mockFetch(api([current, phone]));
    renderApp('/account');
    fireEvent.click(
      await screen.findByRole('button', { name: /^End session ?Safari on iOS, 198\.51\.100\.4$/ }),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument());
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false);
  });

  it('reports a session that could not be ended', async () => {
    mockFetch(
      api([current, phone], (call) =>
        call.method === 'DELETE'
          ? apiError(404, 'not_found', 'No such session. It may have ended already.')
          : undefined,
      ),
    );
    renderApp('/account');
    fireEvent.click(
      await screen.findByRole('button', { name: /^End session ?Safari on iOS, 198\.51\.100\.4$/ }),
    );
    fireEvent.click(
      within(screen.getByRole('alertdialog')).getByRole('button', { name: 'End session' }),
    );
    expect(await screen.findByText('The session could not be ended')).toBeInTheDocument();
    expect(screen.getByText('No such session. It may have ended already.')).toBeInTheDocument();
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
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
    await sessionsList();
    fireEvent.click(screen.getByRole('button', { name: 'End all other sessions' }));
    const dialog = screen.getByRole('alertdialog', { name: 'End all other sessions?' });
    expect(within(dialog).getByText(/\(2 sessions\)/)).toBeInTheDocument();
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false);
    fireEvent.click(within(dialog).getByRole('button', { name: 'End other sessions' }));
    expect(await screen.findByText('Other sessions ended')).toBeInTheDocument();
    expect(
      screen.getByText('2 sessions signed out. This browser stays signed in.'),
    ).toBeInTheDocument();
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
    expect(calls.find((c) => c.method === 'DELETE')?.path).toBe('/api/v1/me/sessions');
  });

  it('cannot end others when this is the only session', async () => {
    mockFetch(api([current]));
    renderApp('/account');
    await sessionsList();
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

  it('chooses the theme from the appearance cards and keeps it', async () => {
    mockFetch(api([current]));
    renderApp('/account');
    const group = await screen.findByRole('group', { name: 'Theme' });
    const radio = (name: RegExp) => within(group).getByRole('radio', { name });
    expect(within(group).getAllByRole('radio')).toHaveLength(3);
    expect(radio(/^System/)).toBeChecked();

    fireEvent.click(radio(/^Light/));
    expect(radio(/^Light/)).toBeChecked();
    expect(radio(/^System/)).not.toBeChecked();
    expect(document.documentElement).toHaveAttribute('data-theme', 'light');
    expect(localStorage.getItem('scanopsis.theme')).toBe('light');

    fireEvent.click(radio(/^Dark/));
    expect(document.documentElement).toHaveAttribute('data-theme', 'dark');

    fireEvent.click(radio(/^System/));
    expect(document.documentElement).not.toHaveAttribute('data-theme');
    expect(localStorage.getItem('scanopsis.theme')).toBeNull();
  });
});
