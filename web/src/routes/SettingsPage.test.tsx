// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { fireEvent, screen } from '@testing-library/react';
import type { Setting } from '../api/hooks';
import { apiError, json, makeUser, mockFetch, renderApp, type Handler } from '../test-utils';

function setting(overrides: Partial<Setting>): Setting {
  return {
    key: 'x.y',
    type: 'string',
    secret: false,
    source: 'default',
    value: '',
    is_set: true,
    env_var: 'SCANOPSIS_X_Y',
    description: 'A setting',
    updated_by: null,
    updated_at: null,
    ...overrides,
  };
}

const initial: Setting[] = [
  setting({
    key: 'gitlab.url',
    type: 'url',
    description: 'GitLab address',
    value: 'https://gitlab.example.com',
    source: 'env',
    env_var: 'SCANOPSIS_GITLAB_URL',
  }),
  setting({
    key: 'gitlab.client_secret',
    secret: true,
    value: null,
    is_set: true,
    source: 'env',
    description: 'GitLab client secret',
    env_var: 'SCANOPSIS_GITLAB_CLIENT_SECRET',
  }),
  setting({
    key: 'gitlab.allowed_groups',
    type: 'string_list',
    description: 'Allowed GitLab groups',
    value: ['platform', 'security'],
    source: 'ui',
    updated_by: 'ada',
    updated_at: new Date(Date.now() - 3 * 3600 * 1000).toISOString(),
  }),
  setting({
    key: 'auth.local_enabled',
    type: 'boolean',
    description: 'Allow local sign-in',
    value: true,
  }),
  setting({
    key: 'session.ttl',
    type: 'duration',
    description: 'Session lifetime',
    value: '8h',
    source: 'ui',
    updated_by: 'ada',
    updated_at: new Date().toISOString(),
  }),
];

function settingsApi(patch?: Handler, items: Setting[] = initial): Handler {
  return async (call) => {
    if (call.path === '/api/v1/setup') return json(200, { required: false });
    if (call.path === '/api/v1/me') return json(200, makeUser({ role: 'admin' }));
    if (call.path === '/api/v1/settings' && call.method === 'GET') return json(200, { items });
    if (call.path === '/api/v1/settings' && call.method === 'PATCH') return patch?.(call);
    return undefined;
  };
}

const patches = (calls: { method: string; body: unknown }[]) =>
  calls.filter((c) => c.method === 'PATCH');

