// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import type { Vulnerability } from '../../api/findings';
import {
  chooseOption,
  json,
  apiError,
  makeUser,
  mockFetch,
  renderApp,
  type Handler,
} from '../../test-utils';

const SERVICE = {
  id: 5,
  name: 'payments-api',
  gitlab_project_id: 1,
  gitlab_project_path: 'acme/payments-api',
  sonar_project_key: null,
  created_via: 'ingest',
  archived: false,
  created_at: '2026-01-01T00:00:00Z',
  default_ref: null,
};

const OVERVIEW = {
  service: SERVICE,
  ref: 'main',
  ref_source: 'latest',
  refs: ['main', 'release'],
  artifacts: [
    {
      id: 90,
      service_id: 5,
      kind: 'image',
      artifact_key: 'app',
      artifact_name: 'app',
      gitlab_pipeline_id: 1234,
      gitlab_job_id: 1,
      ref: 'main',
      ref_protected: true,
      commit_sha: 'abc',
      trivy_version: '0.58',
      scanned_at: '2026-11-19T10:00:00Z',
      received_at: '2026-11-19T10:01:00Z',
      counts: { critical: 1, high: 3, medium: 1, low: 1, unknown: 0 },
      fixable: 6,
      secrets: 1,
      suppressed: 1,
    },
  ],
  counts: { critical: 1, high: 3, medium: 1, low: 1, unknown: 0 },
  fixable: 6,
  secrets: 1,
  suppressed: 1,
  new_since_previous: 0,
  fixed_since_previous: 0,
  by_class: {},
  top_packages: [],
  data_at: '2026-11-19T10:00:00Z',
};

function vuln(id: number, over: Partial<Vulnerability>): Vulnerability {
  return {
    id,
    scan_id: 11,
    artifact_key: 'app',
    artifact_name: 'app',
    target: 'debian 12',
    class: 'os-pkgs',
    pkg_type: 'debian',
    pkg_name: 'pkg',
    pkg_path: '',
    installed_version: '1.0',
    fixed_version: '',
    vuln_id: `CVE-2024-${String(id).padStart(4, '0')}`,
    severity: 'HIGH',
    status: 'fixed',
    title: `Title ${id}`,
    primary_url: `https://avd.aquasec.com/nvd/cve-2024-${String(id).padStart(4, '0')}`,
    suppressed: false,
    suppression: '',
    first_seen_at: '2026-11-01T00:00:00Z',
    ...over,
  };
}

const VULNS: Vulnerability[] = [
  vuln(1, {
    pkg_name: 'openssl',
    installed_version: '3.0.1',
    fixed_version: '3.0.9',
    severity: 'CRITICAL',
  }),
  vuln(2, { pkg_name: 'openssl', installed_version: '3.0.1', fixed_version: '3.0.10, 3.1.2' }),
  vuln(3, { pkg_name: 'zlib', installed_version: '1.2.9' }),
  vuln(4, {
    pkg_name: 'lodash',
    installed_version: '4.17.0',
    fixed_version: '4.17.21',
    severity: 'LOW',
    class: 'lang-pkgs',
    artifact_key: 'worker',
    artifact_name: 'worker:2',
    primary_url: 'javascript:alert(1)',
  }),
  vuln(5, {
    pkg_name: 'libfoo',
    installed_version: '1.0',
    fixed_version: '1.0.1',
    severity: 'MEDIUM',
    class: 'other',
    artifact_key: 'worker',
    artifact_name: 'worker:2',
  }),
  vuln(6, {
    pkg_name: 'bar',
    installed_version: '2.0',
    fixed_version: '1.2.9',
    class: 'lang-pkgs',
  }),
  vuln(7, {
    pkg_name: 'bar',
    installed_version: '2.0',
    fixed_version: '1.2.10',
    class: 'lang-pkgs',
  }),
];
const SUPPRESSED = vuln(8, {
  pkg_name: 'curl',
  installed_version: '8.0',
  fixed_version: '8.1',
  severity: 'CRITICAL',
  suppressed: true,
  suppression: 'vendor not affected',
});

const SECRETS = {
  items: [
    {
      id: 1,
      scan_id: 11,
      artifact_name: '.',
      target: 'config/prod.env',
      rule_id: 'aws-access-key-id',
      category: 'AWS',
      severity: 'CRITICAL',
      title: 'AWS access key',
      start_line: 12,
      end_line: 14,
      suppressed: false,
    },
  ],
};

