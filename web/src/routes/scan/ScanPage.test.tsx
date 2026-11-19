// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import type { Scan, ScanDiff, Vulnerability } from '../../api/scan';
import { apiError, json, makeUser, mockFetch, renderApp, type Handler } from '../../test-utils';

const scan: Scan = {
  id: 7,
  service_id: 3,
  kind: 'image',
  artifact_name: 'registry.example.com/pay/api:1.4',
  gitlab_pipeline_id: 1001,
  gitlab_job_id: 5001,
  ref: 'release/1.x',
  ref_protected: true,
  commit_sha: 'abcdef0123456789',
  trivy_version: '0.58.1',
  scanned_at: '2026-11-19T10:00:00Z',
  received_at: '2026-11-19T10:01:00Z',
  counts: { critical: 2, high: 5, medium: 0, low: 1, unknown: 0 },
  fixable: 4,
  secrets: 1,
  suppressed: 3,
  gitlab_project_path: 'pay/api',
  pipeline_url: 'https://gitlab.example.com/pay/api/-/pipelines/1001',
  job_url: 'https://gitlab.example.com/pay/api/-/jobs/5001',
  commit_url: 'https://gitlab.example.com/pay/api/-/commit/abcdef0123456789',
};

function vuln(id: number, over: Partial<Vulnerability> = {}): Vulnerability {
  return {
    id,
    scan_id: 7,
    artifact_name: scan.artifact_name,
    target: 'alpine 3.19',
    class: 'os-pkgs',
    pkg_type: 'alpine',
    pkg_name: `pkg${id}`,
    pkg_path: '',
    installed_version: '1.0.0',
    fixed_version: '1.0.1',
    vuln_id: `CVE-2026-${id}`,
    severity: 'HIGH',
    status: 'fixed',
    title: 't',
    primary_url: `https://avd.example.com/CVE-2026-${id}`,
    suppressed: false,
    suppression: '',
    first_seen_at: '2026-11-19T10:00:00Z',
    ...over,
  };
}

const diff: ScanDiff = {
  scan_id: 7,
  previous_scan_id: 6,
  new: [
    vuln(1, { severity: 'LOW' }),
    vuln(2, { severity: 'CRITICAL', fixed_version: '', primary_url: 'javascript:alert(1)' }),
    vuln(3),
  ],
  fixed: [vuln(4)],
  unchanged: 12,
};

function api(
  over: { scan?: Partial<Scan>; diff?: Handler; scans?: Handler; service?: Handler } = {},
) {
  const handler: Handler = async (call) => {
    if (call.path === '/api/v1/me') return json(200, makeUser({ role: 'viewer' }));
    if (call.path === '/api/v1/scans/7') return json(200, { ...scan, ...over.scan });
    if (call.path === '/api/v1/scans/7/diff') return (await over.diff?.(call)) ?? json(200, diff);
    if (call.path === '/api/v1/services/3') {
      return (
        (await over.service?.(call)) ??
        json(200, {
          id: 3,
          name: 'Payments API',
          gitlab_project_id: 1,
          gitlab_project_path: 'pay/api',
          sonar_project_key: null,
          created_via: 'ingest',
          archived: false,
          created_at: '2026-01-01T00:00:00Z',
        })
      );
    }
    if (call.path.startsWith('/api/v1/services/3/scans')) {
      return (await over.scans?.(call)) ?? json(200, { items: [{ ...scan }], next_before: null });
    }
    return undefined;
  };
  return mockFetch(handler);
}