describe('settings page', () => {
  it('shows an admin link in the navigation and groups the settings', async () => {
    mockFetch(settingsApi());
    renderApp('/settings');
    expect(await screen.findByRole('link', { name: 'Settings' })).toHaveAttribute(
      'href',
      '/settings',
    );
    await screen.findByLabelText('GitLab address');
    expect(screen.getAllByRole('heading', { level: 2 }).map((h) => h.textContent)).toEqual([
      'GitLab',
      'Sign-in',
      'Sessions',
    ]);
  });

  it('groups SonarQube and scan upload settings separately', async () => {
    const extra = [
      ...initial,
      setting({
        key: 'ingest.protected_refs_only',
        type: 'boolean',
        value: false,
        description: 'Protected refs only',
      }),
      setting({
        key: 'sonar.url',
        type: 'url',
        value: '',
        is_set: false,
        description: 'SonarQube address',
      }),
    ];
    mockFetch(settingsApi(undefined, extra));
    renderApp('/settings');
    await screen.findByLabelText('GitLab address');
    expect(screen.getAllByRole('heading', { level: 2 }).map((h) => h.textContent)).toEqual([
      'GitLab',
      'Sign-in',
      'Sessions',
      'Scan uploads',
      'SonarQube',
    ]);
  });

  it('renders an input for each type with its source', async () => {
    mockFetch(settingsApi());
    renderApp('/settings');
    expect(await screen.findByLabelText('GitLab address')).toHaveAttribute('type', 'url');
    expect(screen.getByLabelText('GitLab address')).toHaveValue('https://gitlab.example.com');
    expect(screen.getByLabelText('Allowed GitLab groups')).toHaveValue('platform\nsecurity');
    expect(screen.getByRole('switch', { name: /Allow local sign-in/ })).toBeChecked();
    expect(screen.getByLabelText('Session lifetime')).toHaveValue('8h');
    expect(screen.getByText(/Use a duration such as 8h or 30m/)).toBeVisible();
    expect(screen.getAllByText('Environment')).toHaveLength(2);
    expect(screen.getAllByText('Set here')).toHaveLength(2);
    expect(screen.getAllByText('Default')).toHaveLength(1);
    expect(screen.getAllByText(/Changed by ada/)).toHaveLength(2);
  });

  it('sends only the changed keys and shows a notice', async () => {
    const { calls } = mockFetch(
      settingsApi(() =>
        json(200, {
          items: initial.map((s) => (s.key === 'session.ttl' ? { ...s, value: '30m' } : s)),
        }),
      ),
    );
    renderApp('/settings');
    const save = await screen.findByRole('button', { name: 'Save changes' });
    expect(save).toBeDisabled();
    fireEvent.change(screen.getByLabelText('Session lifetime'), { target: { value: '30m' } });
    fireEvent.change(screen.getByLabelText('Allowed GitLab groups'), {
      target: { value: 'platform\nsecurity\n  ops \n\n' },
    });
    fireEvent.change(screen.getByLabelText('Allowed GitLab groups'), {
      target: { value: 'platform\nsecurity' },
    });
    fireEvent.click(save);
    expect(await screen.findByText(/Settings saved/)).toBeVisible();
    expect(patches(calls)).toHaveLength(1);
    expect(patches(calls)[0].body).toEqual({ 'session.ttl': '30m' });
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled();
  });

  it('turns list lines into an array and a toggle into a boolean', async () => {
    const { calls } = mockFetch(settingsApi(() => json(200, { items: initial })));
    renderApp('/settings');
    fireEvent.change(await screen.findByLabelText('Allowed GitLab groups'), {
      target: { value: 'platform\n  ops \n\n' },
    });
    fireEvent.click(screen.getByRole('switch', { name: /Allow local sign-in/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));
    await screen.findByText(/Settings saved/);
    expect(patches(calls)[0].body).toEqual({
      'gitlab.allowed_groups': ['platform', 'ops'],
      'auth.local_enabled': false,
    });
  });

  it('warns before local sign-in is turned off', async () => {
    mockFetch(settingsApi());
    renderApp('/settings');
    const toggle = await screen.findByRole('switch', { name: /Allow local sign-in/ });
    expect(screen.queryByText(/make sure GitLab sign-in works/)).not.toBeInTheDocument();
    fireEvent.click(toggle);
    expect(screen.getByText(/make sure GitLab sign-in works/)).toBeVisible();
    fireEvent.click(toggle);
    expect(screen.queryByText(/make sure GitLab sign-in works/)).not.toBeInTheDocument();
  });

  it('never shows a secret and sends it only when typed', async () => {
    const { calls } = mockFetch(settingsApi(() => json(200, { items: initial })));
    const { container } = renderApp('/settings');
    const secret = await screen.findByLabelText('GitLab client secret');
    expect(secret).toHaveAttribute('type', 'password');
    expect(secret).toHaveValue('');
    expect(screen.getByText('Set')).toBeVisible();
    expect(container.textContent).not.toMatch(/hunter2/);

    // Saving without typing sends nothing for the secret.
    fireEvent.change(screen.getByLabelText('Session lifetime'), { target: { value: '1h' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));
    await screen.findByText(/Settings saved/);
    expect(patches(calls)[0].body).toEqual({ 'session.ttl': '1h' });

    fireEvent.change(screen.getByLabelText('GitLab client secret'), {
      target: { value: 'hunter2' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));
    await screen.findByText(/Settings saved/);
    expect(patches(calls)[1].body).toEqual({ 'gitlab.client_secret': 'hunter2' });
    expect(container.textContent).not.toMatch(/hunter2/);
    expect(screen.getByLabelText('GitLab client secret')).toHaveValue('');
  });

  it('shows Not set for a secret without a value', async () => {
    mockFetch(
      settingsApi(undefined, [
        setting({
          key: 'gitlab.client_secret',
          secret: true,
          value: null,
          is_set: false,
          description: 'GitLab client secret',
        }),
      ]),
    );
    renderApp('/settings');
    await screen.findByLabelText('GitLab client secret');
    expect(screen.getByText('Not set')).toBeVisible();
  });

  it('resets a setting by sending null', async () => {
    const { calls } = mockFetch(settingsApi(() => json(200, { items: initial })));
    renderApp('/settings');
    await screen.findByLabelText('Session lifetime');
    fireEvent.click(
      screen.getByRole('button', { name: 'Reset to environment or default for session.ttl' }),
    );
    expect(screen.getByText('Will reset when you save.')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));
    await screen.findByText(/Settings saved/);
    expect(patches(calls)[0].body).toEqual({ 'session.ttl': null });
  });

  it('shows field errors next to the fields and keeps the edits', async () => {
    mockFetch(
      settingsApi(() =>
        json(400, {
          error: {
            code: 'invalid_settings',
            message: 'Some settings are not valid.',
            fields: { 'session.ttl': 'Use a duration such as 8h.' },
          },
        }),
      ),
    );
    renderApp('/settings');
    const input = await screen.findByLabelText('Session lifetime');
    fireEvent.change(input, { target: { value: 'soon' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));
    const message = await screen.findByText('Use a duration such as 8h.');
    expect(input).toHaveAttribute('aria-invalid', 'true');
    expect(input.getAttribute('aria-describedby')).toContain(message.id);
    expect(input).toHaveValue('soon');
    expect(screen.getByRole('alert')).toHaveTextContent('Some settings need attention');
  });

  it('shows a general error for other failures', async () => {
    mockFetch(settingsApi(() => apiError(500, 'internal', 'Something broke.')));
    renderApp('/settings');
    fireEvent.change(await screen.findByLabelText('Session lifetime'), { target: { value: '1h' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Something broke.');
  });

  it('tells viewers they need the admin role without requesting settings', async () => {
    const { calls } = mockFetch((call) =>
      call.path === '/api/v1/me'
        ? json(200, makeUser({ role: 'viewer' }))
        : call.path === '/api/v1/setup'
          ? json(200, { required: false })
          : undefined,
    );
    renderApp('/settings');
    expect(await screen.findByText('You need the admin role to see this page.')).toBeVisible();
    expect(calls.some((c) => c.path === '/api/v1/settings')).toBe(false);
    expect(screen.queryByRole('link', { name: 'Settings' })).not.toBeInTheDocument();
  });
});
