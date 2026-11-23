// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { configure, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { chooseOption, json, makeUser, mockFetch, renderApp, type Handler } from '../../test-utils';
import { describeActor } from '../AuditPage';
import { AuditDetails, ChangeList, describeEvent } from './AuditDetails';

configure({ asyncUtilTimeout: 5000 });

/** Noon local time, `daysAgo` days back, so day grouping never straddles midnight. */
function noon(daysAgo: number, hour = 12) {
  const d = new Date();
  d.setDate(d.getDate() - daysAgo);
  d.setHours(hour, 0, 0, 0);
  return d.toISOString();
}

const event = (id: number, extra: Record<string, unknown> = {}) => ({
  id,
  at: noon(0),
  actor: 'gitlab:ada',
  action: 'login.failed',
  detail: { method: 'gitlab', reason: 'group_not_allowed', ip: '10.0.0.1' },
  ...extra,
});

function api(extra?: Handler): Handler {
  return async (call) => {
    const custom = await extra?.(call);
    if (custom) return custom;
    if (call.path === '/api/v1/me') return json(200, makeUser());
    if (call.path.startsWith('/api/v1/admin/audit')) {
      return json(200, { items: [event(10)], next_before: null });
    }
    return undefined;
  };
}

/** The events shown, as list items of the day sections. */
function shownEvents() {
  return screen
    .getAllByRole('region', { name: /\d+ events?$/ })
    .flatMap((day) => within(day).getAllByRole('listitem'));
}

