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
      return json(200, { vuln_id: id, items });
    }
    return undefined;
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
      await screen.findByText(
        (_, el) =>
          el?.tagName === 'P' &&
          el.textContent === 'CVE-2024-3094 affects 2 services in 3 artifacts',
      ),
    ).toBeInTheDocument();
    expect(searchCalls(calls)).toHaveLength(1);
    expect(searchCalls(calls)[0].path).toBe('/api/v1/search/vulnerabilities?id=CVE-2024-3094');
    expect(screen.getByTestId('location')).toHaveTextContent('/search?id=CVE-2024-3094');

    const payments = screen.getByRole('region', { name: 'payments-api' });
    expect(within(payments).getByRole('link', { name: 'payments-api' })).toHaveAttribute(
      'href',
      '/services/1',
    );
    expect(within(payments).getAllByRole('row')).toHaveLength(3);
    expect(within(payments).getByText('No fix')).toBeInTheDocument();
    expect(within(payments).getAllByRole('link', { name: /ago|just now|in / })[0]).toHaveAttribute(
      'href',
      '/scans/11',
    );
    const auth = screen.getByRole('region', { name: 'auth' });
    expect(within(auth).getByText('release')).toBeInTheDocument();
    expect(within(auth).getByText('High')).toBeInTheDocument();
  });

  it('runs the search from the id URL param', async () => {
    const { calls } = mockFetch(searchApi([ITEMS[0]]));
    renderApp('/search?id=GHSA-jfh8-c2jp-5v3q');
    expect(
      await screen.findByText(
        (_, el) =>
          el?.tagName === 'P' &&
          el.textContent === 'GHSA-jfh8-c2jp-5v3q affects 1 service in 1 artifact',
      ),
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
      await screen.findByText('No service is affected in its current scans.'),
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
      await screen.findByText('No service is affected in its current scans.'),
    ).toBeInTheDocument();
    expect(screen.getByRole('searchbox', { name: 'Vulnerability ID' })).toHaveValue(
      'CVE-2021-44228',
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
