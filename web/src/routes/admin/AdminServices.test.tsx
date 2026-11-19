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
  default_ref: null,
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
  default_ref: null,
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
      'Dashboard branch',
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

  describe('dashboard branch', () => {
    const portfolioItem = (service: Service, ref: string | null, ref_source: string) => ({
      service,
      ref,
      ref_source,
      counts: { critical: 0, high: 0, medium: 0, low: 0, unknown: 0 },
      fixable: 0,
      secrets: 0,
      last_scan_at: null,
      quality_gate_status: null,
      reliability_rating: null,
      security_rating: null,
      maintainability_rating: null,
      coverage: null,
      last_analysis_at: null,
    });
    const overviewOf = (service: Service) => ({
      service,
      ref: 'feature/x',
      ref_source: 'latest',
      refs: ['feature/x', 'main'],
      artifacts: [],
      counts: { critical: 0, high: 0, medium: 0, low: 0, unknown: 0 },
      fixable: 0,
      secrets: 0,
      suppressed: 0,
      new_since_previous: 0,
      fixed_since_previous: 0,
      by_class: {},
      top_packages: [],
      data_at: null,
    });
    const withBranch: Service = { ...docs, default_ref: 'release' };

    function branchApi(extra?: Handler): Handler {
      return async (call) => {
        const custom = await extra?.(call);
        if (custom) return custom;
        if (call.path === '/api/v1/portfolio') {
          return json(200, {
            items: [
              portfolioItem(pay, 'feature/x', 'latest'),
              portfolioItem(withBranch, 'release', 'default'),
            ],
            totals: {
              services: 2,
              failing_quality_gates: 0,
              services_with_critical: 0,
              fixable: 0,
            },
            generated_at: '2026-11-19T10:00:00Z',
          });
        }
        if (call.path.startsWith('/api/v1/services/1/overview')) return json(200, overviewOf(pay));
        if (call.method === 'GET' && call.path.startsWith('/api/v1/services?')) {
          return json(200, { items: [pay, withBranch], next_after: null });
        }
        return undefined;
      };
    }

    it('shows the chosen branch, or says the latest scanned branch is shown', async () => {
      mockFetch(api(branchApi()));
      renderApp('/admin/services');
      const table = await screen.findByRole('table');
      const payRow = within(table).getByRole('row', { name: /Payments/ });
      await within(payRow).findByText('feature/x');
      expect(payRow).toHaveTextContent(
        'Not chosen — showing the latest scanned branch (feature/x)',
      );
      expect(
        within(payRow).getByRole('button', { name: 'Choose dashboard branch of Payments' }),
      ).toBeInTheDocument();
      const docsRow = within(table).getByRole('row', { name: /Docs/ });
      expect(docsRow).toHaveTextContent('release');
      expect(docsRow).not.toHaveTextContent('Not chosen');
      expect(
        within(docsRow).getByRole('button', { name: 'Change dashboard branch of Docs' }),
      ).toBeInTheDocument();
    });

    it('still explains an unset branch when the latest branch is unknown', async () => {
      mockFetch(api());
      renderApp('/admin/services');
      const payRow = within(await screen.findByRole('table')).getByRole('row', {
        name: /Payments/,
      });
      expect(payRow).toHaveTextContent('Not chosen — showing the latest scanned branch');
    });

    it('chooses a branch from the scanned refs and sends it', async () => {
      const { calls } = mockFetch(
        api(
          branchApi((call) =>
            call.method === 'PATCH' ? json(200, { ...pay, default_ref: 'main' }) : undefined,
          ),
        ),
      );
      renderApp('/admin/services');
      await screen.findByRole('link', { name: 'Payments' });
      fireEvent.click(screen.getByRole('button', { name: 'Choose dashboard branch of Payments' }));
      const input = screen.getByLabelText('Dashboard branch of Payments');
      expect(input).toHaveValue('');
      expect(input).toHaveFocus();
      // The datalist offers the service's scanned refs.
      await waitFor(() =>
        expect(
          Array.from(
            document.querySelectorAll(`datalist#${CSS.escape(input.getAttribute('list')!)} option`),
          ).map((o) => o.getAttribute('value')),
        ).toEqual(['feature/x', 'main']),
      );
      // Clear has nothing to clear while no branch is chosen.
      expect(
        screen.getByRole('button', { name: 'Clear dashboard branch of Payments' }),
      ).toBeDisabled();
      fireEvent.change(input, { target: { value: '  main ' } });
      fireEvent.click(screen.getByRole('button', { name: 'Save dashboard branch of Payments' }));
      expect(
        await screen.findByText('The dashboards of Payments now show main.'),
      ).toBeInTheDocument();
      expect(calls.find((c) => c.method === 'PATCH')).toMatchObject({
        path: '/api/v1/services/1',
        body: { default_ref: 'main' },
      });
      expect(screen.queryByLabelText('Dashboard branch of Payments')).not.toBeInTheDocument();
    });

    it('accepts a branch without scans and submits on Enter', async () => {
      const { calls } = mockFetch(
        api(
          branchApi((call) =>
            call.method === 'PATCH' ? json(200, { ...pay, default_ref: 'release/9' }) : undefined,
          ),
        ),
      );
      renderApp('/admin/services');
      await screen.findByRole('link', { name: 'Payments' });
      fireEvent.click(screen.getByRole('button', { name: 'Choose dashboard branch of Payments' }));
      const input = screen.getByLabelText('Dashboard branch of Payments');
      fireEvent.change(input, { target: { value: 'release/9' } });
      fireEvent.submit(input.closest('form')!);
      await screen.findByText('The dashboards of Payments now show release/9.');
      expect(calls.find((c) => c.method === 'PATCH')?.body).toEqual({ default_ref: 'release/9' });
    });

    it('clears the choice with null', async () => {
      const { calls } = mockFetch(
        api(
          branchApi((call) =>
            call.method === 'PATCH' ? json(200, { ...withBranch, default_ref: null }) : undefined,
          ),
        ),
      );
      renderApp('/admin/services');
      await screen.findByRole('link', { name: 'Docs' });
      fireEvent.click(screen.getByRole('button', { name: 'Change dashboard branch of Docs' }));
      expect(screen.getByLabelText('Dashboard branch of Docs')).toHaveValue('release');
      fireEvent.click(screen.getByRole('button', { name: 'Clear dashboard branch of Docs' }));
      expect(await screen.findByText(/Cleared the dashboard branch of Docs/)).toBeInTheDocument();
      expect(calls.find((c) => c.method === 'PATCH')).toMatchObject({
        path: '/api/v1/services/2',
        body: { default_ref: null },
      });
    });

    it('refuses an empty branch and shows the server error inline', async () => {
      let answer = apiError(400, 'invalid_request', 'default_ref must be 1 to 255 characters');
      const { calls } = mockFetch(
        api(branchApi((call) => (call.method === 'PATCH' ? answer : undefined))),
      );
      renderApp('/admin/services');
      await screen.findByRole('link', { name: 'Payments' });
      fireEvent.click(screen.getByRole('button', { name: 'Choose dashboard branch of Payments' }));
      const input = screen.getByLabelText('Dashboard branch of Payments');
      fireEvent.click(screen.getByRole('button', { name: 'Save dashboard branch of Payments' }));
      expect(await screen.findByRole('alert')).toHaveTextContent('Enter a branch name');
      expect(input).toHaveAccessibleDescription(/Enter a branch name/);
      expect(input).toHaveAttribute('aria-invalid', 'true');
      expect(calls.some((c) => c.method === 'PATCH')).toBe(false);

      fireEvent.change(input, { target: { value: 'x' } });
      fireEvent.click(screen.getByRole('button', { name: 'Save dashboard branch of Payments' }));
      expect(
        await screen.findByText('default_ref must be 1 to 255 characters'),
      ).toBeInTheDocument();
      // The editor stays open so the value can be corrected.
      expect(screen.getByLabelText('Dashboard branch of Payments')).toHaveValue('x');
      answer = json(200, { ...pay, default_ref: 'x' });
      fireEvent.click(screen.getByRole('button', { name: 'Save dashboard branch of Payments' }));
      await screen.findByText('The dashboards of Payments now show x.');
    });

    it('cancels with the button or Escape without sending anything', async () => {
      const { calls } = mockFetch(api(branchApi()));
      renderApp('/admin/services');
      await screen.findByRole('link', { name: 'Payments' });
      fireEvent.click(screen.getByRole('button', { name: 'Choose dashboard branch of Payments' }));
      fireEvent.click(
        screen.getByRole('button', { name: 'Cancel editing the dashboard branch of Payments' }),
      );
      expect(screen.queryByLabelText('Dashboard branch of Payments')).not.toBeInTheDocument();
      fireEvent.click(screen.getByRole('button', { name: 'Choose dashboard branch of Payments' }));
      fireEvent.keyDown(screen.getByLabelText('Dashboard branch of Payments'), { key: 'Escape' });
      expect(screen.queryByLabelText('Dashboard branch of Payments')).not.toBeInTheDocument();
      expect(calls.some((c) => c.method === 'PATCH')).toBe(false);
    });
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
