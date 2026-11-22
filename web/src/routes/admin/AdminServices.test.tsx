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
  openMenu,
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
const withBranch: Service = { ...docs, default_ref: 'release' };

const portfolioItem = (
  service: Service,
  ref: string | null,
  ref_source: string,
  extra: Record<string, unknown> = {},
) => ({
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
  ...extra,
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

/** The portfolio knows the latest branch and the last scan and analysis of Payments. */
function branchApi(extra?: Handler): Handler {
  return api(async (call) => {
    const custom = await extra?.(call);
    if (custom) return custom;
    if (call.path === '/api/v1/portfolio') {
      return json(200, {
        items: [
          portfolioItem(pay, 'feature/x', 'latest', {
            last_scan_at: new Date(Date.now() - 2 * 3600_000).toISOString(),
            last_analysis_at: '2026-11-18T10:00:00Z',
          }),
          portfolioItem(withBranch, 'release', 'default', {
            last_scan_at: '2026-01-01T10:00:00Z',
          }),
        ],
        totals: { services: 2, failing_quality_gates: 0, services_with_critical: 0, fixable: 0 },
        generated_at: '2026-11-19T10:00:00Z',
      });
    }
    if (call.path.startsWith('/api/v1/services/1/overview')) return json(200, overviewOf(pay));
    if (call.path.startsWith('/api/v1/services/2/overview')) {
      return json(200, { ...overviewOf(withBranch), refs: [] });
    }
    if (call.method === 'GET' && call.path.startsWith('/api/v1/services?')) {
      return json(200, { items: [pay, withBranch], next_after: null });
    }
    return undefined;
  });
}

configure({ asyncUtilTimeout: 5000 });

async function rowOf(name: RegExp) {
  return within(await screen.findByRole('table')).getByRole('row', { name });
}

async function chooseAction(service: string, item: string) {
  await screen.findByRole('link', { name: service });
  const menu = await openMenu(`Actions for ${service}`);
  fireEvent.click(within(menu).getByRole('menuitem', { name: item }));
}

describe('admin services page', () => {
  it('tells viewers they need the admin role and requests no services', async () => {
    const { calls } = mockFetch((call) =>
      call.path === '/api/v1/me' ? json(200, makeUser({ role: 'viewer' })) : undefined,
    );
    renderApp('/admin/services');
    expect(await screen.findByText('You need the admin role to see this page.')).toBeVisible();
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Add service' })).not.toBeInTheDocument();
    expect(calls.some((c) => c.path.startsWith('/api/v1/services'))).toBe(false);
  });

  it('lists services with name and path, SonarQube link and last scan', async () => {
    mockFetch(branchApi());
    renderApp('/admin/services');
    const table = await screen.findByRole('table', { name: 'Active services' });
    expect(
      within(table)
        .getAllByRole('columnheader')
        .map((h) => h.textContent),
    ).toEqual(['Service', 'Dashboard branch', 'SonarQube key', 'Last scan', 'Actions']);
    const payRow = within(table).getByRole('row', { name: /Payments/ });
    expect(within(payRow).getByRole('link', { name: 'Payments' })).toHaveAttribute(
      'href',
      '/services/1',
    );
    expect(within(payRow).getByText('platform/payments')).toBeInTheDocument();
    expect(within(payRow).getByText('platform_payments')).toBeInTheDocument();
    // An analysis arrived for the key, so the link is confirmed.
    await within(payRow).findByRole('button', { name: /analyses arrive for this key/ });
    expect(payRow).toHaveTextContent('2 hours ago');
    expect(payRow).toHaveTextContent('Added by its first upload');

    const docsRow = within(table).getByRole('row', { name: /Docs/ });
    expect(within(docsRow).getByText('Not linked')).toBeInTheDocument();
    expect(docsRow).toHaveTextContent('Added by hand');
    // Its last scan is months old: flagged as stale.
    expect(docsRow).toHaveTextContent('older than 14 days');
  });

  it('says a linked key without analyses waits, and a service without uploads waits', async () => {
    mockFetch(
      api((call) =>
        call.path === '/api/v1/portfolio'
          ? json(200, { items: [], totals: {}, generated_at: '' })
          : undefined,
      ),
    );
    renderApp('/admin/services');
    const payRow = await rowOf(/Payments/);
    expect(
      within(payRow).getByRole('button', { name: /no analysis has arrived yet/ }),
    ).toBeInTheDocument();
    const docsRow = await rowOf(/Docs/);
    expect(docsRow).toHaveTextContent('Waiting for the first upload');
  });

  it('pages, switches to archived services and searches on the server', async () => {
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
    expect(screen.getByText('All 2 services shown')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('tab', { name: 'Archived' }));
    expect(screen.getByRole('tab', { name: 'Archived' })).toHaveAttribute('aria-selected', 'true');
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

  it('explains empty lists and failures', async () => {
    let answer: Response = json(200, { items: [], next_after: null });
    mockFetch(api((call) => (call.path.startsWith('/api/v1/services?') ? answer : undefined)));
    renderApp('/admin/services');
    expect(await screen.findByText('No services yet')).toBeInTheDocument();

    answer = apiError(500, 'internal_error', 'Something went wrong. Try again later.');
    fireEvent.click(screen.getByRole('tab', { name: 'Archived' }));
    expect(await screen.findByText('Services could not be loaded')).toBeInTheDocument();
    answer = json(200, { items: [], next_after: null });
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByText('No archived services')).toBeInTheDocument();
  });

  it('adds a service in a dialog with only the filled fields and confirms with a toast', async () => {
    const { calls } = mockFetch(
      api((call) =>
        call.method === 'POST' ? json(201, { ...docs, id: 3, name: 'billing' }) : undefined,
      ),
    );
    renderApp('/admin/services');
    await screen.findByRole('link', { name: 'Payments' });
    fireEvent.click(screen.getByRole('button', { name: 'Add service' }));
    const dialog = await screen.findByRole('dialog', { name: 'Add a service' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add service' }));
    expect(await within(dialog).findByText(/Enter the GitLab project path/)).toBeInTheDocument();
    expect(within(dialog).getByLabelText('GitLab project path')).toHaveAttribute(
      'aria-invalid',
      'true',
    );
    expect(calls.some((c) => c.method === 'POST')).toBe(false);

    fireEvent.change(within(dialog).getByLabelText('GitLab project path'), {
      target: { value: ' platform/billing ' },
    });
    // The name the service gets when none is typed.
    expect(within(dialog).getByText('billing')).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add service' }));
    expect(await screen.findByText('Service added')).toBeInTheDocument();
    expect(screen.getByText('billing is ready for its first upload.')).toBeInTheDocument();
    expect(calls.find((c) => c.method === 'POST')).toMatchObject({
      path: '/api/v1/services',
      body: { gitlab_project_path: 'platform/billing' },
    });
    await waitFor(() =>
      expect(screen.queryByRole('dialog', { name: 'Add a service' })).not.toBeInTheDocument(),
    );
  });

  it('shows a 400 under the field it names and a 409 under the path', async () => {
    let answer = apiError(400, 'invalid_request', 'The SonarQube key is too long.');
    mockFetch(api((call) => (call.method === 'POST' ? answer : undefined)));
    renderApp('/admin/services');
    await screen.findByRole('link', { name: 'Payments' });
    fireEvent.click(screen.getByRole('button', { name: 'Add service' }));
    const dialog = await screen.findByRole('dialog', { name: 'Add a service' });
    fireEvent.change(within(dialog).getByLabelText('GitLab project path'), {
      target: { value: 'a/b' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add service' }));
    await within(dialog).findByText('The SonarQube key is too long.');
    expect(within(dialog).getByLabelText(/SonarQube key/)).toHaveAccessibleDescription(
      /The SonarQube key is too long\./,
    );

    answer = apiError(409, 'conflict', 'A service with this project path already exists.');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add service' }));
    await within(dialog).findByText('A service with this project path already exists.');
    expect(within(dialog).getByLabelText('GitLab project path')).toHaveAccessibleDescription(
      /already exists/,
    );
  });

  it('edits the name and unlinks SonarQube with null', async () => {
    const { calls } = mockFetch(
      api((call) =>
        call.method === 'PATCH'
          ? json(200, { ...pay, name: 'Payments API', sonar_project_key: null })
          : undefined,
      ),
    );
    renderApp('/admin/services');
    await chooseAction('Payments', 'Edit');
    const dialog = await screen.findByRole('dialog', { name: 'Edit Payments' });
    expect(within(dialog).getByText('platform/payments')).toBeInTheDocument();
    expect(within(dialog).getByLabelText('SonarQube project key')).toHaveValue('platform_payments');
    fireEvent.change(within(dialog).getByLabelText('SonarQube project key'), {
      target: { value: '' },
    });
    fireEvent.change(within(dialog).getByLabelText('Name'), { target: { value: '' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save changes' }));
    expect(await within(dialog).findByText('Enter a name for the service.')).toBeInTheDocument();
    expect(calls.some((c) => c.method === 'PATCH')).toBe(false);

    fireEvent.change(within(dialog).getByLabelText('Name'), { target: { value: 'Payments API' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save changes' }));
    expect(await screen.findByText('Changes saved')).toBeInTheDocument();
    expect(calls.find((c) => c.method === 'PATCH')).toMatchObject({
      path: '/api/v1/services/1',
      body: { name: 'Payments API', sonar_project_key: null },
    });
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
  });

  it('keeps the edit dialog open with the server error', async () => {
    mockFetch(
      api((call) =>
        call.method === 'PATCH'
          ? apiError(409, 'conflict', 'Another service uses this SonarQube key.')
          : undefined,
      ),
    );
    renderApp('/admin/services');
    await chooseAction('Payments', 'Edit');
    const dialog = await screen.findByRole('dialog', { name: 'Edit Payments' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save changes' }));
    expect(
      await within(dialog).findByText('Another service uses this SonarQube key.'),
    ).toBeInTheDocument();
    expect(screen.getByRole('dialog', { name: 'Edit Payments' })).toBeInTheDocument();
  });

  describe('dashboard branch', () => {
    it('shows the chosen branch as a tag, or the latest scanned branch', async () => {
      mockFetch(branchApi());
      renderApp('/admin/services');
      const payRow = await rowOf(/Payments/);
      expect(
        await within(payRow).findByRole('button', {
          name: 'Dashboard branch of Payments: latest scanned, feature/x. Change',
        }),
      ).toHaveTextContent('Latestfeature/x');
      const docsRow = await rowOf(/Docs/);
      expect(
        within(docsRow).getByRole('button', { name: 'Dashboard branch of Docs: release. Change' }),
      ).toHaveTextContent('release');
    });

    it('still names the latest branch when it is not known', async () => {
      mockFetch(api());
      renderApp('/admin/services');
      const payRow = await rowOf(/Payments/);
      expect(
        within(payRow).getByRole('button', {
          name: 'Dashboard branch of Payments: latest scanned. Change',
        }),
      ).toHaveTextContent('Latest');
    });

    it('chooses a scanned branch from the list with the keyboard and sends it', async () => {
      const { calls } = mockFetch(
        branchApi((call) =>
          call.method === 'PATCH' ? json(200, { ...pay, default_ref: 'main' }) : undefined,
        ),
      );
      renderApp('/admin/services');
      fireEvent.click(
        await screen.findByRole('button', { name: /^Dashboard branch of Payments:/ }),
      );
      const editor = await screen.findByRole('dialog', { name: 'Dashboard branch of Payments' });
      const input = within(editor).getByRole('combobox', { name: 'Branch name' });
      await waitFor(() => expect(input).toHaveFocus());
      expect(input).toHaveValue('');
      const list = within(editor).getByRole('listbox', { name: 'Scanned branches' });
      await waitFor(() =>
        expect(
          within(list)
            .getAllByRole('option')
            .map((o) => o.textContent),
        ).toEqual(['feature/xlatest scan', 'main']),
      );
      // Nothing is chosen, so there is nothing to go back from.
      expect(within(editor).getByRole('button', { name: 'Use latest scanned' })).toBeDisabled();

      fireEvent.keyDown(input, { key: 'ArrowDown' });
      fireEvent.keyDown(input, { key: 'ArrowDown' });
      const options = within(list).getAllByRole('option');
      expect(options[1]).toHaveAttribute('aria-selected', 'true');
      expect(input).toHaveAttribute('aria-activedescendant', options[1].id);
      fireEvent.keyDown(input, { key: 'Enter' });
      expect(input).toHaveValue('main');

      fireEvent.click(within(editor).getByRole('button', { name: 'Save branch' }));
      expect(await screen.findByText('Dashboard branch changed')).toBeInTheDocument();
      expect(screen.getByText('Payments shows main.')).toBeInTheDocument();
      expect(calls.find((c) => c.method === 'PATCH')).toMatchObject({
        path: '/api/v1/services/1',
        body: { default_ref: 'main' },
      });
      await waitFor(() =>
        expect(
          screen.queryByRole('dialog', { name: 'Dashboard branch of Payments' }),
        ).not.toBeInTheDocument(),
      );
    });

    it('narrows the list as one types and accepts a branch without scans', async () => {
      const { calls } = mockFetch(
        branchApi((call) =>
          call.method === 'PATCH' ? json(200, { ...pay, default_ref: 'release/9' }) : undefined,
        ),
      );
      renderApp('/admin/services');
      fireEvent.click(
        await screen.findByRole('button', { name: /^Dashboard branch of Payments:/ }),
      );
      const editor = await screen.findByRole('dialog', { name: 'Dashboard branch of Payments' });
      const input = within(editor).getByRole('combobox', { name: 'Branch name' });
      await within(editor).findByRole('option', { name: /main/ });
      fireEvent.change(input, { target: { value: 'feat' } });
      expect(within(editor).getAllByRole('option')).toHaveLength(1);
      fireEvent.change(input, { target: { value: '  release/9 ' } });
      expect(within(editor).queryAllByRole('option')).toHaveLength(0);
      expect(editor).toHaveTextContent('No scanned branch matches');
      fireEvent.submit(input.closest('form')!);
      await screen.findByText('Payments shows release/9.');
      expect(calls.find((c) => c.method === 'PATCH')?.body).toEqual({ default_ref: 'release/9' });
    });

    it('goes back to the latest scanned branch with null', async () => {
      const { calls } = mockFetch(
        branchApi((call) =>
          call.method === 'PATCH' ? json(200, { ...withBranch, default_ref: null }) : undefined,
        ),
      );
      renderApp('/admin/services');
      fireEvent.click(await screen.findByRole('button', { name: /^Dashboard branch of Docs:/ }));
      const editor = await screen.findByRole('dialog', { name: 'Dashboard branch of Docs' });
      expect(within(editor).getByRole('combobox', { name: 'Branch name' })).toHaveValue('release');
      // Docs has no scans yet: the editor says a branch can be typed anyway.
      expect(await within(editor).findByText(/No scans yet\. Type the branch/)).toBeInTheDocument();
      fireEvent.click(within(editor).getByRole('button', { name: 'Use latest scanned' }));
      expect(await screen.findByText('Docs shows the latest scanned branch.')).toBeInTheDocument();
      expect(calls.find((c) => c.method === 'PATCH')).toMatchObject({
        path: '/api/v1/services/2',
        body: { default_ref: null },
      });
    });

    it('refuses an empty branch and shows the server error inline', async () => {
      let answer = apiError(400, 'invalid_request', 'default_ref must be 1 to 255 characters');
      const { calls } = mockFetch(
        branchApi((call) => (call.method === 'PATCH' ? answer : undefined)),
      );
      renderApp('/admin/services');
      fireEvent.click(
        await screen.findByRole('button', { name: /^Dashboard branch of Payments:/ }),
      );
      const editor = await screen.findByRole('dialog', { name: 'Dashboard branch of Payments' });
      const input = within(editor).getByRole('combobox', { name: 'Branch name' });
      fireEvent.click(within(editor).getByRole('button', { name: 'Save branch' }));
      expect(await within(editor).findByRole('alert')).toHaveTextContent('Enter a branch name');
      expect(input).toHaveAttribute('aria-invalid', 'true');
      expect(calls.some((c) => c.method === 'PATCH')).toBe(false);

      fireEvent.change(input, { target: { value: 'x' } });
      fireEvent.click(within(editor).getByRole('button', { name: 'Save branch' }));
      expect(
        await within(editor).findByText('default_ref must be 1 to 255 characters'),
      ).toBeInTheDocument();
      // The editor stays open so the value can be corrected.
      expect(input).toHaveValue('x');
      answer = json(200, { ...pay, default_ref: 'x' });
      fireEvent.click(within(editor).getByRole('button', { name: 'Save branch' }));
      await screen.findByText('Payments shows x.');
    });

    it('closes with Escape without sending anything', async () => {
      const { calls } = mockFetch(branchApi());
      renderApp('/admin/services');
      fireEvent.click(
        await screen.findByRole('button', { name: /^Dashboard branch of Payments:/ }),
      );
      const editor = await screen.findByRole('dialog', { name: 'Dashboard branch of Payments' });
      fireEvent.keyDown(within(editor).getByRole('combobox', { name: 'Branch name' }), {
        key: 'Escape',
      });
      await waitFor(() =>
        expect(
          screen.queryByRole('dialog', { name: 'Dashboard branch of Payments' }),
        ).not.toBeInTheDocument(),
      );
      expect(calls.some((c) => c.method === 'PATCH')).toBe(false);
    });

    it('opens from the row menu', async () => {
      mockFetch(branchApi());
      renderApp('/admin/services');
      await chooseAction('Payments', 'Choose dashboard branch');
      const editor = await screen.findByRole('dialog', { name: 'Dashboard branch of Payments' });
      await waitFor(() =>
        expect(within(editor).getByRole('combobox', { name: 'Branch name' })).toHaveFocus(),
      );
    });
  });

  it('archives after explaining the effect, as a danger action', async () => {
    const { calls } = mockFetch(
      api((call) => (call.method === 'PATCH' ? json(200, { ...pay, archived: true }) : undefined)),
    );
    renderApp('/admin/services');
    await chooseAction('Payments', 'Archive');
    const dialog = await screen.findByRole('alertdialog', { name: 'Archive Payments?' });
    expect(dialog).toHaveTextContent(/leaves the portfolio and its pipeline uploads are refused/);
    // Focus starts on the safe choice.
    expect(within(dialog).getByRole('button', { name: 'Cancel' })).toHaveFocus();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Archive service' }));
    expect(await screen.findByText('Service archived')).toBeInTheDocument();
    expect(calls.find((c) => c.method === 'PATCH')).toMatchObject({
      path: '/api/v1/services/1',
      body: { archived: true },
    });
  });

  it('unarchives from the archived tab without a confirmation', async () => {
    const archived = { ...pay, archived: true };
    const { calls } = mockFetch(
      api((call) => {
        if (call.method === 'GET' && call.path.startsWith('/api/v1/services?archived=true')) {
          return json(200, { items: [archived], next_after: null });
        }
        if (call.method === 'PATCH') return json(200, pay);
        return undefined;
      }),
    );
    renderApp('/admin/services');
    await screen.findByRole('link', { name: 'Payments' });
    fireEvent.click(screen.getByRole('tab', { name: 'Archived' }));
    await screen.findByRole('table', { name: 'Archived services' });
    await chooseAction('Payments', 'Unarchive');
    expect(await screen.findByText('Service unarchived')).toBeInTheDocument();
    expect(calls.find((c) => c.method === 'PATCH')).toMatchObject({
      path: '/api/v1/services/1',
      body: { archived: false },
    });
  });

  it('reports a failed unarchive in an error toast', async () => {
    mockFetch(
      api((call) => {
        if (call.method === 'GET' && call.path.startsWith('/api/v1/services?archived=true')) {
          return json(200, { items: [{ ...pay, archived: true }], next_after: null });
        }
        if (call.method === 'PATCH') return apiError(500, 'internal_error', 'Database down.');
        return undefined;
      }),
    );
    renderApp('/admin/services');
    await screen.findByRole('link', { name: 'Payments' });
    fireEvent.click(screen.getByRole('tab', { name: 'Archived' }));
    await screen.findByRole('table', { name: 'Archived services' });
    await chooseAction('Payments', 'Unarchive');
    expect(await screen.findByText('Payments could not be changed')).toBeInTheDocument();
  });

  it('deletes only after the exact project path is typed', async () => {
    const { calls } = mockFetch(
      api((call) => (call.method === 'DELETE' ? noContent() : undefined)),
    );
    renderApp('/admin/services');
    await chooseAction('Payments', 'Delete');
    const dialog = await screen.findByRole('alertdialog', { name: 'Delete Payments?' });
    expect(dialog).toHaveTextContent(/deletes every scan and analysis of Payments/);
    const input = within(dialog).getByLabelText(/Type platform\/payments to confirm/);
    fireEvent.change(input, { target: { value: 'platform/payment' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete service' }));
    expect(await within(dialog).findByText(/Type the project path exactly/)).toBeInTheDocument();
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false);

    fireEvent.change(input, { target: { value: 'platform/payments' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(await screen.findByText('Service deleted')).toBeInTheDocument();
    expect(calls.find((c) => c.method === 'DELETE')?.path).toBe('/api/v1/services/1');
  });
});
