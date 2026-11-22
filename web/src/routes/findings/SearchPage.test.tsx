// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { fireEvent, screen, within } from '@testing-library/react';
import { apiError, json, makeUser, mockFetch, renderApp, type Handler } from '../../test-utils';

const svc = (id: number, name: string) => ({
  id,
  name,
  gitlab_project_id: id,
  gitlab_project_path: `acme/${name}`,
  sonar_project_key: null,
  created_via: 'ingest',
  archived: false,
  created_at: '2026-01-01T00:00:00Z',
  default_ref: null,
});

const hit = (over: object) => ({
  service: svc(1, 'payments-api'),
  ref: 'main',
  artifact_name: 'app',
  scan_id: 11,
  scanned_at: '2026-11-19T10:00:00Z',
  pkg_name: 'xz-utils',
  installed_version: '5.6.0',
  fixed_version: '5.6.2',
  severity: 'CRITICAL',
  ...over,
});

function searchApi(items: object[], extra?: Handler): Handler {
  return async (call) => {
    const custom = await extra?.(call);
    if (custom) return custom;
    if (call.path === '/api/v1/me') return json(200, makeUser());
    if (call.path.startsWith('/api/v1/search/vulnerabilities')) {
      const id = new URL(call.path, 'http://x').searchParams.get('id');
      return json(200, {
        vuln_id: id,
        scope: new URL(call.path, 'http://x').searchParams.get('scope') ?? 'dashboard',
        items,
        truncated: false,
        total_findings: items.length,
        total_services: new Set(items.map((i) => (i as { service: { id: number } }).service.id))
          .size,
        total_artifacts: new Set(
          items.map((i) => {
            const h = i as { service: { id: number }; artifact_name: string };
            return `${h.service.id}/${h.artifact_name}`;
          }),
        ).size,
        ...summaryOf(items as { severity: string; fixed_version: string }[]),
      });
    }
    return undefined;
  };
}

/** The summary the API derives from every match. */
function summaryOf(items: { severity: string; fixed_version: string }[]) {
  const counts = { critical: 0, high: 0, medium: 0, low: 0, unknown: 0 };
  for (const i of items) counts[i.severity.toLowerCase() as keyof typeof counts] += 1;
  return {
    counts,
    title: items.length ? 'xz: malicious code in the release tarballs' : '',
    primary_url: items.length ? 'https://avd.aquasec.com/nvd/cve-2024-3094' : '',
    fixed_versions: [...new Set(items.map((i) => i.fixed_version).filter(Boolean))],
  };
}

const searchCalls = (calls: { path: string }[]) =>
  calls.filter((c) => c.path.startsWith('/api/v1/search/vulnerabilities'));

const ITEMS = [
  hit({}),
  hit({ artifact_name: 'worker', scan_id: 12, fixed_version: '' }),
  hit({ service: svc(2, 'auth'), scan_id: 21, ref: 'release', severity: 'HIGH' }),
];

