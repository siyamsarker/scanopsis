// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router';
import { createQueryClient } from '../../api/queryClient';
import {
  apiError,
  chooseOption,
  json,
  makeUser,
  mockFetch,
  type RecordedCall,
} from '../../test-utils';
import { ServicePage } from '../ServicePage';

const counts = (critical = 0, high = 0, medium = 0, low = 0, unknown = 0) => ({
  critical,
  high,
  medium,
  low,
  unknown,
});

const service = {
  id: 7,
  name: 'Payments API',
  gitlab_project_id: 12,
  gitlab_project_path: 'platform/payments-api',
  sonar_project_key: 'payments',
  created_via: 'ingest',
  archived: false,
  created_at: '2026-01-01T00:00:00Z',
  default_ref: null,
};

function scan(id: number, overrides: Record<string, unknown> = {}) {
  return {
    id,
    service_id: 7,
    kind: 'image',
    artifact_key: 'registry.example.com/payments/api',
    artifact_name: 'registry.example.com/payments/api:1.4',
    gitlab_pipeline_id: 900 + id,
    gitlab_job_id: 5000 + id,
    ref: 'main',
    ref_protected: true,
    commit_sha: '0123456789abcdef0123456789abcdef01234567',
    trivy_version: '0.58.0',
    scanned_at: '2026-11-18T10:00:00Z',
    received_at: '2026-11-18T10:01:00Z',
    counts: counts(2, 44, 5, 3, 1),
    fixable: 30,
    secrets: 1,
    suppressed: 0,
    ...overrides,
  };
}

// Risk: 2×10 + 44×5 + 5×3 + 3×1 + 1×5 = 263.
const overview = {
  service,
  ref: 'main',
  ref_source: 'latest',
  refs: ['main', 'release/1.x'],
  artifacts: [
    scan(11),
    scan(12, {
      kind: 'secret-fs',
      artifact_key: '.',
      artifact_name: '/builds/x/0/platform/payments-api',
      counts: counts(0, 0, 0, 0, 0),
      secrets: 2,
      scanned_at: '2026-11-18T09:00:00Z',
    }),
  ],
  counts: counts(2, 44, 5, 3, 1),
  fixable: 30,
  secrets: 4,
  suppressed: 2,
  new_since_previous: 6,
  fixed_since_previous: 9,
  by_class: {
    'os-pkgs': counts(1, 20, 2, 1, 0),
    'lang-pkgs': counts(1, 24, 3, 2, 0),
    'secret-class': counts(0, 0, 0, 0, 1),
  },
  top_packages: [
    {
      pkg_name: 'openssl',
      installed_version: '3.0.2',
      fix_version: '3.0.15',
      findings: 12,
      by_severity: counts(1, 8, 3, 0, 0),
      fixable: 12,
      risk: 59,
      risk_cleared: 59,
    },
    {
      pkg_name: 'libxml2 dev',
      installed_version: '2.9.1',
      fix_version: '',
      findings: 3,
      by_severity: counts(0, 1, 2, 0, 0),
      fixable: 0,
      risk: 11,
      risk_cleared: 0,
    },
  ],
  data_at: '2026-11-18T10:00:00Z',
};

function analysis(id: number, overrides: Record<string, unknown> = {}) {
  return {
    id,
    analysed_at: `2026-11-${10 + id}T08:00:00Z`,
    revision: 'abc',
    quality_gate_status: 'ERROR',
    conditions: [
      { metric: 'new_coverage', operator: 'LT', threshold: '80', value: '62.5', status: 'ERROR' },
      { metric: 'coverage', operator: 'LT', threshold: '50', value: '71.4', status: 'OK' },
    ],
    reliability_rating: 'A',
    security_rating: 'C',
    maintainability_rating: null,
    security_review_rating: 'B',
    coverage: 71.4,
    duplicated_lines_density: 3.1,
    measures: { new_coverage: '62.5' },
    mode: 'mqr',
    gitlab_pipeline_id: 911,
    project_url: 'https://sonar.example.com/dashboard?id=payments',
    ...overrides,
  };
}

const sonar = {
  sonar_project_key: 'payments',
  latest: analysis(3),
  history: [
    analysis(3),
    analysis(2, { coverage: 70.0, duplicated_lines_density: 3.6, measures: {} }),
    analysis(1, { coverage: 60 }),
  ],
};