describe('audit page', () => {
  it('tells viewers they need the admin role and requests nothing admin', async () => {
    const { calls } = mockFetch((call) =>
      call.path === '/api/v1/me' ? json(200, makeUser({ role: 'viewer' })) : undefined,
    );
    renderApp('/admin/audit');
    expect(await screen.findByText('You need the admin role to see this page.')).toBeVisible();
    expect(calls.some((c) => c.path.includes('/admin/'))).toBe(false);
  });

  it('shows each event as time, actor and a plain sentence, with the remaining details', async () => {
    mockFetch(api());
    renderApp('/admin/audit');
    const today = await screen.findByRole('region', { name: /^Today ?1 event$/ });
    const [item] = within(today).getAllByRole('listitem');
    expect(within(item).getByText('12:00')).toHaveAttribute('datetime', noon(0));
    expect(within(item).getByText('ada')).toHaveAttribute('title', 'GitLab account: gitlab:ada');
    // The action's plain label is there for screen readers next to its icon.
    expect(within(item).getByText('Sign-in failed')).toHaveClass('sr-only');
    expect(item).toHaveTextContent('could not sign in with GitLab: group_not_allowed');
    // The address is not in the sentence, so it is listed underneath.
    expect(within(item).getByText('Address')).toBeInTheDocument();
    expect(within(item).getByText('10.0.0.1')).toBeInTheDocument();
    expect(within(item).queryByText('reason')).not.toBeInTheDocument();
  });

  it('groups events by day, newest first, with headings for today, yesterday and older days', async () => {
    const older = noon(5, 9);
    mockFetch(
      api((call) =>
        call.path === '/api/v1/admin/audit'
          ? json(200, {
              // Ids and times disagree slightly, as they can: the time decides the order.
              items: [
                event(4, { at: noon(1, 8), actor: 'local:root', action: 'logout', detail: {} }),
                event(5, { at: noon(0, 9) }),
                event(3, { at: noon(0, 10), actor: 'local:root', action: 'logout', detail: {} }),
                event(2, {
                  at: older,
                  actor: 'retention',
                  action: 'retention.deleted',
                  detail: { scans: 3 },
                }),
              ],
              next_before: null,
            })
          : undefined,
      ),
    );
    renderApp('/admin/audit');
    await screen.findByRole('region', { name: /^Today ?2 events$/ });
    const days = screen.getAllByRole('region', { name: /\d+ events?$/ });
    const title = new Intl.DateTimeFormat('en-GB', {
      weekday: 'long',
      day: 'numeric',
      month: 'long',
      ...(new Date(older).getFullYear() !== new Date().getFullYear() ? { year: 'numeric' } : {}),
    }).format(new Date(older));
    expect(days.map((d) => within(d).getByRole('heading').textContent)).toEqual([
      'Today 2 events',
      'Yesterday 1 event',
      `${title} 1 event`,
    ]);
    const today = within(days[0]).getAllByRole('listitem');
    expect(today[0]).toHaveTextContent('10:00');
    expect(today[1]).toHaveTextContent('09:00');
    expect(days[2]).toHaveTextContent('Retention');
    expect(days[2]).toHaveTextContent('deleted old data: 3 scans');
  });

  it('shows a settings change as a before and after view', async () => {
    mockFetch(
      api((call) =>
        call.path === '/api/v1/admin/audit'
          ? json(200, {
              items: [
                event(7, {
                  actor: 'local:root',
                  action: 'settings.changed',
                  detail: {
                    keys: ['session.idle_timeout', 'gitlab.allowed_groups', 'sonar.token'],
                    changes: [
                      { key: 'session.idle_timeout', from: '30m', to: '45m' },
                      {
                        key: 'gitlab.allowed_groups',
                        from: ['platform'],
                        to: ['platform', 'data'],
                      },
                      { key: 'sonar.token', changed: true },
                    ],
                  },
                }),
              ],
              next_before: null,
            })
          : undefined,
      ),
    );
    renderApp('/admin/audit');
    const [item] = within(
      await screen.findByRole('region', { name: /^Today ?1 event$/ }),
    ).getAllByRole('listitem');
    expect(item).toHaveTextContent(/root\s*\(Local account\)\s*changed 3 settings/);
    const terms = within(item)
      .getAllByRole('term')
      .map((t) => t.textContent);
    expect(terms).toEqual(['Idle timeout', 'Groups that may sign in', 'Access token']);
    const values = within(item)
      .getAllByRole('definition')
      .map((d) => d.textContent);
    expect(values).toEqual([
      'from 30m to 45m',
      'from platform to platform, data',
      'Changed; secret values are never recorded',
    ]);
    // keys and changes are said in the view, so they are not repeated as raw details.
    expect(within(item).queryByText('keys')).not.toBeInTheDocument();
  });

  it('reads filters from the URL, sends them, writes changes back and clears them', async () => {
    const { calls } = mockFetch(api());
    renderApp('/admin/audit?action=login.failed&actor=ada');
    await screen.findByRole('region', { name: /^Today ?1 event$/ });
    expect(calls.some((c) => c.path === '/api/v1/admin/audit?action=login.failed&actor=ada')).toBe(
      true,
    );
    expect(screen.getByRole('combobox', { name: 'Action' })).toHaveTextContent('Sign-in failed');
    expect(screen.getByLabelText('Actor')).toHaveValue('ada');

    await chooseOption('Action', 'All actions');
    await waitFor(() =>
      expect(screen.getByTestId('location')).toHaveTextContent('/admin/audit?actor=ada'),
    );
    const search = screen.getByLabelText('Actor');
    fireEvent.change(search, { target: { value: 'grace' } });
    fireEvent.keyDown(search, { key: 'Enter' });
    await waitFor(() =>
      expect(screen.getByTestId('location')).toHaveTextContent('/admin/audit?actor=grace'),
    );
    await waitFor(() =>
      expect(calls.some((c) => c.path === '/api/v1/admin/audit?actor=grace')).toBe(true),
    );

    fireEvent.click(screen.getByRole('button', { name: 'Clear filters' }));
    await waitFor(() =>
      expect(screen.getByTestId('location')).toHaveTextContent(/^\/admin\/audit$/),
    );
    expect(screen.queryByRole('button', { name: 'Clear filters' })).not.toBeInTheDocument();
    expect(screen.getByLabelText('Actor')).toHaveValue('');
  });

  it('loads more with next_before', async () => {
    const { calls } = mockFetch(
      api((call) => {
        if (call.path === '/api/v1/admin/audit') {
          return json(200, { items: [event(10)], next_before: 10 });
        }
        if (call.path === '/api/v1/admin/audit?before=10') {
          return json(200, {
            items: [event(9, { actor: 'gitlab:grace', at: noon(0, 11) })],
            next_before: null,
          });
        }
        return undefined;
      }),
    );
    renderApp('/admin/audit');
    await screen.findByText('ada');
    fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
    expect(await screen.findByText('grace')).toBeInTheDocument();
    expect(calls.some((c) => c.path === '/api/v1/admin/audit?before=10')).toBe(true);
    expect(shownEvents()).toHaveLength(2);
    expect(screen.getByText('All 2 events shown')).toBeInTheDocument();
  });

  it('renders details as escaped text and truncates long values', async () => {
    const long = 'x'.repeat(200);
    const html = '<img src=x onerror=alert(1)>';
    mockFetch(
      api((call) =>
        call.path.startsWith('/api/v1/admin/audit')
          ? json(200, {
              items: [
                event(1, {
                  action: 'something.new',
                  detail: { note: html, long, nested: { a: 1 }, ok: true },
                }),
              ],
              next_before: null,
            })
          : undefined,
      ),
    );
    const { container } = renderApp('/admin/audit');
    expect(await screen.findByText(html)).toBeInTheDocument();
    expect(container.querySelector('main img, section img')).toBeNull();
    const cut = screen.getByText(`${'x'.repeat(80)}…`);
    expect(cut).toHaveAttribute('title', long);
    expect(screen.getByText('{"a":1}')).toBeInTheDocument();
    expect(screen.getByText('On')).toBeInTheDocument();
    // An action this version does not know reads as its code.
    expect(screen.getAllByText('something.new').length).toBeGreaterThan(0);
  });

  it('shows an empty state with and without filters', async () => {
    mockFetch(
      api((call) =>
        call.path.startsWith('/api/v1/admin/audit')
          ? json(200, { items: [], next_before: null })
          : undefined,
      ),
    );
    renderApp('/admin/audit?actor=nobody');
    expect(await screen.findByText('No events match')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Clear filters' }));
    expect(await screen.findByText('No events yet')).toBeInTheDocument();
  });

  it('shows an error state with retry', async () => {
    let fail = true;
    mockFetch(
      api((call) =>
        call.path.startsWith('/api/v1/admin/audit') && fail
          ? json(500, { error: { code: 'internal_error', message: 'Database down.' } })
          : undefined,
      ),
    );
    renderApp('/admin/audit');
    expect(await screen.findByText('The audit log could not be loaded')).toBeInTheDocument();
    fail = false;
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByRole('region', { name: /^Today ?1 event$/ })).toBeInTheDocument();
  });
});