describe('scan detail', () => {
  it('shows header facts with exact links and the protected tag', async () => {
    const { calls } = api();
    renderApp('/scans/7');
    expect(await screen.findByRole('heading', { level: 1 })).toHaveTextContent(scan.artifact_name);
    expect(screen.getByText('Image')).toBeInTheDocument();
    const crumbs = screen.getByRole('navigation', { name: 'Breadcrumb' });
    expect(within(crumbs).getByRole('link', { name: 'Portfolio' })).toHaveAttribute('href', '/');
    expect(await within(crumbs).findByRole('link', { name: 'Payments API' })).toHaveAttribute(
      'href',
      '/services/3',
    );
    expect(screen.getByRole('link', { name: 'Pipeline #1001' })).toHaveAttribute(
      'href',
      scan.pipeline_url,
    );
    expect(screen.getByRole('link', { name: 'Job #5001' })).toHaveAttribute('href', scan.job_url);
    const commit = screen.getByRole('link', { name: 'abcdef01' });
    expect(commit).toHaveAttribute('href', scan.commit_url);
    expect(screen.getByText('release/1.x')).toBeInTheDocument();
    expect(screen.getByText('protected')).toBeInTheDocument();
    expect(screen.getByText('0.58.1')).toBeInTheDocument();
    expect(screen.getByText('Scanned at')).toBeInTheDocument();
    expect(screen.getByText('Received at')).toBeInTheDocument();
    expect(calls.some((c) => c.path === '/api/v1/scans/7/diff')).toBe(true);
  });

  it('shows no link for null or unsafe URLs, no protected tag, and the secret scan label', async () => {
    api({
      scan: {
        pipeline_url: null,
        job_url: 'javascript:alert(1)',
        commit_url: null,
        ref_protected: false,
        kind: 'secret-fs',
      },
    });
    renderApp('/scans/7');
    expect(await screen.findByText('Secret scan of the source tree')).toBeInTheDocument();
    expect(screen.getByText('Pipeline #1001')).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'Pipeline #1001' })).toBeNull();
    expect(screen.getByText('Job #5001')).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'Job #5001' })).toBeNull();
    expect(screen.getByText('abcdef01')).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'abcdef01' })).toBeNull();
    expect(screen.queryByText('protected')).toBeNull();
  });

  it('links every posture number to the findings explorer with the exact query', async () => {
    api();
    renderApp('/scans/7');
    await screen.findByText('Posture');
    const base = '/services/3/findings?ref=release%2F1.x';
    const posture = screen.getByRole('region', { name: 'Posture' });
    const list = within(posture).getByRole('list', { name: /Findings by severity/ });
    expect(within(list).getByRole('link', { name: /Critical/ })).toHaveAttribute(
      'href',
      `${base}&severity=CRITICAL`,
    );
    expect(within(list).getByRole('link', { name: /Unknown/ })).toHaveAttribute(
      'href',
      `${base}&severity=UNKNOWN`,
    );
    expect(within(posture).getByRole('link', { name: /Fixable/ })).toHaveAttribute(
      'href',
      `${base}&fixable=true`,
    );
    expect(within(posture).getByRole('link', { name: /Secrets/ })).toHaveAttribute(
      'href',
      `${base}&tab=secrets`,
    );
    expect(within(posture).getByRole('link', { name: /Suppressed/ })).toHaveAttribute(
      'href',
      `${base}&suppressed=true`,
    );
    expect(within(posture).getByRole('link', { name: /Fixable/ })).toHaveTextContent('4');
  });

  it('shows the newer-scan notice only when a newer scan of the same artifact exists', async () => {
    const calls = api({
      scans: () =>
        json(200, {
          items: [{ ...scan, id: 20, artifact_name: 'other:1' }, { ...scan, id: 9 }, { ...scan }],
          next_before: null,
        }),
    }).calls;
    renderApp('/scans/7');
    const notice = await screen.findByText(/A newer scan of this artifact exists/);
    expect(within(notice.closest('p')!).getByRole('link')).toHaveAttribute('href', '/scans/9');
    expect(
      calls.some(
        (c) =>
          c.path.startsWith('/api/v1/services/3/scans?') && c.path.includes('ref=release%2F1.x'),
      ),
    ).toBe(true);
  });

  it('omits the notice for the latest scan and when the lookup fails', async () => {
    api();
    const first = renderApp('/scans/7');
    await screen.findByText('Posture');
    await waitFor(() => expect(screen.queryByText(/newer scan/)).toBeNull());
    first.unmount();

    api({ scans: () => apiError(500, 'internal', 'boom') });
    renderApp('/scans/7');
    await screen.findByText('Posture');
    expect(screen.queryByText(/A newer scan/)).toBeNull();
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('shows summary tiles and diff tabs whose counts equal the API', async () => {
    api();
    renderApp('/scans/7');
    await screen.findByRole('tablist', { name: 'Changes' });
    const tiles = screen.getByRole('region', { name: 'Changes since the previous scan' });
    expect(within(tiles).getByRole('link', { name: /New\s*3/ })).toHaveAttribute(
      'href',
      '/scans/6',
    );
    expect(within(tiles).getByRole('link', { name: /Fixed\s*1/ })).toHaveAttribute(
      'href',
      '/scans/6',
    );
    expect(within(tiles).getByRole('link', { name: /Unchanged\s*12/ })).toHaveAttribute(
      'href',
      '/scans/6',
    );
    expect(screen.getByRole('tab', { name: /New\s*3/ })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('tab', { name: /Fixed\s*1/ })).toBeInTheDocument();

    const table = screen.getByRole('table', { name: 'New findings' });
    const rows = within(table).getAllByRole('row').slice(1);
    // Default sort puts the most severe first.
    expect(rows[0]).toHaveTextContent('CVE-2026-2');
    expect(rows[0]).toHaveTextContent('no fix');
    expect(within(rows[0]).queryByRole('link')).toBeNull();
    expect(within(rows[1]).getByRole('link', { name: 'CVE-2026-3' })).toHaveAttribute(
      'href',
      'https://avd.example.com/CVE-2026-3',
    );
    expect(rows[1]).toHaveTextContent('1.0.0 → 1.0.1');
    expect(rows[1]).toHaveTextContent('alpine 3.19');

    fireEvent.click(within(table).getByRole('button', { name: /Vulnerability/ }));
    expect(within(table).getAllByRole('row')[1]).toHaveTextContent('CVE-2026-1');

    fireEvent.click(screen.getByRole('tab', { name: /Fixed/ }));
    const fixed = screen.getByRole('table', { name: 'Fixed findings' });
    expect(within(fixed).getAllByRole('row')).toHaveLength(2);
  });

  it('shows the empty states for each tab', async () => {
    api({ diff: () => json(200, { ...diff, new: [], fixed: [] }) });
    renderApp('/scans/7');
    expect(await screen.findByText('Nothing new since the previous scan')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('tab', { name: /Fixed/ }));
    expect(screen.getByText('Nothing was fixed since the previous scan')).toBeInTheDocument();
  });

  it('describes the first scan of an artifact and lists its findings', async () => {
    api({ diff: () => json(200, { ...diff, previous_scan_id: null, fixed: [], unchanged: 0 }) });
    renderApp('/scans/7');
    expect(
      await screen.findByText('First scan of this artifact on this ref: all 3 findings are new.'),
    ).toBeInTheDocument();
    expect(screen.queryByRole('tablist')).toBeNull();
    expect(screen.getByRole('table', { name: 'New findings' })).toBeInTheDocument();
  });

  it('switches to the virtual table above 500 rows', async () => {
    const many = Array.from({ length: 501 }, (_, i) => vuln(i + 100));
    api({ diff: () => json(200, { ...diff, new: many }) });
    renderApp('/scans/7');
    const grid = await screen.findByRole('table', { name: 'New findings' });
    expect(grid.tagName).not.toBe('TABLE');
  });

  it('shows Scan not found with a link to the portfolio on 404', async () => {
    mockFetch(async (call) => {
      if (call.path === '/api/v1/me') return json(200, makeUser());
      return apiError(404, 'not_found', 'No such scan.');
    });
    renderApp('/scans/7');
    expect(await screen.findByText('Scan not found')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Back to the portfolio' })).toHaveAttribute(
      'href',
      '/',
    );
  });

  it('keeps sections independent: a failed diff does not hide the scan, and retry works', async () => {
    let fail = true;
    api({ diff: () => (fail ? apiError(500, 'internal', 'Diff failed.') : undefined) });
    renderApp('/scans/7');
    expect(await screen.findByText('Diff failed.')).toBeInTheDocument();
    expect(screen.getByText('Posture')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Pipeline #1001' })).toBeInTheDocument();
    fail = false;
    fireEvent.click(screen.getByRole('button', { name: /try again|retry/i }));
    expect(await screen.findByRole('tablist', { name: 'Changes' })).toBeInTheDocument();
  });

  it('shows a retryable error when the scan itself fails', async () => {
    api({});
    mockFetch(async (call) => {
      if (call.path === '/api/v1/me') return json(200, makeUser());
      return apiError(500, 'internal', 'Scan failed.');
    });
    renderApp('/scans/7');
    expect(await screen.findByText('Scan failed.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /try again|retry/i })).toBeInTheDocument();
  });
});