const trendPoint = (id: number, over: Record<string, unknown> = {}) => ({
  scan_id: id,
  kind: 'image',
  ref: 'main',
  scanned_at: `2026-11-1${id - 8}T10:00:00Z`,
  gitlab_pipeline_id: 900 + id,
  commit_sha: `sha${id}`,
  artifact_key: 'registry.example.com/payments/api',
  artifact_name: 'registry.example.com/payments/api:1.4',
  counts: counts(2, 44, 5, 3, 1),
  fixable: 30,
  previous_scan_id: id - 1,
  new: counts(0, 0, 0, 0, 0),
  fixed: 0,
  ...over,
});

const trends = {
  ref: 'main',
  ref_source: 'latest',
  points: [
    trendPoint(9, { previous_scan_id: null, new: counts(1, 40, 5, 3, 1) }),
    trendPoint(10, { new: counts(1, 2, 0, 0, 0), fixed: 3 }),
    trendPoint(11, { new: counts(0, 4, 0, 0, 0), fixed: 9 }),
  ],
  total: 3,
  truncated: false,
};

const scanDetail = {
  ...scan(11),
  gitlab_project_path: 'platform/payments-api',
  pipeline_url: 'https://gitlab.example.com/platform/payments-api/-/pipelines/911',
  job_url: null,
  commit_url: null,
};

interface Opts {
  overrides?: (call: RecordedCall) => Response | undefined;
  user?: Record<string, unknown>;
}

