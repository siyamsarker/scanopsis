// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import type { Scan, ScanDiff, ScanSummary, Vulnerability } from '../../api/scan';
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

/** An earlier scan of the same artifact. */
function earlier(id: number, over: Partial<ScanSummary> = {}): ScanSummary {
  return {
    id,
    service_id: 3,
    kind: 'image',
    artifact_name: scan.artifact_name,
    gitlab_pipeline_id: 990 + id,
    gitlab_job_id: 4000 + id,
    ref: 'release/1.x',
    ref_protected: true,
    commit_sha: 'aaaa',
    trivy_version: '0.58.1',
    scanned_at: `2026-11-${String(10 + id).padStart(2, '0')}T10:00:00Z`,
    received_at: '2026-11-10T10:01:00Z',
    counts: { critical: 1, high: 5, medium: 0, low: 1, unknown: 0 },
    fixable: 4,
    secrets: 0,
    suppressed: 0,
    ...over,
  };
}

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
    title: `Title ${id}`,
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
  base: earlier(6),
  new: [
    vuln(2, { severity: 'CRITICAL', fixed_version: '' }),
    vuln(3),
    vuln(1, { severity: 'LOW' }),
  ],
  fixed: [vuln(4, { scan_id: 6 })],
  unchanged_items: [vuln(5, { severity: 'CRITICAL' })],
  unchanged: 12,
  new_total: 3,
  fixed_total: 1,
  truncated: false,
  new_counts: { critical: 1, high: 1, medium: 0, low: 1, unknown: 0 },
  fixed_counts: { critical: 0, high: 1, medium: 0, low: 0, unknown: 0 },
  unchanged_counts: { critical: 1, high: 11, medium: 0, low: 0, unknown: 0 },
};

function api(
  over: {
    scan?: Partial<Scan>;
    diff?: Handler;
    scans?: Handler;
    earlier?: Handler;
  } = {},
) {
  const handler: Handler = async (call) => {
    if (call.path === '/api/v1/me') return json(200, makeUser({ role: 'viewer' }));
    if (call.path === '/api/v1/scans/7') return json(200, { ...scan, ...over.scan });
    if (call.path.startsWith('/api/v1/scans/7/diff'))
      return (await over.diff?.(call)) ?? json(200, diff);
    if (call.path.startsWith('/api/v1/scans/7/earlier')) {
      return (
        (await over.earlier?.(call)) ??
        json(200, {
          items: [earlier(6), earlier(5, { ref: 'feature/x', ref_protected: false }), earlier(4)],
          next_before: null,
        })
      );
    }
    if (call.path === '/api/v1/services/3') {
      return json(200, {
        id: 3,
        name: 'Payments API',
        gitlab_project_id: 1,
        gitlab_project_path: 'pay/api',
        sonar_project_key: null,
        created_via: 'ingest',
        archived: false,
        created_at: '2026-01-01T00:00:00Z',
        default_ref: null,
      });
    }
    if (call.path.startsWith('/api/v1/services/3/scans')) {
      return (await over.scans?.(call)) ?? json(200, { items: [{ ...scan }], next_before: null });
    }
    return undefined;
  };
  return mockFetch(handler);
}

const diffCalls = (calls: { path: string }[]) =>
  calls.filter((c) => c.path.startsWith('/api/v1/scans/7/diff')).map((c) => c.path);

