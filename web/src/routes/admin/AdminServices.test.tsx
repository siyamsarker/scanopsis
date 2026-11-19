// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { configure, fireEvent, screen, waitFor, within } from '@testing-library/react';
import type { Service } from '../../api/admin';
import {
  apiError,
  json,
  makeUser,
  mockFetch,
  noContent,
  renderApp,
  type Handler,
} from '../../test-utils';

const pay: Service = {
  id: 1,
  name: 'Payments',
  gitlab_project_id: 4711,
  gitlab_project_path: 'platform/payments',
  sonar_project_key: 'platform_payments',
  created_via: 'ingest',
  archived: false,
  created_at: '2026-11-01T10:00:00Z',
};
const docs: Service = {
  id: 2,
  name: 'Docs',
  gitlab_project_id: null,
  gitlab_project_path: 'platform/docs',
  sonar_project_key: null,
  created_via: 'manual',
  archived: false,
  created_at: '2026-11-02T10:00:00Z',
};

function api(extra?: Handler): Handler {
  return async (call) => {
    const custom = await extra?.(call);
    if (custom) return custom;
    if (call.path === '/api/v1/me') return json(200, makeUser());
    if (call.method === 'GET' && call.path.startsWith('/api/v1/services?')) {
      return json(200, { items: [pay, docs], next_after: null });
    }
    return undefined;
  };
}

configure({ asyncUtilTimeout: 5000 });