function setup(path = '/services/7', opts: Opts = {}) {
  const mock = mockFetch((call) => {
    const custom = opts.overrides?.(call);
    if (custom) return custom;
    const p = call.path;
    if (p === '/api/v1/me') return json(200, makeUser(opts.user));
    if (p === '/api/v1/services/7') return json(200, service);
    if (p.startsWith('/api/v1/services/7/overview')) return json(200, overview);
    if (p.startsWith('/api/v1/services/7/trends')) return json(200, trends);
    if (p.startsWith('/api/v1/services/7/sonar')) return json(200, sonar);
    if (p.startsWith('/api/v1/services/7/scans'))
      return json(200, { items: [scan(11), scan(12)], next_before: null });
    if (p === '/api/v1/scans/11') return json(200, scanDetail);
    return undefined;
  });
  function Probe() {
    const location = useLocation();
    return <output data-testid="location">{location.pathname + location.search}</output>;
  }
  render(
    <QueryClientProvider client={createQueryClient()}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/services/:id" element={<ServicePage />} />
          <Route path="*" element={null} />
        </Routes>
        <Probe />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return mock;
}

const requested = (calls: RecordedCall[], prefix: string) =>
  calls.filter((c) => c.path.startsWith(prefix)).map((c) => c.path);

describe('service dashboard', () => {
  it('opens with a verdict band: iris, sentence, branch and the ways out', async () => {
    setup();
    expect(await screen.findByRole('heading', { name: 'Payments API', level: 1 })).toBeVisible();
    expect(
      screen.getByRole('img', { name: /^Payments API: Risk 263: 2 critical × 10 = 20/ }),
    ).toBeInTheDocument();
    // The newest image scan (11) compares with scan 10, pipeline 910.
    expect(await screen.findByTestId('verdict')).toHaveTextContent(
      'Failing its quality gate; 2 critical; 9 fixed and 6 new since pipeline #910.',
    );
    expect(screen.getByTestId('ref-source')).toHaveTextContent(
      'Showing main, the latest scanned branch; no dashboard branch is chosen',
    );
    expect(screen.getByRole('link', { name: /Explore findings/ })).toHaveAttribute(
      'href',
      '/services/7/findings?ref=main',
    );
    expect(await screen.findByRole('link', { name: /Open pipeline #911/ })).toHaveAttribute(
      'href',
      'https://gitlab.example.com/platform/payments-api/-/pipelines/911',
    );
    expect(
      screen.getByRole('link', { name: /Open platform\/payments-api in GitLab/ }),
    ).toHaveAttribute('href', 'https://gitlab.example.com/platform/payments-api');
    expect(screen.getAllByRole('link', { name: /Open in SonarQube/ })[0]).toHaveAttribute(
      'href',
      'https://sonar.example.com/dashboard?id=payments',
    );
    expect(screen.getByRole('link', { name: 'pipeline #911' })).toHaveAttribute(
      'href',
      '/scans/11',
    );
    expect(screen.getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent(
      'PortfolioPayments API',
    );
  });

  it('switches the branch, refetching with it and writing it to the URL', async () => {
    const { calls } = setup();
    await screen.findByTestId('verdict');
    await chooseOption('Branch', 'release/1.x');
    expect(screen.getByTestId('location')).toHaveTextContent('/services/7?ref=release%2F1.x');
    await waitFor(() =>
      expect(requested(calls, '/api/v1/services/7/overview')).toContain(
        '/api/v1/services/7/overview?ref=release%2F1.x',
      ),
    );
    expect(await screen.findByRole('link', { name: /Explore findings/ })).toHaveAttribute(
      'href',
      '/services/7/findings?ref=release%2F1.x',
    );
  });

  it('draws the pipelines and opens the scan of a chosen pipeline', async () => {
    const { calls } = setup('/services/7?days=30');
    const strip = await screen.findByRole('group', {
      name: 'Pipelines on main in the last 30 days: pipelines',
    });
    expect(requested(calls, '/api/v1/services/7/trends')).toContain(
      '/api/v1/services/7/trends?days=30',
    );
    // The first scan is the starting point; the other two add 7 and fix 12.
    expect(screen.getByTestId('strip-totals')).toHaveTextContent(
      '3 pipelines on main in the last 30 days: 7 new and 12 fixed. The first scan of an artifact is its starting point and does not count as new.',
    );
    const column = within(strip).getByRole('button', { name: /^Pipeline #910/ });
    expect(column).toHaveAccessibleName(expect.stringContaining('3 new findings'));
    // The analysis of pipeline 911 gives its gate and coverage on new code.
    expect(within(strip).getByRole('button', { name: /^Pipeline #911/ })).toHaveAccessibleName(
      expect.stringContaining('gate failed, coverage on new code 62.5%'),
    );
    fireEvent.click(column);
    expect(screen.getByTestId('location')).toHaveTextContent('/scans/10');
  });

  it('asks for every branch and a longer range through the URL', async () => {
    const { calls } = setup();
    await screen.findByRole('group', { name: /Pipelines on main/ });
    fireEvent.click(screen.getByRole('radio', { name: 'All branches' }));
    expect(screen.getByTestId('location')).toHaveTextContent('?branches=all');
    await waitFor(() =>
      expect(requested(calls, '/api/v1/services/7/trends')).toContain(
        '/api/v1/services/7/trends?days=90&all_refs=true',
      ),
    );
    fireEvent.click(screen.getByRole('radio', { name: '180 days' }));
    expect(screen.getByTestId('location')).toHaveTextContent('?branches=all&days=180');
  });

  it('says when the strip holds only the newest scans', async () => {
    setup('/services/7', {
      overrides: (call) =>
        call.path.startsWith('/api/v1/services/7/trends')
          ? json(200, { ...trends, total: 412, truncated: true })
          : undefined,
    });
    expect(await screen.findByTestId('strip-totals')).toHaveTextContent(
      'The strip holds the newest 3 of the 412 scans in this range.',
    );
  });

  it('lists the upgrades that clear the most risk, linked to their findings', async () => {
    setup();
    const list = await screen.findByRole('list', { name: 'Packages to upgrade' });
    const rows = within(list).getAllByRole('link');
    expect(rows[0]).toHaveTextContent('openssl');
    expect(rows[0]).toHaveTextContent('3.0.2 → upgrade to 3.0.15');
    expect(rows[0]).toHaveTextContent('−59 risk');
    expect(rows[0]).toHaveAttribute('href', '/services/7/findings?ref=main&q=openssl');
    expect(rows[1]).toHaveTextContent('No fixed version yet');
    expect(screen.getByText(/These upgrades clear 59 of 263 risk points/)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'See every upgrade' })).toHaveAttribute(
      'href',
      '/services/7/findings?ref=main&tab=fix',
    );
  });

  it('breaks the open findings down once, with links', async () => {
    setup();
    const panel = within(await screen.findByRole('region', { name: 'Open findings' }));
    expect(panel.getByText(/Current state of/)).toHaveTextContent(
      'Current state of main, latest scan per artifact',
    );
    expect(panel.getByRole('link', { name: /30 fixable/ })).toHaveAttribute(
      'href',
      '/services/7/findings?ref=main&fixable=true',
    );
    expect(panel.getByRole('link', { name: /4 secrets/ })).toHaveAttribute(
      'href',
      '/services/7/findings?ref=main&tab=secrets',
    );
    expect(panel.getByRole('link', { name: /2 suppressed/ })).toHaveAttribute(
      'href',
      '/services/7/findings?ref=main&suppressed=true',
    );
    expect(panel.getByText('OS packages').parentElement).toHaveTextContent('24');
    expect(panel.getByText('Language packages').parentElement).toHaveTextContent('30');
    expect(panel.getByText('Other')).toBeInTheDocument();
    const source = panel.getByRole('link', { name: /Source tree/ });
    expect(source).toHaveTextContent('2 secrets');
    expect(source).not.toHaveTextContent('vulnerabilities');
    expect(source).toHaveAttribute('href', '/scans/12');
    // The image by its key, the latest tag as a detail.
    const image = panel.getByRole('link', { name: /^registry\.example\.com\/payments\/api(?!:)/ });
    expect(image).toHaveTextContent('55 vulnerabilities');
    expect(within(image).getByText(/^Latest:/)).toHaveTextContent(
      'Latest: registry.example.com/payments/api:1.4',
    );
    expect(within(source).queryByText(/^Latest:/)).toBeNull();
  });

  it('shows the quality gate with new code and overall labelled, and measure changes', async () => {
    setup();
    const panel = within(await screen.findByRole('region', { name: 'Code quality' }));
    const conditions = panel.getByRole('table', { name: 'Quality gate conditions' });
    const rows = within(conditions).getAllByRole('row').slice(1);
    expect(rows[0]).toHaveTextContent(
      'Failed: Coverage on new codeneeds at least 80%New code62.5%',
    );
    expect(rows[1]).toHaveTextContent('Passed: Coverageneeds at least 50%Overall71.4%');
    expect(panel.getByText('Failed')).toBeInTheDocument();
    // Coverage moved from 70.0 to 71.4; duplication from 3.6 to 3.1.
    expect(
      panel.getByText('up 1.4 percentage points since the analysis before, better'),
    ).toBeInTheDocument();
    expect(
      panel.getByText('down 0.5 percentage points since the analysis before, better'),
    ).toBeInTheDocument();
    expect(panel.getByText('in pipeline #911', { exact: false })).toBeInTheDocument();
    expect(panel.getAllByText('Rating')).toHaveLength(3);
    expect(panel.getByText('Not available')).toBeInTheDocument();
  });

  it('does not link an unsafe SonarQube URL', async () => {
    setup('/services/7', {
      overrides: (call) =>
        call.path.startsWith('/api/v1/services/7/sonar')
          ? json(200, { ...sonar, latest: analysis(3, { project_url: 'javascript:alert(1)' }) })
          : undefined,
    });
    await screen.findByRole('region', { name: 'Code quality' });
    expect(screen.queryByRole('link', { name: /Open in SonarQube/ })).not.toBeInTheDocument();
  });

  it('explains how to link SonarQube, with the Services link only for admins', async () => {
    setup('/services/7', {
      overrides: (call) =>
        call.path.startsWith('/api/v1/services/7/sonar')
          ? json(200, { sonar_project_key: null, latest: null, history: [] })
          : undefined,
    });
    expect(await screen.findByText('No SonarQube project linked')).toBeInTheDocument();
    expect(await screen.findByRole('link', { name: 'Open the Services page' })).toHaveAttribute(
      'href',
      '/admin/services',
    );
    expect(screen.getByRole('link', { name: 'Read the SonarQube guide' })).toBeInTheDocument();
  });

  it('tells viewers what a linked project without analysis means', async () => {
    setup('/services/7', {
      user: { role: 'viewer' },
      overrides: (call) =>
        call.path.startsWith('/api/v1/services/7/sonar')
          ? json(200, { sonar_project_key: 'payments', latest: null, history: [] })
          : undefined,
    });
    expect(await screen.findByText('No SonarQube analysis yet')).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'Open the Services page' })).not.toBeInTheDocument();
  });

  it('says which branch the dashboards follow', async () => {
    setup('/services/7', {
      overrides: (call) =>
        call.path === '/api/v1/services/7'
          ? json(200, { ...service, default_ref: 'main' })
          : call.path.startsWith('/api/v1/services/7/overview')
            ? json(200, { ...overview, ref_source: 'default' })
            : undefined,
    });
    expect(await screen.findByTestId('ref-source')).toHaveTextContent(
      'Showing main, the dashboard branch',
    );
  });

  it('shows the no-scans state of a chosen branch and keeps SonarQube', async () => {
    setup('/services/7', {
      overrides: (call) =>
        call.path.startsWith('/api/v1/services/7/overview')
          ? json(200, {
              ...overview,
              ref: 'release/9',
              ref_source: 'default',
              artifacts: [],
              counts: counts(),
              top_packages: [],
              data_at: null,
            })
          : undefined,
    });
    expect(await screen.findByText('No scans of release/9 yet')).toBeInTheDocument();
    expect(screen.getByText(/An administrator chose this branch/)).toBeInTheDocument();
    expect(screen.getByTestId('verdict')).toHaveTextContent(
      'Failing its quality gate; no scans of release/9 yet.',
    );
    expect(screen.queryByRole('link', { name: /Explore findings/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('region', { name: 'Fix first' })).not.toBeInTheDocument();
    expect(await screen.findByRole('region', { name: 'Code quality' })).toBeInTheDocument();
  });

  it('lists recent scans and loads more with next_before', async () => {
    const { calls } = setup('/services/7', {
      overrides: (call) => {
        if (call.path === '/api/v1/services/7/scans')
          return json(200, { items: [scan(11)], next_before: 11 });
        if (call.path === '/api/v1/services/7/scans?before=11')
          return json(200, { items: [scan(5)], next_before: null });
        return undefined;
      },
    });
    const table = await screen.findByRole('table', { name: 'Recent scans' });
    await within(table).findByText('#911');
    // Each scan names its image by key and the tag it scanned below it.
    const link = within(table).getByRole('link', {
      name: /Open the scan of .*:1\.4 from pipeline 911/,
    });
    expect(link).toHaveTextContent(
      /^registry\.example\.com\/payments\/apiregistry\.example\.com\/payments\/api:1\.4$/,
    );
    expect(within(link).getByText(':1.4')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Load more scans' }));
    expect(await within(table).findByText('#905')).toBeInTheDocument();
    expect(requested(calls, '/api/v1/services/7/scans')).toContain(
      '/api/v1/services/7/scans?before=11',
    );
  });

  it('shows a not found state with a link to the portfolio', async () => {
    setup('/services/7', {
      overrides: (call) =>
        call.path === '/api/v1/services/7'
          ? apiError(404, 'not_found', 'No such service.')
          : undefined,
    });
    expect(await screen.findByText('Service not found')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Back to the portfolio' })).toHaveAttribute(
      'href',
      '/',
    );
  });

  it('rejects an id that is not a number', () => {
    setup('/services/abc');
    expect(screen.getByText('Service not found')).toBeInTheDocument();
  });

  it('keeps the other sections when one endpoint fails, and retries just that one', async () => {
    let failSonar = true;
    setup('/services/7', {
      overrides: (call) =>
        call.path.startsWith('/api/v1/services/7/sonar') && failSonar
          ? apiError(500, 'internal', 'boom')
          : undefined,
    });
    // The other sections are intact.
    expect(await screen.findByRole('region', { name: 'Fix first' })).toBeInTheDocument();
    expect(
      await screen.findByRole('heading', { name: 'Code quality could not be loaded' }),
    ).toBeVisible();
    failSonar = false;
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByRole('region', { name: 'Code quality' })).toBeInTheDocument();
  });

  it('shows a Trivy error on its own when the overview fails', async () => {
    setup('/services/7', {
      overrides: (call) =>
        call.path.startsWith('/api/v1/services/7/overview')
          ? apiError(500, 'internal', 'boom')
          : undefined,
    });
    expect(await screen.findByText('Trivy findings could not be loaded')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Payments API', level: 1 })).toBeInTheDocument();
  });
});