describe('scan detail', () => {
  it('shows header facts with exact links and the protected tag', async () => {
    const { calls } = api();
    renderApp('/scans/7');
    expect(await screen.findByRole('heading', { level: 1 })).toHaveTextContent(scan.artifact_name);
    expect(screen.getByText(/Image scanned/)).toBeInTheDocument();
    const crumbs = screen.getByRole('navigation', { name: 'Breadcrumb' });
    expect(within(crumbs).getByRole('link', { name: 'Portfolio' })).toHaveAttribute('href', '/');
    expect(await within(crumbs).findByRole('link', { name: 'Payments API' })).toHaveAttribute(
      'href',
      '/services/3',
    );
    expect(screen.getByRole('link', { name: /#1001/ })).toHaveAttribute('href', scan.pipeline_url);
    expect(screen.getByRole('link', { name: /#5001/ })).toHaveAttribute('href', scan.job_url);
    expect(screen.getByRole('link', { name: /abcdef01/ })).toHaveAttribute('href', scan.commit_url);
    expect(screen.getByRole('button', { name: 'Copy commit' })).toBeInTheDocument();
    expect(screen.getAllByText('release/1.x').length).toBeGreaterThan(0);
    expect(screen.getByText('protected')).toBeInTheDocument();
    expect(screen.getByText('0.58.1')).toBeInTheDocument();
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
        artifact_name: '.',
        secrets: 2,
      },
    });
    renderApp('/scans/7');
    expect(await screen.findByText(/Secret scan of the source tree/)).toBeInTheDocument();
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Source tree');
    expect(screen.getByText('#1001')).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: /#1001/ })).toBeNull();
    expect(screen.queryByRole('link', { name: /#5001/ })).toBeNull();
    expect(screen.getByText('abcdef01')).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: /abcdef01/ })).toBeNull();
    expect(screen.queryByText('protected')).toBeNull();
    expect(document.querySelector('a[href^="javascript"]')).toBeNull();
    // A secret scan states its secrets, not a risk number, and has no picker.
    expect(screen.getByText('secrets found in the source tree')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Compare with/ })).toBeNull();
    // Diffs compare vulnerabilities, which a secret scan does not have.
    expect(screen.queryByRole('region', { name: 'What changed' })).toBeNull();
  });

  it('links every posture number to the findings explorer with the exact query', async () => {
    api();
    renderApp('/scans/7');
    const posture = await screen.findByRole('region', { name: 'Posture' });
    const base = '/services/3/findings?ref=release%2F1.x';
    expect(within(posture).getByText(/8 open findings/)).toBeInTheDocument();
    // 2 critical × 10 + 5 high × 5 + 1 low = 46.
    expect(within(posture).getByText(/^Risk 46: 2 critical × 10 = 20/)).toBeInTheDocument();
    const hrefs = within(posture)
      .getAllByRole('link')
      .map((l) => l.getAttribute('href'));
    expect(hrefs).toContain(`${base}&severity=CRITICAL`);
    expect(hrefs).toContain(`${base}&severity=LOW`);
    expect(within(posture).getByRole('link', { name: /fixable/ })).toHaveAttribute(
      'href',
      `${base}&fixable=true`,
    );
    expect(within(posture).getByRole('link', { name: /fixable/ })).toHaveTextContent('4');
    expect(within(posture).getByRole('link', { name: /secret/ })).toHaveAttribute(
      'href',
      `${base}&tab=secrets`,
    );
    expect(within(posture).getByRole('link', { name: /suppressed/ })).toHaveAttribute(
      'href',
      `${base}&suppressed=true`,
    );
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
    await screen.findByRole('region', { name: 'Posture' });
    await waitFor(() => expect(screen.queryByText(/newer scan/)).toBeNull());
    first.unmount();

    api({ scans: () => apiError(500, 'internal', 'boom') });
    renderApp('/scans/7');
    await screen.findByRole('region', { name: 'Posture' });
    expect(screen.queryByText(/A newer scan/)).toBeNull();
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('compares with the previous scan: counts, net risk and tabs equal the API', async () => {
    api();
    renderApp('/scans/7');
    const changes = await screen.findByRole('region', { name: 'What changed' });
    expect(
      within(changes).getByRole('link', { name: /pipeline #996 on release\/1.x/ }),
    ).toHaveAttribute('href', '/scans/6');
    // Before: 1 critical, 5 high, 1 low = 36 points; after: 46.
    expect(within(changes).getByText('36 → 46 points')).toBeInTheDocument();
    expect(within(changes).getByText(/up 10 risk points/)).toBeInTheDocument();
    const chart = within(changes).getByRole('table', {
      name: 'New and fixed findings by severity',
    });
    expect(within(chart).getByRole('row', { name: /Critical 0 1/ })).toBeInTheDocument();
    expect(within(chart).getByRole('row', { name: /High 1 1/ })).toBeInTheDocument();

    expect(screen.getByRole('tab', { name: /New\s*3/ })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('tab', { name: /Fixed\s*1/ })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: /Unchanged\s*12/ })).toBeInTheDocument();
    const rows = within(screen.getByRole('list', { name: 'New findings' })).getAllByRole(
      'listitem',
    );
    expect(rows[0]).toHaveTextContent('CVE-2026-2');
    expect(rows[0]).toHaveTextContent('no fix yet');
    expect(rows[1]).toHaveTextContent('pkg3@1.0.0→ fixed in 1.0.1');

    fireEvent.click(screen.getByRole('tab', { name: /Unchanged/ }));
    expect(
      within(screen.getByRole('list', { name: 'Unchanged findings' })).getAllByRole('listitem'),
    ).toHaveLength(1);
    expect(screen.getByText('Showing the 1 most severe of 12 findings.')).toBeInTheDocument();
  });

  it('opens a finding of the comparison in the details sheet', async () => {
    api();
    renderApp('/scans/7');
    const list = await screen.findByRole('list', { name: 'New findings' });
    fireEvent.click(within(list).getByRole('button', { name: /CVE-2026-3/ }));
    const dialog = await screen.findByRole('dialog', { name: /CVE-2026-3/ });
    expect(within(dialog).getByText('apk add --upgrade pkg3=1.0.1')).toBeInTheDocument();
  });

  it('opens on the first list with findings', async () => {
    api({ diff: () => json(200, { ...diff, new: [], new_total: 0 }) });
    renderApp('/scans/7');
    expect(await screen.findByRole('tab', { name: /Fixed\s*1/ })).toHaveAttribute(
      'aria-selected',
      'true',
    );
    fireEvent.click(screen.getByRole('tab', { name: /New/ }));
    expect(screen.getByText('Nothing new in this scan')).toBeInTheDocument();
  });

  it('compares with any earlier scan chosen in the picker, through the URL', async () => {
    const { calls } = api({
      diff: (call) =>
        call.path.includes('base=5')
          ? json(200, {
              ...diff,
              previous_scan_id: 5,
              base: earlier(5, { ref: 'feature/x', ref_protected: false }),
            })
          : undefined,
    });
    renderApp('/scans/7');
    fireEvent.click(await screen.findByRole('button', { name: /Compare with pipeline #996/ }));
    const options = await screen.findByRole('listbox', { name: 'Earlier scans' });
    const items = within(options).getAllByRole('option');
    expect(items).toHaveLength(3);
    expect(items[0]).toHaveAttribute('aria-selected', 'true');
    expect(items[0]).toHaveTextContent('Previous');
    expect(items[1]).toHaveTextContent('feature/x');

    fireEvent.click(items[1]);
    expect(
      await screen.findByRole('link', { name: /pipeline #995 on feature\/x/ }),
    ).toBeInTheDocument();
    expect(screen.getByTestId('location')).toHaveTextContent('/scans/7?base=5');
    expect(diffCalls(calls)).toContain('/api/v1/scans/7/diff?base=5');

    // Only this ref.
    fireEvent.click(screen.getByRole('button', { name: /Compare with pipeline #995/ }));
    fireEvent.click(await screen.findByRole('checkbox', { name: /Only/ }));
    expect(within(screen.getByRole('listbox')).getAllByRole('option')).toHaveLength(2);
    // Back to the previous scan: the parameter leaves the URL.
    fireEvent.click(within(screen.getByRole('listbox')).getAllByRole('option')[0]);
    await waitFor(() => expect(screen.getByTestId('location')).toHaveTextContent(/^\/scans\/7$/));
  });

  it('explains a base that cannot be compared and goes back to the previous scan', async () => {
    api({
      diff: (call) =>
        call.path.includes('base=')
          ? apiError(400, 'invalid_parameter', 'The base parameter must be …')
          : undefined,
    });
    renderApp('/scans/7?base=99');
    expect(await screen.findByText('That scan cannot be compared')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Compare with the previous scan' }));
    expect(await screen.findByRole('tab', { name: /New\s*3/ })).toBeInTheDocument();
    expect(screen.getByTestId('location')).toHaveTextContent(/^\/scans\/7$/);
  });

  it('says so when the lists of changes were cut, and counts every change', async () => {
    api({
      diff: async () => json(200, { ...diff, new_total: 1200, fixed_total: 3, truncated: true }),
    });
    renderApp('/scans/7');
    expect(
      await screen.findByText('Showing the 3 most severe of 1,200 findings.'),
    ).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: /New\s*1,200/ })).toBeInTheDocument();
  });

  it('describes the first scan of an artifact and lists its findings', async () => {
    api({
      diff: () =>
        json(200, {
          ...diff,
          previous_scan_id: null,
          base: null,
          fixed: [],
          fixed_total: 0,
          unchanged_items: [],
          unchanged: 0,
        }),
    });
    renderApp('/scans/7');
    expect(
      await screen.findByText('First scan of this artifact on this ref: every finding is new.'),
    ).toBeInTheDocument();
    expect(screen.getByRole('list', { name: 'New findings' })).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: 'Choose a scan to compare with' }),
    ).toBeInTheDocument();
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
    expect(screen.getByRole('region', { name: 'Posture' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /#1001/ })).toBeInTheDocument();
    fail = false;
    fireEvent.click(screen.getByRole('button', { name: /try again|retry/i }));
    expect(await screen.findByRole('tablist', { name: 'Changes' })).toBeInTheDocument();
  });

  it('shows a retryable error when the scan itself fails', async () => {
    mockFetch(async (call) => {
      if (call.path === '/api/v1/me') return json(200, makeUser());
      return apiError(500, 'internal', 'Scan failed.');
    });
    renderApp('/scans/7');
    expect(await screen.findByText('Scan failed.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /try again|retry/i })).toBeInTheDocument();
  });
});
