// Copyright 2026 The Scanopsis Authors
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
    artifact_name: 'worker',
    primary_url: 'javascript:alert(1)',
  }),
  vuln(5, {
    pkg_name: 'libfoo',
    installed_version: '1.0',
    fixed_version: '1.0.1',
    severity: 'MEDIUM',
    class: 'other',
    artifact_name: 'worker',
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

const summary = (text: string) => screen.findByText(text);

beforeEach(() => {
  // jsdom has no layout: give every element a viewport so virtual rows are computed.
  vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockReturnValue(600);
  vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockReturnValue(1200);
  vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(600);
  Element.prototype.scrollTo = vi.fn();
});
afterEach(() => vi.restoreAllMocks());

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
    expect(screen.getByRole('link', { name: 'Pipeline 1234' })).toHaveAttribute(
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
      'Vulnerability',
      'Package',
      'Installed → fixed',
      'Target',
      'Artifact',
      'First seen',
      'Status',
    ]);
    expect(screen.getByTitle('3.0.1 → 3.0.10, 3.1.2')).toBeInTheDocument();
    expect(screen.getByTitle('1.2.9 → No fix')).toBeInTheDocument();
  });

  it('re-sorts by package when the header is activated', async () => {
    mockFetch(api());
    renderApp('/services/5/findings');
    await summary('7 vulnerabilities, 6 fixable');
    fireEvent.click(
      within(screen.getByRole('columnheader', { name: /Package/ })).getByRole('button'),
    );
    expect(screen.getByRole('columnheader', { name: /Package/ })).toHaveAttribute(
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
          name: /No fix/,
        },
      ),
    );
    expect(await summary('0 vulnerabilities, 0 fixable')).toBeInTheDocument();
    expect(screen.getByText('No vulnerabilities match these filters')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Clear filters' }));
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
    const row = screen.getByText('vendor not affected').closest('[role="row"]') as HTMLElement;
    expect(within(row).getByText('Suppressed')).toBeInTheDocument();
    expect(row).toHaveTextContent('CVE-2024-0008');
  });

  it('links advisories only for http(s) URLs', async () => {
    mockFetch(api());
    renderApp('/services/5/findings');
    await summary('7 vulnerabilities, 6 fixable');
    const link = screen.getByRole('link', { name: /CVE-2024-0001/ });
    expect(link).toHaveAttribute('href', 'https://avd.aquasec.com/nvd/cve-2024-0001');
    expect(link).toHaveAttribute('target', '_blank');
    expect(link).toHaveAttribute('rel', 'noopener noreferrer');
    expect(screen.getByText('CVE-2024-0004')).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: /CVE-2024-0004/ })).toBeNull();
    expect(document.querySelector('a[href^="javascript"]')).toBeNull();
  });

  it('shows no-fix and the fixed version in the version column', async () => {
    mockFetch(api());
    renderApp('/services/5/findings');
    await summary('7 vulnerabilities, 6 fixable');
    const zlib = screen
      .getByRole('button', { name: 'zlib' })
      .closest('[role="row"]') as HTMLElement;
    expect(within(zlib).getByText('No fix')).toBeInTheDocument();
    const lodash = screen
      .getByRole('button', { name: 'lodash' })
      .closest('[role="row"]') as HTMLElement;
    expect(within(lodash).getByText('4.17.21')).toBeInTheDocument();
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
    const close = within(dialog).getByRole('button', { name: 'Close details' });
    expect(close).toHaveFocus();
    fireEvent.keyDown(close, { key: 'Tab', shiftKey: true });
    expect(within(dialog).getByRole('link', { name: 'Scan 11' })).toHaveFocus();
    fireEvent.keyDown(within(dialog).getByRole('link', { name: 'Scan 11' }), { key: 'Tab' });
    expect(close).toHaveFocus();
    fireEvent.keyDown(close, { key: 'Escape' });
    expect(screen.queryByRole('dialog')).toBeNull();
    await waitFor(() => expect(bodyRows()[0]).toHaveFocus());
  });

  it('shows the suppression reason in the drawer', async () => {
    mockFetch(api());
    renderApp('/services/5/findings?suppressed=true&q=curl');
    await summary('1 vulnerability, 1 fixable');
    fireEvent.click(screen.getByRole('button', { name: 'curl' }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('vendor not affected')).toBeInTheDocument();
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
    const row = within(table).getByRole('row', { name: /aws-access-key-id/ });
    expect(within(row).getByText('12–14')).toBeInTheDocument();
    expect(within(row).getByText('config/prod.env')).toBeInTheDocument();
    expect(within(row).getByText('AWS')).toBeInTheDocument();
    expect(within(row).getByText('Critical')).toBeInTheDocument();
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

  it('groups fixable findings into upgrades in order with the highest fixed version', async () => {
    mockFetch(api());
    renderApp('/services/5/findings?tab=fix');
    const table = await screen.findByRole('table', { name: 'Packages to upgrade' });
    expect(
      await screen.findByText('Upgrading 4 packages clears 6 of 6 fixable vulnerabilities'),
    ).toBeInTheDocument();
    const rows = within(table).getAllByRole('row').slice(1);
    expect(rows.map((r) => within(r).getAllByRole('cell')[1].textContent)).toEqual([
      'openssl',
      'bar',
      'libfoo',
      'lodash',
    ]);
    // comma list: 3.1.2 beats 3.0.10 and 3.0.9
    expect(rows[0]).toHaveTextContent('3.0.1');
    expect(rows[0]).toHaveTextContent('3.1.2');
    expect(rows[0]).toHaveTextContent('2 findings');
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
    const table = await screen.findByRole('table', { name: 'Packages to upgrade' });
    await screen.findByText('Upgrading 4 packages clears 6 of 6 fixable vulnerabilities');
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
