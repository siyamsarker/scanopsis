// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import type { Setting } from '../api/hooks';
import {
  apiError,
  json,
  makeUser,
  mockFetch,
  renderApp,
  type Handler,
  type RecordedCall,
} from '../test-utils';

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
    description: 'Base URL of the GitLab instance.',
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
    description: 'Secret of the GitLab OAuth application.',
    env_var: 'SCANOPSIS_GITLAB_CLIENT_SECRET',
  }),
  setting({
    key: 'gitlab.allowed_groups',
    type: 'string_list',
    description: 'GitLab groups whose members may sign in.',
    value: ['platform', 'security'],
    source: 'ui',
    updated_by: 'ada',
    updated_at: new Date(Date.now() - 3 * 3600 * 1000).toISOString(),
  }),
  setting({
    key: 'auth.local_login_enabled',
    type: 'boolean',
    description: 'Whether users can sign in with a local username and password.',
    value: true,
  }),
  setting({
    key: 'session.idle_timeout',
    type: 'duration',
    description: 'How long a session may stay unused.',
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

const patches = (calls: RecordedCall[]) => calls.filter((c) => c.method === 'PATCH');

/** The sticky bar that appears while there are unsaved changes. */
const saveBar = () => screen.queryByRole('region', { name: 'Unsaved changes' });

function save() {
  const bar = saveBar();
  if (!bar) throw new Error('The save bar is not shown.');
  fireEvent.click(within(bar).getByRole('button', { name: 'Save changes' }));
}

describe('settings page', () => {
  it('shows an admin link in the navigation and groups the settings in sections', async () => {
    mockFetch(settingsApi());
    renderApp('/settings');
    expect(await screen.findByRole('link', { name: 'Settings' })).toHaveAttribute(
      'href',
      '/settings',
    );
    await screen.findByLabelText('GitLab URL');
    expect(screen.getAllByRole('heading', { level: 2 }).map((h) => h.textContent)).toEqual([
      'GitLab',
      'Sign-in',
      'Sessions',
    ]);
    const nav = screen.getByRole('navigation', { name: 'Settings sections' });
    const links = within(nav).getAllByRole('link');
    expect(links.map((l) => l.textContent?.trim())).toEqual(['GitLab', 'Sign-in', 'Sessions']);
    expect(links[2]).toHaveAttribute('href', '#section-sessions');
    expect(links[0]).toHaveAttribute('aria-current', 'true');
    fireEvent.click(links[2]);
    expect(links[2]).toHaveAttribute('aria-current', 'true');
    expect(links[0]).not.toHaveAttribute('aria-current');
  });

  it('orders SonarQube before scan uploads and retention', async () => {
    const extra = [
      ...initial,
      setting({
        key: 'ingest.protected_refs_only',
        type: 'boolean',
        value: false,
        description: 'Accept scan reports only from protected refs.',
      }),
      setting({ key: 'sonar.url', type: 'url', value: '', description: 'SonarQube address' }),
      setting({ key: 'retention.days', type: 'integer', value: 90, description: 'Days kept' }),
    ];
    mockFetch(settingsApi(undefined, extra));
    renderApp('/settings');
    await screen.findByLabelText('GitLab URL');
    expect(screen.getAllByRole('heading', { level: 2 }).map((h) => h.textContent)).toEqual([
      'GitLab',
      'Sign-in',
      'Sessions',
      'SonarQube',
      'Scan uploads',
      'Retention',
    ]);
    expect(screen.getByLabelText('SonarQube URL')).toBeInTheDocument();
    expect(
      screen.getByRole('switch', { name: 'Protected branches and tags only' }),
    ).not.toBeChecked();
  });

  it('labels each setting briefly, with the long description as help text and the key', async () => {
    const { container } = mockFetchAndRender();
    const url = await screen.findByLabelText('GitLab URL');
    expect(url).toHaveAttribute('type', 'url');
    expect(url).toHaveValue('https://gitlab.example.com');
    expect(url).toHaveAccessibleDescription('Base URL of the GitLab instance.');
    expect(screen.getByLabelText('Groups that may sign in')).toHaveValue('platform\nsecurity');
    expect(screen.getByLabelText('Groups that may sign in')).toHaveAccessibleDescription(
      'GitLab groups whose members may sign in. One per line.',
    );
    expect(screen.getByLabelText('Idle timeout')).toHaveValue('8h');
    expect(screen.getByRole('switch', { name: 'Local accounts' })).toBeChecked();
    expect(screen.getByText('gitlab.allowed_groups')).toBeVisible();
    expect(container.textContent).toContain('Changed 3 hours ago');
  });

  it('shows where each value comes from as a quiet tag with the details', async () => {
    mockFetch(settingsApi());
    renderApp('/settings');
    await screen.findByLabelText('GitLab URL');
    expect(screen.getAllByText('From environment')).toHaveLength(2);
    expect(screen.getAllByText('Set here')).toHaveLength(2);
    expect(screen.getAllByText('Default')).toHaveLength(1);
    expect(
      screen.getByRole('button', {
        name: 'From environment. Seeded by SCANOPSIS_GITLAB_URL. A value saved here overrides it.',
      }),
    ).toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: /^Set here\. Saved here by ada/ })).toHaveLength(
      2,
    );
  });

  it('shows the save bar only with unsaved changes, counts them and discards them', async () => {
    const { calls } = mockFetch(settingsApi(() => json(200, { items: initial })));
    renderApp('/settings');
    const idle = await screen.findByLabelText('Idle timeout');
    expect(saveBar()).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Save changes', hidden: true })).toBeDisabled();

    fireEvent.change(idle, { target: { value: '30m' } });
    expect(saveBar()).toHaveTextContent('1 unsaved change');
    expect(screen.getByText('Unsaved')).toBeVisible();
    fireEvent.click(screen.getByRole('switch', { name: 'Local accounts' }));
    expect(saveBar()).toHaveTextContent('2 unsaved changes');
    const nav = screen.getByRole('navigation', { name: 'Settings sections' });
    expect(within(nav).getByRole('link', { name: /^Sessions\s?1\s?unsaved$/ })).toBeInTheDocument();

    // Typing the saved value again is not a change.
    fireEvent.change(idle, { target: { value: '8h' } });
    expect(saveBar()).toHaveTextContent('1 unsaved change');

    fireEvent.click(within(saveBar()!).getByRole('button', { name: 'Discard' }));
    expect(saveBar()).not.toBeInTheDocument();
    expect(screen.getByRole('switch', { name: 'Local accounts' })).toBeChecked();
    expect(patches(calls)).toHaveLength(0);
  });

  it('undoes the change of one setting', async () => {
    mockFetch(settingsApi());
    renderApp('/settings');
    const idle = await screen.findByLabelText('Idle timeout');
    fireEvent.change(idle, { target: { value: '30m' } });
    fireEvent.click(screen.getByRole('button', { name: 'Undo Idle timeout' }));
    expect(idle).toHaveValue('8h');
    expect(saveBar()).not.toBeInTheDocument();
  });

  it('sends only the changed keys and confirms with a toast', async () => {
    const { calls } = mockFetch(
      settingsApi(() =>
        json(200, {
          items: initial.map((s) =>
            s.key === 'session.idle_timeout' ? { ...s, value: '30m' } : s,
          ),
        }),
      ),
    );
    renderApp('/settings');
    fireEvent.change(await screen.findByLabelText('Idle timeout'), { target: { value: '30m' } });
    const groups = screen.getByLabelText('Groups that may sign in');
    fireEvent.change(groups, { target: { value: 'platform\nsecurity\n  ops \n\n' } });
    fireEvent.change(groups, { target: { value: 'platform\nsecurity' } });
    save();
    expect(await screen.findByText('Settings saved')).toBeVisible();
    expect(screen.getByText('The change applies now.')).toBeVisible();
    expect(patches(calls)).toHaveLength(1);
    expect(patches(calls)[0].body).toEqual({ 'session.idle_timeout': '30m' });
    expect(saveBar()).not.toBeInTheDocument();
  });

  it('turns list lines into an array and the switch into a boolean', async () => {
    const { calls } = mockFetch(settingsApi(() => json(200, { items: initial })));
    renderApp('/settings');
    fireEvent.change(await screen.findByLabelText('Groups that may sign in'), {
      target: { value: 'platform\n  ops \n\n' },
    });
    const local = screen.getByRole('switch', { name: 'Local accounts' });
    fireEvent.click(local);
    expect(local).not.toBeChecked();
    save();
    expect(await screen.findByText('2 changes apply now.')).toBeVisible();
    expect(patches(calls)[0].body).toEqual({
      'gitlab.allowed_groups': ['platform', 'ops'],
      'auth.local_login_enabled': false,
    });
  });

  it('warns before local accounts are turned off', async () => {
    mockFetch(settingsApi());
    renderApp('/settings');
    const toggle = await screen.findByRole('switch', { name: 'Local accounts' });
    expect(screen.queryByText(/Make sure GitLab sign-in works/)).not.toBeInTheDocument();
    fireEvent.click(toggle);
    expect(screen.getByText(/Make sure GitLab sign-in works/)).toBeVisible();
    fireEvent.click(toggle);
    expect(screen.queryByText(/Make sure GitLab sign-in works/)).not.toBeInTheDocument();
  });

  it('never shows a secret and sends it only when typed', async () => {
    const { calls } = mockFetch(settingsApi(() => json(200, { items: initial })));
    const { container } = renderApp('/settings');
    const secret = await screen.findByLabelText('Application secret');
    expect(secret).toHaveAttribute('type', 'password');
    expect(secret).toHaveValue('');
    expect(screen.getByText('A value is saved')).toBeVisible();
    expect(secret).toHaveAccessibleDescription(
      'Secret of the GitLab OAuth application. The saved value is never shown.',
    );

    // Saving without typing sends nothing for the secret.
    fireEvent.change(screen.getByLabelText('Idle timeout'), { target: { value: '1h' } });
    save();
    await waitFor(() => expect(patches(calls)).toHaveLength(1));
    expect(patches(calls)[0].body).toEqual({ 'session.idle_timeout': '1h' });
    await waitFor(() => expect(saveBar()).not.toBeInTheDocument());

    fireEvent.change(screen.getByLabelText('Application secret'), {
      target: { value: 'hunter2' },
    });
    save();
    await waitFor(() => expect(patches(calls)).toHaveLength(2));
    expect(patches(calls)[1].body).toEqual({ 'gitlab.client_secret': 'hunter2' });
    await waitFor(() => expect(screen.getByLabelText('Application secret')).toHaveValue(''));
    expect(container.textContent).not.toMatch(/hunter2/);
  });

  it('says when a secret has no value', async () => {
    mockFetch(
      settingsApi(undefined, [
        setting({
          key: 'gitlab.client_secret',
          secret: true,
          value: null,
          is_set: false,
          description: 'Secret of the GitLab OAuth application.',
        }),
      ]),
    );
    renderApp('/settings');
    expect(await screen.findByLabelText('Application secret')).toHaveAttribute(
      'placeholder',
      'Not set',
    );
    expect(screen.getByText('No value saved')).toBeVisible();
  });

  it('resets a setting by sending null, and the reset can be undone', async () => {
    const { calls } = mockFetch(settingsApi(() => json(200, { items: initial })));
    renderApp('/settings');
    await screen.findByLabelText('Idle timeout');
    // Only values saved here can be reset.
    expect(
      screen.queryByRole('button', { name: /^Reset to environment or default\s?for GitLab URL$/ }),
    ).not.toBeInTheDocument();
    const reset = () =>
      fireEvent.click(
        screen.getByRole('button', {
          name: /^Reset to environment or default\s?for Idle timeout$/,
        }),
      );
    reset();
    expect(screen.getByText('Resets on save')).toBeVisible();
    expect(saveBar()).toHaveTextContent('1 unsaved change');
    fireEvent.click(screen.getByRole('button', { name: 'Undo Idle timeout' }));
    expect(saveBar()).not.toBeInTheDocument();
    reset();
    save();
    await waitFor(() => expect(patches(calls)).toHaveLength(1));
    expect(patches(calls)[0].body).toEqual({ 'session.idle_timeout': null });
  });

  it('shows field errors from the server next to the fields and keeps the edits', async () => {
    mockFetch(
      settingsApi(() =>
        json(400, {
          error: {
            code: 'invalid_settings',
            message: 'Some settings are not valid.',
            fields: { 'session.idle_timeout': 'Use a duration such as 8h.' },
          },
        }),
      ),
    );
    renderApp('/settings');
    const input = await screen.findByLabelText('Idle timeout');
    fireEvent.change(input, { target: { value: 'soon' } });
    save();
    const message = await screen.findByText('Use a duration such as 8h.');
    expect(input).toHaveAttribute('aria-invalid', 'true');
    expect(input.getAttribute('aria-describedby')).toContain(message.closest('p')!.id);
    expect(input).toHaveValue('soon');
    expect(screen.getByText('Nothing was saved.')).toBeVisible();
    expect(screen.getByText('Fix the settings marked in red, then save again.')).toBeVisible();
    const nav = screen.getByRole('navigation', { name: 'Settings sections' });
    expect(within(nav).getByRole('link', { name: /^Sessions\s?1\s?to fix$/ })).toBeInTheDocument();
    // The edit is kept, still unsaved.
    expect(saveBar()).toHaveTextContent('1 unsaved change');
  });

  it('reports other failures in the save bar and keeps the edits', async () => {
    mockFetch(settingsApi(() => apiError(500, 'internal', 'Something broke.')));
    renderApp('/settings');
    fireEvent.change(await screen.findByLabelText('Idle timeout'), { target: { value: '1h' } });
    save();
    expect(await screen.findByText('Nothing was saved.')).toBeVisible();
    expect(screen.getByText('Something broke.')).toBeVisible();
    expect(screen.getByLabelText('Idle timeout')).toHaveValue('1h');
    expect(saveBar()).toHaveTextContent('1 unsaved change');
  });

  describe('integer settings', () => {
    const retention = setting({
      key: 'retention.days',
      type: 'integer',
      description: 'How many days scan history is kept.',
      value: 90,
      env_var: 'SCANOPSIS_RETENTION_DAYS',
    });
    const withRetention = [...initial, retention];

    it('shows a number input in days with its limits in a Retention section', async () => {
      mockFetch(settingsApi(undefined, withRetention));
      renderApp('/settings');
      const input = await screen.findByLabelText('Keep scan history for');
      expect(input).toHaveAttribute('type', 'number');
      expect(input).toHaveAttribute('min', '7');
      expect(input).toHaveAttribute('max', '3650');
      expect(input).toHaveAttribute('step', '1');
      expect(input).toHaveValue(90);
      expect(screen.getByText('days')).toBeVisible();
      expect(screen.getByRole('heading', { level: 2, name: 'Retention' })).toBeInTheDocument();
    });

    it('sends the value as a number', async () => {
      const { calls } = mockFetch(
        settingsApi(() => json(200, { items: withRetention }), withRetention),
      );
      renderApp('/settings');
      fireEvent.change(await screen.findByLabelText('Keep scan history for'), {
        target: { value: '365' },
      });
      save();
      await waitFor(() => expect(patches(calls)).toHaveLength(1));
      expect(patches(calls)[0].body).toEqual({ 'retention.days': 365 });
    });

    it.each(['3', '4000', '', '12.5'])(
      'does not send %j and says which numbers are allowed',
      async (value) => {
        const { calls } = mockFetch(
          settingsApi(() => json(200, { items: withRetention }), withRetention),
        );
        renderApp('/settings');
        const input = await screen.findByLabelText('Keep scan history for');
        fireEvent.change(input, { target: { value } });
        save();
        const message = await screen.findByText('Use a whole number from 7 to 3650.');
        expect(input).toHaveAttribute('aria-invalid', 'true');
        expect(input.getAttribute('aria-describedby')).toContain(message.closest('p')!.id);
        expect(patches(calls)).toHaveLength(0);
        // A new value clears the message.
        fireEvent.change(input, { target: { value: '30' } });
        expect(screen.queryByText('Use a whole number from 7 to 3650.')).not.toBeInTheDocument();
      },
    );

    it('shows a message the server returns for the setting', async () => {
      mockFetch(
        settingsApi(
          () =>
            json(400, {
              error: {
                code: 'invalid_settings',
                message: 'Some settings are not valid.',
                fields: { 'retention.days': 'Must be a whole number from 7 to 3650.' },
              },
            }),
          withRetention,
        ),
      );
      renderApp('/settings');
      const input = await screen.findByLabelText('Keep scan history for');
      fireEvent.change(input, { target: { value: '30' } });
      save();
      expect(await screen.findByText('Must be a whole number from 7 to 3650.')).toBeVisible();
      expect(input).toHaveAttribute('aria-invalid', 'true');
    });
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

function mockFetchAndRender() {
  mockFetch(settingsApi());
  return renderApp('/settings');
}