describe('audit wording', () => {
  const sentence = (action: string, detail: Record<string, unknown>) => {
    const { container, unmount } = render(<p>{describeEvent(action, detail).sentence}</p>);
    const text = container.textContent;
    unmount();
    return text;
  };

  it('names the actor by kind', () => {
    expect(describeActor('local:root')).toMatchObject({ name: 'root', kind: 'Local account' });
    expect(describeActor('gitlab:ada')).toMatchObject({ name: 'ada', kind: 'GitLab account' });
    expect(describeActor('ci:platform/payments')).toMatchObject({
      name: 'platform/payments',
      kind: 'GitLab CI pipeline',
    });
    expect(describeActor('sonarqube:unknown')).toMatchObject({
      name: 'SonarQube',
      kind: 'Unverified webhook sender',
    });
    expect(describeActor('cli')).toMatchObject({ name: 'Command line' });
    expect(describeActor('ingest')).toMatchObject({ name: 'Scan upload' });
    expect(describeActor('someone')).toEqual({ name: 'someone', kind: '' });
    // Automated actors get an icon instead of initials.
    expect(describeActor('retention').icon).toBeDefined();
    expect(describeActor('local:root').icon).toBeUndefined();
  });

  it('says what happened in plain words', () => {
    expect(sentence('login.succeeded', { method: 'local' })).toBe('signed in with a password');
    expect(sentence('session.revoked', { scope: 'others', count: 2 })).toBe(
      'ended 2 other sessions',
    );
    expect(sentence('session.revoked', { scope: 'one' })).toBe('ended one of their sessions');
    expect(sentence('user.disabled', { username: 'dave' })).toBe('disabled dave');
    expect(sentence('mfa.verified', { method: 'recovery' })).toBe(
      'passed two-factor with a recovery code',
    );
    expect(sentence('service.deleted', { gitlab_project_path: 'a/b' })).toBe('deleted a/b');
    expect(sentence('ingest.rejected', { reason: 'ref_not_protected', project_path: 'a/b' })).toBe(
      'refused: branch not protected, from a/b',
    );
    expect(sentence('retention.deleted', { scans: 1, sonar_analyses: 2 })).toBe(
      'deleted old data: 1 scan, 2 analyses',
    );
    expect(sentence('retention.deleted', {})).toBe('found no old data to delete');
    expect(sentence('retention.deleted', { scans: 0, raw_reports: 360000 })).toBe(
      'deleted old data: 360,000 raw reports',
    );
    expect(
      sentence('retention.deleted', {
        scans: 1,
        raw_reports: 2,
        sonar_analyses: 2,
        sonar_deliveries: 1,
        audit_events: 3,
      }),
    ).toBe(
      'deleted old data: 1 scan, 2 raw reports, 2 analyses, 1 SonarQube delivery, 3 audit events',
    );
    expect(sentence('user.deleted', { username: 'eve' })).toBe('deleted eve');
  });

  it('marks failures and destructive actions by tone', () => {
    expect(describeEvent('login.failed', {}).tone).toBe('warn');
    expect(describeEvent('user.deleted', {}).tone).toBe('fail');
    expect(describeEvent('logout', {}).tone).toBe('neutral');
  });

  it('turns role and dashboard branch changes into before and after rows', () => {
    expect(describeEvent('user.role_changed', { from: 'viewer', to: 'admin' }).changes).toEqual([
      { label: 'Role', from: 'Viewer', to: 'Admin' },
    ]);
    const updated = describeEvent('service.updated', {
      gitlab_project_path: 'a/b',
      fields: ['default_ref', 'name'],
      default_ref: { from: null, to: 'main' },
    });
    expect(updated.changes).toEqual([
      { label: 'Dashboard branch', from: 'Latest scanned', to: 'main', mono: true },
    ]);
    render(<ChangeList changes={updated.changes!} />);
    expect(screen.getByRole('definition')).toHaveTextContent('from Latest scanned to main');
  });

  it('lists only the details the sentence did not use', () => {
    render(
      <AuditDetails
        detail={{ username: 'x', ip: '1.2.3.4', user_agent: 'curl' }}
        used={['username']}
      />,
    );
    expect(screen.getAllByRole('term').map((t) => t.textContent)).toEqual([
      'Address',
      'user agent',
    ]);
  });

  it('shows a time in a detail as a date, with the exact value in the tooltip', () => {
    render(<AuditDetails detail={{ scan_cutoff: '2026-08-24T15:58:51Z', raw_reports: 32 }} />);
    const time = screen.getByText((_, el) => el?.tagName === 'TIME');
    expect(time).toHaveAttribute('dateTime', '2026-08-24T15:58:51Z');
    expect(time).toHaveAttribute('title', '2026-08-24T15:58:51Z');
    expect(time.textContent).toMatch(/^24 Aug 2026, \d{2}:\d{2} /);
    expect(screen.getByText('32')).toBeInTheDocument();
  });
});