describe('vulnerability search', () => {
  it('searches only on Enter and groups results by service', async () => {
    const { calls } = mockFetch(searchApi(ITEMS));
    renderApp('/search');
    expect(await screen.findByText('Search by vulnerability ID')).toBeInTheDocument();
    const box = screen.getByRole('searchbox', { name: 'Vulnerability ID' });
    fireEvent.change(box, { target: { value: 'CVE-2024-3094' } });
    expect(searchCalls(calls)).toHaveLength(0);
    fireEvent.keyDown(box, { key: 'Enter' });

    expect(
      await screen.findByText('CVE-2024-3094 affects 2 services in 3 images.'),
    ).toBeInTheDocument();
    // The summary: worst severity, title, fixed versions and reach.
    const summary = screen.getByRole('region', { name: 'Vulnerability summary' });
    expect(within(summary).getByText('Critical at worst')).toBeInTheDocument();
    expect(
      within(summary).getByRole('heading', { name: 'xz: malicious code in the release tarballs' }),
    ).toBeInTheDocument();
    expect(within(summary).getByText('5.6.2')).toBeInTheDocument();
    expect(within(summary).getByRole('link', { name: /Advisory/ })).toHaveAttribute(
      'href',
      'https://avd.aquasec.com/nvd/cve-2024-3094',
    );
    expect(searchCalls(calls)).toHaveLength(1);
    expect(searchCalls(calls)[0].path).toBe('/api/v1/search/vulnerabilities?id=CVE-2024-3094');
    expect(screen.getByTestId('location')).toHaveTextContent('/search?id=CVE-2024-3094');

    // One table; a group of rows per service under a row naming it.
    const table = screen.getByRole('table', { name: 'Where CVE-2024-3094 is present' });
    const payments = within(table).getByRole('rowgroup', { name: 'payments-api' });
    expect(within(payments).getByRole('link', { name: 'payments-api' })).toHaveAttribute(
      'href',
      '/services/1',
    );
    expect(within(payments).getAllByRole('row')).toHaveLength(3);
    expect(within(payments).getByText('no fix yet')).toBeInTheDocument();
    expect(within(payments).getAllByRole('link', { name: /ago|just now|in / })[0]).toHaveAttribute(
      'href',
      '/scans/11',
    );
    const auth = within(table).getByRole('rowgroup', { name: 'auth' });
    expect(within(auth).getByText('release')).toBeInTheDocument();
    expect(within(auth).getByText('High')).toBeInTheDocument();
  });

  it('says so when the matches were cut and gives the true totals', async () => {
    mockFetch(async (call) => {
      if (call.path === '/api/v1/me') return json(200, makeUser());
      if (call.path.startsWith('/api/v1/search/vulnerabilities')) {
        return json(200, {
          vuln_id: 'CVE-2024-3094',
          scope: 'dashboard',
          items: [ITEMS[0]],
          truncated: true,
          total_findings: 4200,
          total_services: 310,
          total_artifacts: 512,
          ...summaryOf([ITEMS[0]]),
        });
      }
      return undefined;
    });
    renderApp('/search?id=CVE-2024-3094');
    expect(
      await screen.findByText('CVE-2024-3094 affects 310 services in 512 images.'),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/4,200 findings match\. Showing the 1 most severe/),
    ).toBeInTheDocument();
  });

  it('runs the search from the id URL param', async () => {
    const { calls } = mockFetch(searchApi([ITEMS[0]]));
    renderApp('/search?id=GHSA-jfh8-c2jp-5v3q');
    expect(
      await screen.findByText('GHSA-jfh8-c2jp-5v3q affects 1 service in 1 image.'),
    ).toBeInTheDocument();
    expect(searchCalls(calls)[0].path).toBe(
      '/api/v1/search/vulnerabilities?id=GHSA-jfh8-c2jp-5v3q',
    );
  });

  it('validates the format before sending anything', async () => {
    const { calls } = mockFetch(searchApi(ITEMS));
    renderApp('/search');
    const box = await screen.findByRole('searchbox', { name: 'Vulnerability ID' });
    expect(
      screen.getByText(/Use 3 to 100 characters: letters, digits and - _ : \./),
    ).toBeInTheDocument();
    fireEvent.change(box, { target: { value: 'a b' } });
    fireEvent.keyDown(box, { key: 'Enter' });
    expect(screen.getByRole('alert')).toHaveTextContent('Use 3 to 100 characters');
    fireEvent.change(box, { target: { value: 'ab' } });
    fireEvent.keyDown(box, { key: 'Enter' });
    expect(screen.getByRole('alert')).toBeInTheDocument();
    expect(searchCalls(calls)).toHaveLength(0);
  });

  it('does not send an invalid id from the URL', async () => {
    const { calls } = mockFetch(searchApi(ITEMS));
    renderApp('/search?id=a%20b');
    expect(await screen.findByRole('alert')).toBeInTheDocument();
    expect(searchCalls(calls)).toHaveLength(0);
  });

  it('shows a calm message when no service is affected', async () => {
    mockFetch(searchApi([]));
    renderApp('/search?id=CVE-2000-0001');
    expect(
      await screen.findByText('No service is affected in its current scans'),
    ).toBeInTheDocument();
  });

  it('offers example ids as links', async () => {
    mockFetch(searchApi([]));
    renderApp('/search');
    expect(await screen.findByRole('link', { name: 'CVE-2024-3094' })).toHaveAttribute(
      'href',
      '/search?id=CVE-2024-3094',
    );
    expect(screen.getByRole('link', { name: 'GHSA-jfh8-c2jp-5v3q' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('link', { name: 'CVE-2021-44228' }));
    expect(
      await screen.findByText('No service is affected in its current scans'),
    ).toBeInTheDocument();
    expect(screen.getByRole('searchbox', { name: 'Vulnerability ID' })).toHaveValue(
      'CVE-2021-44228',
    );
  });

  it('searches dashboard branches and protected refs by default and says so', async () => {
    const { calls } = mockFetch(searchApi(ITEMS));
    renderApp('/search?id=CVE-2024-3094');
    expect(
      await screen.findByText(/Searched dashboard branches and protected refs/),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('radio', { name: 'Dashboard branches and protected refs' }),
    ).toBeChecked();
    expect(screen.getByRole('radio', { name: 'All branches' })).not.toBeChecked();
    expect(searchCalls(calls)[0].path).toBe('/api/v1/search/vulnerabilities?id=CVE-2024-3094');
  });

  it('switches to all branches, reflects it in the URL and searches again', async () => {
    const { calls } = mockFetch(searchApi(ITEMS));
    renderApp('/search?id=CVE-2024-3094');
    await screen.findByText(/Searched dashboard branches and protected refs/);
    fireEvent.click(screen.getByRole('radio', { name: 'All branches' }));
    expect(await screen.findByText(/Searched all branches/)).toBeInTheDocument();
    expect(screen.getByTestId('location')).toHaveTextContent('/search?id=CVE-2024-3094&scope=all');
    expect(searchCalls(calls).map((c) => c.path)).toEqual([
      '/api/v1/search/vulnerabilities?id=CVE-2024-3094',
      '/api/v1/search/vulnerabilities?id=CVE-2024-3094&scope=all',
    ]);
    // Back to the default: the parameter leaves the URL.
    fireEvent.click(screen.getByRole('radio', { name: 'Dashboard branches and protected refs' }));
    await screen.findByText(/Searched dashboard branches and protected refs/);
    expect(screen.getByTestId('location')).toHaveTextContent(/^\/search\?id=CVE-2024-3094$/);
  });

  it('opens with the scope from the URL and keeps it for a new search', async () => {
    const { calls } = mockFetch(searchApi([ITEMS[0]]));
    renderApp('/search?id=CVE-2024-3094&scope=all');
    expect(await screen.findByText(/Searched all branches/)).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: 'All branches' })).toBeChecked();
    const box = screen.getByRole('searchbox', { name: 'Vulnerability ID' });
    fireEvent.change(box, { target: { value: 'CVE-2021-44228' } });
    fireEvent.keyDown(box, { key: 'Enter' });
    await screen.findByText('CVE-2021-44228 affects 1 service in 1 image.');
    expect(searchCalls(calls).at(-1)?.path).toBe(
      '/api/v1/search/vulnerabilities?id=CVE-2021-44228&scope=all',
    );
  });

  it('names the scope when nothing is found and offers every branch', async () => {
    mockFetch(searchApi([]));
    renderApp('/search?id=CVE-2000-0001&scope=all');
    expect(await screen.findByText(/searching all branches\./)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Search all branches' })).toBeNull();
  });

  it('offers to search every branch when the default scope finds nothing', async () => {
    const { calls } = mockFetch(searchApi([]));
    renderApp('/search?id=CVE-2000-0001');
    fireEvent.click(await screen.findByRole('button', { name: 'Search all branches' }));
    expect(await screen.findByText(/searching all branches\./)).toBeInTheDocument();
    expect(searchCalls(calls).at(-1)?.path).toBe(
      '/api/v1/search/vulnerabilities?id=CVE-2000-0001&scope=all',
    );
  });

  it('shows an error with a retry', async () => {
    mockFetch(
      searchApi(ITEMS, (c) =>
        c.path.startsWith('/api/v1/search/')
          ? apiError(500, 'internal', 'Search is unavailable.')
          : undefined,
      ),
    );
    renderApp('/search?id=CVE-2024-3094');
    expect(await screen.findByText('Search is unavailable.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Try again' })).toBeInTheDocument();
  });
});