describe('admin services page', () => {
  it('tells viewers they need the admin role and requests no services', async () => {
    const { calls } = mockFetch((call) =>
      call.path === '/api/v1/me' ? json(200, makeUser({ role: 'viewer' })) : undefined,
    );
    renderApp('/admin/services');
    expect(await screen.findByText('You need the admin role to see this page.')).toBeVisible();
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
    expect(calls.some((c) => c.path.startsWith('/api/v1/services'))).toBe(false);
  });

  it('lists services with links, identifiers and placeholders', async () => {
    mockFetch(api());
    renderApp('/admin/services');
    const table = await screen.findByRole('table');
    expect(
      within(table)
        .getAllByRole('columnheader')
        .map((h) => h.textContent),
    ).toEqual([
      'Name',
      'GitLab project path',
      'GitLab project ID',
      'SonarQube key',
      'Created via',
      'Created',
      'Actions',
    ]);
    const payRow = within(table).getByRole('row', { name: /Payments/ });
    expect(within(payRow).getByRole('link', { name: 'Payments' })).toHaveAttribute(
      'href',
      '/services/1',
    );
    expect(within(payRow).getByText('4711')).toBeInTheDocument();
    expect(within(payRow).getByText('platform_payments')).toBeInTheDocument();
    expect(within(payRow).getByText('Upload')).toBeInTheDocument();
    const docsRow = within(table).getByRole('row', { name: /Docs/ });
    expect(within(docsRow).getByText('Not linked yet')).toBeInTheDocument();
    expect(within(docsRow).getByText('Not linked')).toBeInTheDocument();
    expect(within(docsRow).getByText('Manual')).toBeInTheDocument();
  });

  it('pages, filters by archived and searches on the server', async () => {
    const { calls } = mockFetch(
      api((call) => {
        if (call.path === '/api/v1/services?archived=false&after=0') {
          return json(200, { items: [pay], next_after: 1 });
        }
        if (call.path === '/api/v1/services?archived=false&after=1') {
          return json(200, { items: [docs], next_after: null });
        }
        return undefined;
      }),
    );
    renderApp('/admin/services');
    await screen.findByRole('link', { name: 'Payments' });
    fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
    await screen.findByRole('link', { name: 'Docs' });
    expect(calls.some((c) => c.path === '/api/v1/services?archived=false&after=1')).toBe(true);
    expect(screen.queryByRole('button', { name: 'Load more' })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('radio', { name: 'Archived' }));
    await waitFor(() =>
      expect(calls.some((c) => c.path === '/api/v1/services?archived=true&after=0')).toBe(true),
    );

    const search = screen.getByLabelText('Search services');
    fireEvent.change(search, { target: { value: 'pay' } });
    fireEvent.keyDown(search, { key: 'Enter' });
    await waitFor(() =>
      expect(calls.some((c) => c.path === '/api/v1/services?q=pay&archived=true&after=0')).toBe(
        true,
      ),
    );
  });

  it('validates the path, then creates a service with only the filled fields', async () => {
    const { calls } = mockFetch(
      api((call) =>
        call.method === 'POST' ? json(201, { ...docs, id: 3, name: 'billing' }) : undefined,
      ),
    );
    renderApp('/admin/services');
    await screen.findByRole('link', { name: 'Payments' });
    fireEvent.click(screen.getByRole('button', { name: 'Add service' }));
    expect(screen.getByText('group/project, as in the GitLab URL')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Add service', description: '' }));
    expect(await screen.findByText(/Enter the GitLab project path/)).toBeInTheDocument();
    expect(calls.some((c) => c.method === 'POST')).toBe(false);

    fireEvent.change(screen.getByLabelText('GitLab project path'), {
      target: { value: ' platform/billing ' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add service' }));
    expect(await screen.findByText('Added billing.')).toBeInTheDocument();
    expect(calls.find((c) => c.method === 'POST')).toMatchObject({
      path: '/api/v1/services',
      body: { gitlab_project_path: 'platform/billing' },
    });
  });

  it('shows a 400 under the field and a 409 as returned', async () => {
    let answer = apiError(400, 'invalid_request', 'The SonarQube key is too long.');
    mockFetch(api((call) => (call.method === 'POST' ? answer : undefined)));
    renderApp('/admin/services');
    await screen.findByRole('link', { name: 'Payments' });
    fireEvent.click(screen.getByRole('button', { name: 'Add service' }));
    fireEvent.change(screen.getByLabelText('GitLab project path'), {
      target: { value: 'a/b' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add service' }));
    const message = await screen.findByText('The SonarQube key is too long.');
    expect(screen.getByLabelText(/SonarQube project key/)).toHaveAccessibleDescription(
      'The SonarQube key is too long.',
    );
    expect(message).toBeInTheDocument();

    answer = apiError(409, 'conflict', 'A service with this project path already exists.');
    fireEvent.click(screen.getByRole('button', { name: 'Add service' }));
    expect(
      await screen.findByText('A service with this project path already exists.'),
    ).toBeInTheDocument();
  });

  it('edits the name and unlinks SonarQube with null', async () => {
    const { calls } = mockFetch(
      api((call) =>
        call.method === 'PATCH' ? json(200, { ...pay, sonar_project_key: null }) : undefined,
      ),
    );
    renderApp('/admin/services');
    await screen.findByRole('link', { name: 'Payments' });
    fireEvent.click(screen.getByRole('button', { name: 'Edit Payments' }));
    const dialog = screen.getByRole('alertdialog');
    expect(within(dialog).getByLabelText('SonarQube project key')).toHaveValue('platform_payments');
    fireEvent.change(within(dialog).getByLabelText('SonarQube project key'), {
      target: { value: '' },
    });
    fireEvent.change(within(dialog).getByLabelText('Name'), { target: { value: 'Payments API' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));
    expect(await screen.findByText('Saved Payments API.')).toBeInTheDocument();
    expect(calls.find((c) => c.method === 'PATCH')).toMatchObject({
      path: '/api/v1/services/1',
      body: { name: 'Payments API', sonar_project_key: null },
    });
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
  });

  it('archives after explaining the effect', async () => {
    const { calls } = mockFetch(
      api((call) => (call.method === 'PATCH' ? json(200, { ...pay, archived: true }) : undefined)),
    );
    renderApp('/admin/services');
    await screen.findByRole('link', { name: 'Payments' });
    fireEvent.click(screen.getByRole('button', { name: 'Archive Payments' }));
    expect(
      screen.getByText(/hidden from the portfolio and receive no new uploads/),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Archive' }));
    expect(await screen.findByText('Archived Payments.')).toBeInTheDocument();
    expect(calls.find((c) => c.method === 'PATCH')).toMatchObject({
      path: '/api/v1/services/1',
      body: { archived: true },
    });
  });

  it('deletes only after the exact project path is typed', async () => {
    const { calls } = mockFetch(
      api((call) => (call.method === 'DELETE' ? noContent() : undefined)),
    );
    renderApp('/admin/services');
    await screen.findByRole('link', { name: 'Payments' });
    fireEvent.click(screen.getByRole('button', { name: 'Delete Payments' }));
    const dialog = screen.getByRole('alertdialog');
    expect(within(dialog).getByText(/deletes all scans and analyses/)).toBeInTheDocument();
    const input = within(dialog).getByLabelText('Project path');
    fireEvent.change(input, { target: { value: 'platform/payment' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete service' }));
    expect(await within(dialog).findByText(/Type the project path exactly/)).toBeInTheDocument();
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false);

    fireEvent.change(input, { target: { value: 'platform/payments' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete service' }));
    expect(await screen.findByText(/Deleted Payments/)).toBeInTheDocument();
    expect(calls.find((c) => c.method === 'DELETE')?.path).toBe('/api/v1/services/1');
  });
});
