// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { json, makeUser, mockFetch, renderApp, type Handler } from '../../test-utils';

const event = (id: number, extra: Record<string, unknown> = {}) => ({
  id,
  at: '2026-11-19T09:00:00Z',
  actor: 'ada',
  action: 'login.failed',
  detail: { ip: '10.0.0.1' },
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

describe('audit page', () => {
  it('tells viewers they need the admin role and requests nothing admin', async () => {
    const { calls } = mockFetch((call) =>
      call.path === '/api/v1/me' ? json(200, makeUser({ role: 'viewer' })) : undefined,
    );
    renderApp('/admin/audit');
    expect(await screen.findByText('You need the admin role to see this page.')).toBeVisible();
    expect(calls.some((c) => c.path.includes('/admin/'))).toBe(false);
  });

  it('shows time, actor, action with a plain label and details', async () => {
    mockFetch(api());
    renderApp('/admin/audit');
    const row = await screen.findByRole('row', { name: /login\.failed/ });
    expect(within(row).getByText('ada')).toBeInTheDocument();
    expect(within(row).getByText('Sign-in failed')).toBeInTheDocument();
    expect(within(row).getByText('ip:')).toBeInTheDocument();
    expect(within(row).getByText('10.0.0.1')).toBeInTheDocument();
    expect(within(row).getByText(/2026/)).toBeInTheDocument();
  });

  it('reads filters from the URL, sends them, and writes changes back', async () => {
    const { calls } = mockFetch(api());
    renderApp('/admin/audit?action=login.failed&actor=ada');
    await screen.findByRole('row', { name: /login\.failed/ });
    expect(calls.some((c) => c.path === '/api/v1/admin/audit?action=login.failed&actor=ada')).toBe(
      true,
    );
    expect(screen.getByLabelText('Action')).toHaveValue('login.failed');
    expect(screen.getByLabelText('Filter by actor')).toHaveValue('ada');

    fireEvent.change(screen.getByLabelText('Action'), { target: { value: '' } });
    await waitFor(() =>
      expect(screen.getByTestId('location')).toHaveTextContent('/admin/audit?actor=ada'),
    );
    const search = screen.getByLabelText('Filter by actor');
    fireEvent.change(search, { target: { value: 'grace' } });
    fireEvent.keyDown(search, { key: 'Enter' });
    await waitFor(() =>
      expect(screen.getByTestId('location')).toHaveTextContent('/admin/audit?actor=grace'),
    );
    await waitFor(() =>
      expect(calls.some((c) => c.path === '/api/v1/admin/audit?actor=grace')).toBe(true),
    );
  });

  it('loads more with next_before', async () => {
    const { calls } = mockFetch(
      api((call) => {
        if (call.path === '/api/v1/admin/audit') {
          return json(200, { items: [event(10)], next_before: 10 });
        }
        if (call.path === '/api/v1/admin/audit?before=10') {
          return json(200, { items: [event(9, { actor: 'grace' })], next_before: null });
        }
        return undefined;
      }),
    );
    renderApp('/admin/audit');
    await screen.findByText('ada');
    fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
    expect(await screen.findByText('grace')).toBeInTheDocument();
    expect(calls.some((c) => c.path === '/api/v1/admin/audit?before=10')).toBe(true);
    expect(screen.getAllByRole('row')).toHaveLength(3);
  });

  it('renders details as escaped text and truncates long values', async () => {
    const long = 'x'.repeat(200);
    const html = '<img src=x onerror=alert(1)>';
    mockFetch(
      api((call) =>
        call.path.startsWith('/api/v1/admin/audit')
          ? json(200, {
              items: [event(1, { detail: { note: html, long, nested: { a: 1 }, ok: true } })],
              next_before: null,
            })
          : undefined,
      ),
    );
    const { container } = renderApp('/admin/audit');
    expect(await screen.findByText(html)).toBeInTheDocument();
    expect(container.querySelector('img')).toBeNull();
    const cut = screen.getByText(`${'x'.repeat(80)}…`);
    expect(cut).toHaveAttribute('title', long);
    expect(screen.getByText('{"a":1}')).toBeInTheDocument();
    expect(screen.getByText('true')).toBeInTheDocument();
  });

  it('shows an empty state for no events', async () => {
    mockFetch(
      api((call) =>
        call.path.startsWith('/api/v1/admin/audit')
          ? json(200, { items: [], next_before: null })
          : undefined,
      ),
    );
    renderApp('/admin/audit?actor=nobody');
    expect(await screen.findByText('No events match these filters.')).toBeInTheDocument();
  });
});