const SCAN = {
  ...OVERVIEW.artifacts[0],
  id: 11,
  gitlab_project_path: 'acme/payments-api',
  pipeline_url: 'https://gitlab.example.com/acme/payments-api/-/pipelines/1234',
  job_url: 'https://gitlab.example.com/acme/payments-api/-/jobs/1',
  commit_url: null,
};

/** The same ID in this service and in two others. */
function search(id: string) {
  const hit = (serviceId: number, name: string) => ({
    service: { ...SERVICE, id: serviceId, name },
    ref: 'main',
    artifact_key: 'app',
    artifact_name: 'app',
    scan_id: 100 + serviceId,
    scanned_at: '2026-11-19T10:00:00Z',
    pkg_name: 'openssl',
    installed_version: '3.0.1',
    fixed_version: '3.0.9',
    severity: 'CRITICAL',
  });
  return {
    vuln_id: id,
    scope: 'dashboard',
    items: [hit(5, 'payments-api'), hit(6, 'billing-worker'), hit(7, 'auth-service')],
    truncated: false,
    total_findings: 3,
    total_services: 3,
    total_artifacts: 3,
    counts: { critical: 3, high: 0, medium: 0, low: 0, unknown: 0 },
    title: 'Title',
    primary_url: '',
    fixed_versions: ['3.0.9'],
  };
}

/** Serves the service with vulnerabilities in pages of three, ordered by id. */
function api(extra?: Handler): Handler {
  return async (call) => {
    const custom = await extra?.(call);
    if (custom) return custom;
    const url = new URL(call.path, 'http://x');
    const q = url.searchParams;
    if (url.pathname === '/api/v1/me') return json(200, makeUser());
    if (url.pathname === '/api/v1/services/5') return json(200, SERVICE);
    if (url.pathname === '/api/v1/services/5/overview') return json(200, OVERVIEW);
    if (url.pathname === '/api/v1/services/5/secrets') return json(200, SECRETS);
    if (url.pathname === '/api/v1/scans/11') return json(200, SCAN);
    if (url.pathname === '/api/v1/search/vulnerabilities')
      return json(200, search(q.get('id') ?? ''));
    if (url.pathname === '/api/v1/services/5/vulnerabilities') {
      const all = q.get('suppressed') === 'true' ? [...VULNS, SUPPRESSED] : VULNS;
      const after = Number(q.get('after') ?? 0);
      const rest = all.filter((v) => v.id > after);
      const items = rest.slice(0, 3);
      return json(200, {
        items,
        next_after: rest.length > 3 ? items[items.length - 1].id : null,
      });
    }
    return undefined;
  };
}

function vulnCalls(calls: { path: string }[]) {
  return calls.filter((c) => c.path.startsWith('/api/v1/services/5/vulnerabilities'));
}

const summary = (text: string) => screen.findByText(text, {}, { timeout: 4000 });

beforeEach(() => {
  // jsdom has no layout: give every element a viewport so virtual rows are computed.
  vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockReturnValue(600);
  vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockReturnValue(1200);
  vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(600);
  Element.prototype.scrollTo = vi.fn();
});
afterEach(() => vi.restoreAllMocks());

/** The list row that shows a vulnerability ID. */
function row(id: string) {
  return screen.getByText(id).closest('[role="row"]') as HTMLElement;
}

function bodyRows() {
  return screen.getAllByRole('row').slice(1);
}

describe('findings explorer', () => {
  it('shows the header, loads every page and counts exactly', async () => {
    const { calls } = mockFetch(api());
    renderApp('/services/5/findings');
    expect(await summary('7 vulnerabilities, 6 fixable')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Findings', level: 1 })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'payments-api' })).toHaveAttribute(
      'href',
      '/services/5',
    );
    expect(screen.getByRole('link', { name: 'pipeline #1234' })).toHaveAttribute(
      'href',
      '/scans/90',
    );
    expect(screen.getByRole('combobox', { name: 'Ref' })).toHaveTextContent('main');

    const pages = vulnCalls(calls).map((c) => new URL(c.path, 'http://x').searchParams);
    expect(pages).toHaveLength(3);
    expect(pages.map((p) => p.get('after'))).toEqual([null, '3', '6']);
    for (const p of pages) {
      expect(p.get('limit')).toBe('500');
      expect(p.get('ref')).toBe('main');
      expect(p.get('suppressed')).toBe('false');
      expect(p.get('severity')).toBeNull();
      expect(p.get('q')).toBeNull();
    }
  });

  it('sorts critical first, then fixable first', async () => {
    mockFetch(api());
    renderApp('/services/5/findings');
    await summary('7 vulnerabilities, 6 fixable');
    const ids = bodyRows().map((r) => /CVE-2024-\d{4}/.exec(r.textContent ?? '')?.[0]);
    expect(ids).toEqual([
      'CVE-2024-0001',
      'CVE-2024-0006',
      'CVE-2024-0007',
      'CVE-2024-0002',
      'CVE-2024-0003',
      'CVE-2024-0005',
      'CVE-2024-0004',
    ]);
  });

  it('keeps every column header and gives the version cell its full values in a title', async () => {
    mockFetch(api());
    renderApp('/services/5/findings');
    await summary('7 vulnerabilities, 6 fixable');
    expect(screen.getAllByRole('columnheader').map((h) => h.textContent)).toEqual([
      'Severity',
      'Vulnerability and package',
      'Image and target',
      'First seen',
    ]);
    expect(screen.getByTitle('openssl 3.0.1 → 3.0.10, 3.1.2')).toBeInTheDocument();
    expect(screen.getByTitle('zlib 1.2.9 → no fix')).toBeInTheDocument();
  });

  it('re-sorts by package when the header is activated', async () => {
    mockFetch(api());
    renderApp('/services/5/findings');
    await summary('7 vulnerabilities, 6 fixable');
    fireEvent.click(
      within(screen.getByRole('columnheader', { name: /package/ })).getByRole('button'),
    );
    expect(screen.getByRole('columnheader', { name: /package/ })).toHaveAttribute(
      'aria-sort',
      'ascending',
    );
    expect(bodyRows()[0]).toHaveTextContent('bar');
    expect(bodyRows()[6]).toHaveTextContent('zlib');
  });

  it.each([
    ['?severity=CRITICAL', '1 vulnerability, 1 fixable'],
    ['?severity=CRITICAL,HIGH', '5 vulnerabilities, 4 fixable'],
    ['?fixable=true', '6 vulnerabilities, 6 fixable'],
    ['?fixable=false', '1 vulnerability, 0 fixable'],
    ['?q=lodash', '1 vulnerability, 1 fixable'],
    ['?q=cve-2024-0003', '1 vulnerability, 0 fixable'],
    ['?q=title 5', '1 vulnerability, 1 fixable'],
    ['?class=lang-pkgs', '3 vulnerabilities, 3 fixable'],
    ['?artifact=worker', '2 vulnerabilities, 2 fixable'],
    // A link from before images were named by key still filters.
    ['?artifact=worker:2', '2 vulnerabilities, 2 fixable'],
  ])('applies URL filters %s client-side', async (query, expected) => {
    const { calls } = mockFetch(api());
    renderApp(`/services/5/findings${query}`);
    expect(await summary(expected)).toBeInTheDocument();
    for (const c of vulnCalls(calls)) {
      const p = new URL(c.path, 'http://x').searchParams;
      expect(p.get('severity')).toBeNull();
      expect(p.get('fixable')).toBeNull();
      expect(p.get('q')).toBeNull();
    }
  });

  it('shows images by key, filters them by key and groups them under it', async () => {
    mockFetch(api());
    renderApp('/services/5/findings');
    await summary('7 vulnerabilities, 6 fixable');
    expect(within(row('CVE-2024-0004')).getByText('worker')).toHaveAttribute('title', 'worker:2');
    expect(within(row('CVE-2024-0004')).queryByText('worker:2')).toBeNull();
    const images = screen.getByRole('group', { name: 'Filter by image' });
    expect(within(images).queryByRole('button', { name: /worker:2/ })).toBeNull();
    fireEvent.click(within(images).getByRole('button', { name: /worker/ }));
    expect(await summary('2 vulnerabilities, 2 fixable')).toBeInTheDocument();
    expect(screen.getByTestId('location')).toHaveTextContent('artifact=worker');
    expect(screen.getByTestId('location')).not.toHaveTextContent('worker%3A2');
  });

  it('marks the image chip of an older link that names the tag', async () => {
    mockFetch(api());
    renderApp('/services/5/findings?artifact=worker:2');
    await summary('2 vulnerabilities, 2 fixable');
    const images = screen.getByRole('group', { name: 'Filter by image' });
    expect(within(images).getByRole('button', { name: /worker/ })).toHaveAttribute(
      'aria-pressed',
      'true',
    );
  });

  it('names an image group by key with its latest tag', async () => {
    mockFetch(api());
    renderApp('/services/5/findings?group=image');
    await summary('7 vulnerabilities, 6 fixable');
    const latest = screen.getByText(/^Latest:/);
    expect(latest).toHaveTextContent('Latest: worker:2');
    const header = latest.closest('[role="row"]') as HTMLElement;
    expect(within(header).getByTitle('worker')).toHaveTextContent(/^worker$/);
  });

  it('writes facet changes to the URL and shows facet counts', async () => {
    mockFetch(api());
    renderApp('/services/5/findings');
    await summary('7 vulnerabilities, 6 fixable');
    const severity = screen.getByRole('group', { name: 'Filter by severity' });
    expect(within(severity).getByRole('button', { name: /Critical\s*1/ })).toBeInTheDocument();
    expect(within(severity).getByRole('button', { name: /High\s*4/ })).toBeInTheDocument();
    fireEvent.click(within(severity).getByRole('button', { name: /Critical/ }));
    expect(await summary('1 vulnerability, 1 fixable')).toBeInTheDocument();
    expect(screen.getByTestId('location')).toHaveTextContent('severity=critical');
    fireEvent.click(
      within(screen.getByRole('group', { name: 'Filter by fix availability' })).getByRole(
        'button',
        {
          name: /No fix yet/,
        },
      ),
    );
    expect(await summary('0 vulnerabilities, 0 fixable')).toBeInTheDocument();
    expect(screen.getByText('No vulnerabilities match these filters')).toBeInTheDocument();
    fireEvent.click(screen.getAllByRole('button', { name: 'Clear filters' })[0]);
    expect(await summary('7 vulnerabilities, 6 fixable')).toBeInTheDocument();
    expect(screen.getByTestId('location')).toHaveTextContent('/services/5/findings');
    expect(screen.getByTestId('location').textContent).not.toContain('severity');
  });

  it('puts the search text in the URL', async () => {
    mockFetch(api());
    renderApp('/services/5/findings');
    await summary('7 vulnerabilities, 6 fixable');
    const box = screen.getByRole('searchbox', { name: /Search package/ });
    fireEvent.change(box, { target: { value: 'openssl' } });
    fireEvent.keyDown(box, { key: 'Enter' });
    expect(await summary('2 vulnerabilities, 2 fixable')).toBeInTheDocument();
    expect(screen.getByTestId('location')).toHaveTextContent('q=openssl');
  });

  it('requests suppressed findings only when the toggle is on and marks them', async () => {
    const { calls } = mockFetch(api());
    renderApp('/services/5/findings');
    await summary('7 vulnerabilities, 6 fixable');
    expect(screen.queryByText('vendor not affected')).toBeNull();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Include suppressed' }));
    expect(await summary('8 vulnerabilities, 7 fixable')).toBeInTheDocument();
    expect(
      vulnCalls(calls).some(
        (c) => new URL(c.path, 'http://x').searchParams.get('suppressed') === 'true',
      ),
    ).toBe(true);
    expect(screen.getByTestId('location')).toHaveTextContent('suppressed=true');
    const suppressed = row('CVE-2024-0008');
    expect(within(suppressed).getByText('Suppressed')).toHaveAttribute(
      'title',
      'vendor not affected',
    );
  });

  it('links advisories only for http(s) URLs', async () => {
    mockFetch(api());
    renderApp('/services/5/findings');
    await summary('7 vulnerabilities, 6 fixable');
    fireEvent.click(row('CVE-2024-0001'));
    let dialog = await screen.findByRole('dialog', { name: /CVE-2024-0001/ });
    const link = within(dialog).getByRole('link', { name: /Advisory/ });
    expect(link).toHaveAttribute('href', 'https://avd.aquasec.com/nvd/cve-2024-0001');
    expect(link).toHaveAttribute('target', '_blank');
    expect(link).toHaveAttribute('rel', 'noopener noreferrer');
    fireEvent.keyDown(dialog, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    fireEvent.click(row('CVE-2024-0004'));
    dialog = await screen.findByRole('dialog', { name: /CVE-2024-0004/ });
    expect(within(dialog).queryByRole('link', { name: /Advisory/ })).toBeNull();
    expect(document.querySelector('a[href^="javascript"]')).toBeNull();
  });

  it('shows no-fix and the fixed version in the version column', async () => {
    mockFetch(api());
    renderApp('/services/5/findings');
    await summary('7 vulnerabilities, 6 fixable');
    expect(within(row('CVE-2024-0003')).getByText('no fix yet')).toBeInTheDocument();
    expect(within(row('CVE-2024-0004')).getByText('4.17.21')).toBeInTheDocument();
  });

  it('opens a drawer from the keyboard, traps focus and closes with Escape', async () => {
    mockFetch(api());
    renderApp('/services/5/findings');
    await summary('7 vulnerabilities, 6 fixable');
    const row = bodyRows()[0];
    row.focus();
    fireEvent.keyDown(row, { key: 'Enter' });
    const dialog = await screen.findByRole('dialog', { name: /CVE-2024-0001/ });
    expect(within(dialog).getByText('Title 1')).toBeInTheDocument();
    expect(within(dialog).getByText('debian 12')).toBeInTheDocument();
    // The exact upgrade, as a command to copy.
    expect(
      within(dialog).getByText('apt-get install --only-upgrade openssl=3.0.9'),
    ).toBeInTheDocument();
    // Where else it is: the other two services, through the search API.
    expect(await within(dialog).findByText('2 other services')).toBeInTheDocument();
    expect(within(dialog).getByRole('link', { name: 'billing-worker' })).toHaveAttribute(
      'href',
      '/services/6/findings?q=CVE-2024-0001',
    );
    await within(dialog).findByRole('link', { name: 'Scan details' });
    expect(within(dialog).getByRole('link', { name: /Pipeline #1234/ })).toHaveAttribute(
      'href',
      SCAN.pipeline_url,
    );
    const close = within(dialog).getByRole('button', { name: 'Close details' });
    fireEvent.keyDown(close, { key: 'Escape' });
    expect(screen.queryByRole('dialog')).toBeNull();
    await waitFor(() => expect(bodyRows()[0]).toHaveFocus());
  });

  it('shows the suppression reason in the drawer', async () => {
    mockFetch(api());
    renderApp('/services/5/findings?suppressed=true&q=curl');
    await summary('1 vulnerability, 1 fixable');
    fireEvent.click(row('CVE-2024-0008'));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('Suppressed: vendor not affected')).toBeInTheDocument();
  });

  it('changes the ref through the URL and requests that ref', async () => {
    const { calls } = mockFetch(api());
    renderApp('/services/5/findings');
    await summary('7 vulnerabilities, 6 fixable');
    await chooseOption('Ref', 'release');
    await waitFor(() =>
      expect(calls.some((c) => c.path.includes('/overview?ref=release'))).toBe(true),
    );
    expect(screen.getByTestId('location')).toHaveTextContent('ref=release');
  });

  it('lists secrets with line ranges and the empty state', async () => {
    mockFetch(api());
    renderApp('/services/5/findings?tab=secrets');
    const table = await screen.findByRole('table', { name: 'Secret findings' });
    const secret = within(table).getByRole('row', { name: /aws-access-key-id/ });
    expect(within(secret).getByTitle('config/prod.env:12–14')).toBeInTheDocument();
    expect(within(secret).getByText('AWS')).toBeInTheDocument();
    expect(within(secret).getByText('Critical')).toBeInTheDocument();
    // The matched value is never shown: it is not stored.
    expect(within(secret).getByText('Value not stored')).toBeInTheDocument();
  });

  it('shows the secrets empty state', async () => {
    mockFetch(
      api((c) =>
        c.path.startsWith('/api/v1/services/5/secrets') ? json(200, { items: [] }) : undefined,
      ),
    );
    renderApp('/services/5/findings?tab=secrets');
    expect(await screen.findByText('No secrets found in the current scans')).toBeInTheDocument();
  });

  it('does not load vulnerabilities for the secrets tab', async () => {
    const { calls } = mockFetch(api());
    renderApp('/services/5/findings?tab=secrets');
    await screen.findByRole('table', { name: 'Secret findings' });
    expect(vulnCalls(calls)).toHaveLength(0);
  });

  it('ranks upgrades by the risk they clear, with the highest fixed version', async () => {
    mockFetch(api());
    renderApp('/services/5/findings?tab=fix');
    const list = await screen.findByRole('list', { name: 'Packages to upgrade' });
    // 1 critical × 10 + 4 high × 5 + 1 medium × 3 + 1 low = 34 points; zlib's 5 have no fix.
    expect(
      screen.getByText(/upgrading 4 packages clears 29 of 34 \(85%, 6 findings\)/),
    ).toBeInTheDocument();
    const rows = within(list).getAllByRole('listitem');
    expect(rows.map((r) => within(r).getAllByRole('link')[0].textContent)).toEqual([
      'openssl',
      'bar',
      'libfoo',
      'lodash',
    ]);
    // comma list: 3.1.2 beats 3.0.10 and 3.0.9
    expect(rows[0]).toHaveTextContent('3.0.1');
    expect(rows[0]).toHaveTextContent('3.1.2');
    expect(rows[0]).toHaveTextContent('−15 pts');
    expect(rows[0]).toHaveTextContent('apt-get install --only-upgrade openssl=3.1.2');
    // numeric compare: 1.2.10 beats 1.2.9
    expect(rows[1]).toHaveTextContent('1.2.10');
    expect(rows[1]).not.toHaveTextContent('1.2.9');
    expect(within(rows[0]).getByRole('link', { name: 'openssl' })).toHaveAttribute(
      'href',
      '/services/5/findings?q=openssl',
    );
  });

  it('leaves suppressed findings out of the upgrade list and keeps the ref in links', async () => {
    mockFetch(api());
    renderApp('/services/5/findings?tab=fix&suppressed=true&ref=main');
    const table = await screen.findByRole('list', { name: 'Packages to upgrade' });
    expect(within(table).queryByText('curl')).toBeNull();
    expect(within(table).getByRole('link', { name: 'bar' })).toHaveAttribute(
      'href',
      '/services/5/findings?ref=main&q=bar',
    );
  });

  it('shows the not found state for an unknown service', async () => {
    mockFetch(
      api((c) =>
        c.path.startsWith('/api/v1/services/5')
          ? apiError(404, 'not_found', 'No such service.')
          : undefined,
      ),
    );
    renderApp('/services/5/findings');
    expect(await screen.findByText('Service not found')).toBeInTheDocument();
  });

  it('shows an error with a retry when the vulnerabilities fail', async () => {
    mockFetch(
      api((c) =>
        c.path.startsWith('/api/v1/services/5/vulnerabilities')
          ? apiError(500, 'internal', 'Database unavailable.')
          : undefined,
      ),
    );
    renderApp('/services/5/findings');
    expect(await screen.findByText('Database unavailable.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Try again' })).toBeInTheDocument();
  });

  it('shows the empty state when the ref has no vulnerabilities', async () => {
    mockFetch(
      api((c) =>
        c.path.startsWith('/api/v1/services/5/vulnerabilities')
          ? json(200, { items: [], next_after: null })
          : undefined,
      ),
    );
    renderApp('/services/5/findings');
    expect(await screen.findByText('No vulnerabilities in the current scans')).toBeInTheDocument();
    expect(screen.getByText('0 vulnerabilities, 0 fixable')).toBeInTheDocument();
  });

  it('shows the no-scans state when the service has no ref', async () => {
    mockFetch(
      api((c) =>
        c.path.startsWith('/api/v1/services/5/overview')
          ? json(200, { ...OVERVIEW, ref: null, refs: [], artifacts: [], data_at: null })
          : undefined,
      ),
    );
    renderApp('/services/5/findings');
    expect(await screen.findByText('No scans yet')).toBeInTheDocument();
  });

  it('says there are no scans of a chosen branch and keeps it in the ref list', async () => {
    mockFetch(
      api((c) =>
        c.path.startsWith('/api/v1/services/5/overview')
          ? json(200, {
              ...OVERVIEW,
              ref: 'release/2.x',
              ref_source: 'default',
              artifacts: [],
              data_at: null,
            })
          : undefined,
      ),
    );
    renderApp('/services/5/findings');
    expect(await screen.findByText('No scans of release/2.x yet')).toBeInTheDocument();
    const select = screen.getByRole('combobox', { name: 'Ref' });
    expect(select).toHaveTextContent('release/2.x');
    fireEvent.keyDown(select, { key: 'Enter' });
    expect(
      within(await screen.findByRole('listbox'))
        .getAllByRole('option')
        .map((o) => o.textContent),
    ).toEqual(['main', 'release', 'release/2.x']);
  });
});
