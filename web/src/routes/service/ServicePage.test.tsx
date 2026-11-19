// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router';
import { createQueryClient } from '../../api/queryClient';
import { readPalette } from '../../charts/palette';
import type { ChartProps } from '../../charts/Chart';
import { apiError, json, makeUser, mockFetch, type RecordedCall } from '../../test-utils';
import { ServicePage } from '../ServicePage';

const chartCalls: ChartProps[] = [];
vi.mock('../../charts/Chart', () => ({
  Chart: (props: ChartProps) => {
    chartCalls.push(props);
    return (
      <div data-testid="chart" data-status={props.status}>
        {props.ariaLabel}
      </div>
    );
  },
}));

const palette = readPalette();

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
};

function scan(id: number, overrides: Record<string, unknown> = {}) {
  return {
    id,
    service_id: 7,
    kind: 'image',
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

const overview = {
  service,
  ref: 'main',
  refs: ['main', 'release/1.x'],
  artifacts: [
    scan(11),
    scan(12, {
      kind: 'secret-fs',
      artifact_name: '.',
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
    },
    {
      pkg_name: 'libxml2 dev',
      installed_version: '2.9.1',
      fix_version: '',
      findings: 3,
      by_severity: counts(0, 1, 2, 0, 0),
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
      { metric: 'new_bugs', operator: 'GT', threshold: '0', value: '0', status: 'OK' },
    ],
    reliability_rating: 'A',
    security_rating: 'C',
    maintainability_rating: null,
    security_review_rating: 'B',
    coverage: 71.4,
    duplicated_lines_density: null,
    measures: {},
    mode: 'mqr',
    gitlab_pipeline_id: 910,
    project_url: 'https://sonar.example.com/dashboard?id=payments',
    ...overrides,
  };
}

const sonar = {
  sonar_project_key: 'payments',
  latest: analysis(3),
  history: [analysis(3), analysis(2, { coverage: null }), analysis(1, { coverage: 60 })],
};

const trends = {
  ref: 'main',
  points: [
    {
      scan_id: 1,
      kind: 'image',
      scanned_at: '2026-11-10T10:00:00Z',
      gitlab_pipeline_id: 801,
      commit_sha: 'aaaaaaaabbbbbbbb',
      artifact_name: 'api',
      counts: counts(3, 10, 0, 0, 0),
      fixable: 5,
    },
    {
      scan_id: 2,
      kind: 'image',
      scanned_at: '2026-11-11T10:00:00Z',
      gitlab_pipeline_id: 802,
      commit_sha: 'ccccccccdddddddd',
      artifact_name: 'api',
      counts: counts(1, 9, 2, 0, 0),
      fixable: 4,
    },
  ],
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
    if (p === '/api/v1/services/7/scans?ref=main' || p === '/api/v1/services/7/scans')
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
        </Routes>
        <Probe />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return mock;
}

const href = (el: HTMLElement) => el.closest('a')?.getAttribute('href');
const lastChart = (label: RegExp) =>
  [...chartCalls].reverse().find((c) => label.test(c.ariaLabel) && c.status === 'ready');
const requested = (calls: RecordedCall[], prefix: string) =>
  calls.filter((c) => c.path.startsWith(prefix)).map((c) => c.path);

beforeEach(() => {
  chartCalls.length = 0;
});

describe('service dashboard', () => {
  it('counts secrets, not vulnerabilities, for a secret scan artifact', async () => {
    setup();
    const row = await screen.findByRole('link', { name: /Source tree/ });
    expect(row).toHaveTextContent('2 secrets');
    expect(row).not.toHaveTextContent('0 vulnerabilities');
  });

  it('shows API numbers and links every tile, package and artifact to its findings', async () => {
    setup();
    expect(await screen.findByRole('heading', { name: 'Payments API', level: 1 })).toBeVisible();
    expect(screen.getByText('platform/payments-api')).toBeInTheDocument();

    const base = '/services/7/findings?ref=main';
    const tile = (label: string) =>
      screen
        .getAllByText(label)
        .map((el) => el.closest('a'))
        .find((a) => a !== null) as HTMLElement;
    await screen.findByText('Findings by severity');
    expect(tile('Critical')).toHaveAttribute('href', `${base}&severity=CRITICAL`);
    expect(within(tile('Critical')).getByText('2')).toBeInTheDocument();
    expect(tile('High')).toHaveAttribute('href', `${base}&severity=HIGH`);
    expect(within(tile('High')).getByText('44')).toBeInTheDocument();
    expect(tile('Fixable')).toHaveAttribute('href', `${base}&fixable=true`);
    expect(within(tile('Fixable')).getByText('30')).toBeInTheDocument();
    expect(tile('Secrets')).toHaveAttribute('href', `${base}&tab=secrets`);
    expect(within(tile('Secrets')).getByText('4')).toBeInTheDocument();

    const newTile = screen.getByText('New since previous scan').parentElement?.parentElement;
    expect(newTile).toHaveTextContent('6');
    const fixedTile = screen.getByText('Fixed since previous scan').parentElement?.parentElement;
    expect(fixedTile).toHaveTextContent('9');

    expect(screen.getByRole('link', { name: 'Explore findings' })).toHaveAttribute('href', base);

    // Fix first
    const openssl = screen.getByText('openssl').closest('a') as HTMLElement;
    expect(openssl).toHaveAttribute('href', `${base}&q=openssl`);
    expect(openssl).toHaveTextContent('3.0.2');
    expect(openssl).toHaveTextContent('3.0.15');
    expect(openssl).toHaveTextContent('12 findings');
    const noFix = screen.getByText('libxml2 dev').closest('a') as HTMLElement;
    expect(noFix).toHaveAttribute('href', `${base}&q=libxml2+dev`);
    expect(noFix).toHaveTextContent('No fix available');

    // Artifacts
    const artifact = screen.getAllByText('registry.example.com/payments/api:1.4')[0];
    expect(href(artifact)).toBe('/scans/11');
    expect(screen.getAllByText('Source tree')[0].closest('a')).toHaveAttribute('href', '/scans/12');

    // Classes: os, language and other are separate rows
    expect(screen.getByText('OS packages')).toBeInTheDocument();
    expect(screen.getByText('Language packages')).toBeInTheDocument();
    expect(screen.getByText('Other')).toBeInTheDocument();
  });

  it('shows freshness with pipeline and scan links, and the GitLab link only when known', async () => {
    setup();
    expect(await screen.findByRole('link', { name: 'Pipeline #911' })).toHaveAttribute(
      'href',
      '/scans/11',
    );
    expect(screen.getByText(/Trivy scanned/)).toBeInTheDocument();
    expect(screen.getByText(/SonarQube analysed/)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Open project' })).toHaveAttribute(
      'href',
      'https://sonar.example.com/dashboard?id=payments',
    );
    expect(
      await screen.findByRole('link', { name: 'Open platform/payments-api in GitLab' }),
    ).toHaveAttribute('href', 'https://gitlab.example.com/platform/payments-api');
  });

  it('has no GitLab link without a scan detail URL', async () => {
    setup('/services/7', {
      overrides: (call) =>
        call.path === '/api/v1/scans/11'
          ? json(200, { ...scanDetail, pipeline_url: null })
          : undefined,
    });
    await screen.findByText('Findings by severity');
    await waitFor(() => expect(screen.getByRole('link', { name: 'Pipeline #911' })).toBeVisible());
    expect(screen.queryByRole('link', { name: /in GitLab/ })).not.toBeInTheDocument();
  });

  it('switches the ref, refetching with it and writing it to the URL', async () => {
    const { calls } = setup();
    const select = await screen.findByLabelText('Git ref');
    expect(select).toHaveValue('main');
    expect(requested(calls, '/api/v1/services/7/overview')).toEqual([
      '/api/v1/services/7/overview',
    ]);
    fireEvent.change(select, { target: { value: 'release/1.x' } });
    await waitFor(() =>
      expect(screen.getByTestId('location')).toHaveTextContent('/services/7?ref=release%2F1.x'),
    );
    await waitFor(() =>
      expect(requested(calls, '/api/v1/services/7/overview')).toContain(
        '/api/v1/services/7/overview?ref=release%2F1.x',
      ),
    );
    expect(requested(calls, '/api/v1/services/7/trends')).toContain(
      '/api/v1/services/7/trends?ref=release%2F1.x&days=90',
    );
    expect(requested(calls, '/api/v1/services/7/scans')).toContain(
      '/api/v1/services/7/scans?ref=release%2F1.x',
    );
  });

  it('reads ref and days from the URL', async () => {
    const { calls } = setup('/services/7?ref=release%2F1.x&days=30');
    await screen.findByText('Findings by severity');
    expect(requested(calls, '/api/v1/services/7/overview')).toEqual([
      '/api/v1/services/7/overview?ref=release%2F1.x',
    ]);
    expect(requested(calls, '/api/v1/services/7/trends')).toEqual([
      '/api/v1/services/7/trends?ref=release%2F1.x&days=30',
    ]);
    expect(screen.getByRole('radio', { name: '30 days' })).toBeChecked();
  });

  it('shows SonarQube posture with failed conditions, ratings and Not available', async () => {
    setup();
    expect(await screen.findByText('Failed')).toBeVisible();
    expect(screen.getByText('Coverage on new code')).toBeInTheDocument();
    expect(screen.getByText('62.5% · must be at least 80%')).toBeInTheDocument();
    expect(screen.queryByText('new_bugs')).not.toBeInTheDocument();
    const posture = screen.getByRole('region', { name: 'SonarQube posture' });
    expect(posture).toHaveTextContent(/Reliability\s*Rating A/);
    expect(posture).toHaveTextContent(/Security\s*Rating C/);
    expect(posture).toHaveTextContent(/Security review\s*Rating B/);
    expect(posture).toHaveTextContent(/MaintainabilityNot available/);
    // maintainability rating and duplication are null
    expect(within(posture).getAllByText('Not available')).toHaveLength(2);
    expect(within(posture).getByText('71.4%')).toBeInTheDocument();
    expect(within(posture).getByRole('link', { name: /Open in SonarQube/ })).toHaveAttribute(
      'href',
      'https://sonar.example.com/dashboard?id=payments',
    );
  });

  it('reports null coverage as Not available, not 0%', async () => {
    setup('/services/7', {
      overrides: (call) =>
        call.path.startsWith('/api/v1/services/7/sonar')
          ? json(200, { ...sonar, latest: analysis(3, { coverage: null }) })
          : undefined,
    });
    const posture = await screen.findByRole('region', { name: 'SonarQube posture' });
    await within(posture).findByText('Coverage');
    expect(within(posture).queryByText('0%')).not.toBeInTheDocument();
    expect(within(posture).getAllByText('Not available')).toHaveLength(3);
  });

  it('does not link an unsafe SonarQube URL', async () => {
    setup('/services/7', {
      overrides: (call) =>
        call.path.startsWith('/api/v1/services/7/sonar')
          ? json(200, {
              ...sonar,
              latest: analysis(3, { project_url: 'javascript:alert(1)' }),
            })
          : undefined,
    });
    await screen.findByText('Quality gate');
    expect(screen.queryByRole('link', { name: /Open in SonarQube/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'Open project' })).not.toBeInTheDocument();
  });

  it('explains how to link SonarQube, with the Services link only for admins', async () => {
    const noSonar = (call: RecordedCall) =>
      call.path.startsWith('/api/v1/services/7/sonar')
        ? json(200, { sonar_project_key: null, latest: null, history: [] })
        : undefined;
    setup('/services/7', { overrides: noSonar, user: { role: 'admin' } });
    expect(await screen.findByText('No SonarQube project linked')).toBeVisible();
    expect(await screen.findByRole('link', { name: 'Open the Services page' })).toHaveAttribute(
      'href',
      '/admin/services',
    );
    expect(screen.getByRole('link', { name: 'Read the SonarQube guide' })).toHaveAttribute(
      'href',
      'https://github.com/siyamsarker/scanopsis/blob/main/docs/integrations/sonarqube.md',
    );
  });

  it('shows no Services link to viewers', async () => {
    setup('/services/7', {
      user: { role: 'viewer' },
      overrides: (call) =>
        call.path.startsWith('/api/v1/services/7/sonar')
          ? json(200, { sonar_project_key: null, latest: null, history: [] })
          : undefined,
    });
    expect(await screen.findByText('No SonarQube project linked')).toBeVisible();
    await waitFor(() =>
      expect(screen.getByRole('link', { name: 'Read the SonarQube guide' })).toBeVisible(),
    );
    expect(screen.queryByRole('link', { name: 'Open the Services page' })).not.toBeInTheDocument();
  });

  it('shows the no-scans state', async () => {
    setup('/services/7', {
      overrides: (call) => {
        if (call.path.startsWith('/api/v1/services/7/overview'))
          return json(200, {
            ...overview,
            ref: null,
            refs: [],
            artifacts: [],
            data_at: null,
            top_packages: [],
          });
        if (call.path.startsWith('/api/v1/services/7/scans'))
          return json(200, { items: [], next_before: null });
        if (call.path.startsWith('/api/v1/services/7/trends'))
          return json(200, { ref: null, points: [] });
        return undefined;
      },
    });
    expect(await screen.findByText('No Trivy scans yet')).toBeVisible();
    expect(await screen.findByText('No scans yet')).toBeVisible();
    expect(screen.queryByLabelText('Git ref')).not.toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getAllByTestId('chart').some((c) => c.dataset.status === 'empty')).toBe(true),
    );
  });

  it('leaves secret scans out of the trends chart and says so', async () => {
    // The server marks each trend point with its scan kind.
    setup('/services/7', {
      overrides: (call) =>
        call.path.startsWith('/api/v1/services/7/trends')
          ? json(200, {
              ...trends,
              points: [trends.points[0], { ...trends.points[1], kind: 'secret-fs' }],
            })
          : undefined,
    });
    expect(
      await screen.findByText(/One bar per image scan.*Secret scans have no vulnerability counts/),
    ).toBeVisible();
    await waitFor(() => {
      const chart = lastChart(/Findings per scan/) as ChartProps;
      const option = (
        typeof chart.option === 'function' ? chart.option(palette) : chart.option
      ) as { series: { data: number[] }[]; xAxis: { data: string[] } };
      expect(option.xAxis.data).toEqual(['10 Nov #801']);
      expect(option.series[0].data).toEqual([3]);
    });
  });

  it('builds the trends chart: stacked critical first, one bar per scan, days param', async () => {
    const { calls } = setup();
    await waitFor(() => expect(lastChart(/Findings per scan/)).toBeDefined());
    const chart = lastChart(/Findings per scan/) as ChartProps;
    expect(chart.ariaLabel).toBe('Findings per scan, last 90 days');
    expect(requested(calls, '/api/v1/services/7/trends')).toEqual([
      '/api/v1/services/7/trends?days=90',
    ]);
    const option = (typeof chart.option === 'function' ? chart.option(palette) : chart.option) as {
      series: { name: string; stack: string; data: number[] }[];
      xAxis: { data: string[] };
    };
    expect(option.series.map((s) => s.name)).toEqual([
      'Critical',
      'High',
      'Medium',
      'Low',
      'Unknown',
    ]);
    expect(new Set(option.series.map((s) => s.stack)).size).toBe(1);
    expect(option.series[0].data).toEqual([3, 1]);
    expect(option.series[1].data).toEqual([10, 9]);
    expect(option.xAxis.data).toEqual(['10 Nov #801', '11 Nov #802']);

    fireEvent.click(screen.getByRole('radio', { name: '180 days' }));
    await waitFor(() =>
      expect(requested(calls, '/api/v1/services/7/trends')).toContain(
        '/api/v1/services/7/trends?days=180',
      ),
    );
    expect(screen.getByTestId('location')).toHaveTextContent('/services/7?days=180');
  });

  it('leaves analyses without coverage as gaps in the coverage chart', async () => {
    setup();
    await waitFor(() => expect(lastChart(/Coverage per SonarQube analysis/)).toBeDefined());
    const chart = lastChart(/Coverage per SonarQube analysis/) as ChartProps;
    const option = (typeof chart.option === 'function' ? chart.option(palette) : chart.option) as {
      series: { data: (number | null)[] }[];
      xAxis: { data: string[] };
    };
    // history is newest first; the chart runs oldest to newest
    expect(option.series[0].data).toEqual([60, null, 71.4]);
    expect(option.xAxis.data).toEqual(['11 Nov', '12 Nov', '13 Nov']);
  });

  it('lists recent scans and loads more with next_before', async () => {
    const { calls } = setup('/services/7', {
      overrides: (call) => {
        if (call.path === '/api/v1/services/7/scans')
          return json(200, { items: [scan(11)], next_before: 11 });
        if (call.path === '/api/v1/services/7/scans?before=11')
          return json(200, {
            items: [scan(10, { artifact_name: 'older-image' })],
            next_before: null,
          });
        return undefined;
      },
    });
    const table = await screen.findByRole('table', { name: 'Recent scans' });
    expect(within(table).getByText('#911')).toBeInTheDocument();
    expect(within(table).getByText('01234567')).toBeInTheDocument();
    expect(within(table).getByRole('link', { name: 'Open scan 11' })).toHaveAttribute(
      'href',
      '/scans/11',
    );
    fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
    expect(await within(table).findByText('older-image')).toBeVisible();
    expect(requested(calls, '/api/v1/services/7/scans')).toContain(
      '/api/v1/services/7/scans?before=11',
    );
    expect(screen.queryByRole('button', { name: 'Load more' })).not.toBeInTheDocument();
  });

  it('shows a not found state with a link to the portfolio', async () => {
    setup('/services/7', {
      overrides: (call) =>
        call.path === '/api/v1/services/7'
          ? apiError(404, 'not_found', 'No such service.')
          : undefined,
    });
    expect(await screen.findByText('Service not found')).toBeVisible();
    expect(screen.getByRole('link', { name: 'Back to the portfolio' })).toHaveAttribute(
      'href',
      '/',
    );
    expect(screen.queryByText('Trivy posture')).not.toBeInTheDocument();
  });

  it('keeps the other sections when one endpoint fails, and retries just that one', async () => {
    let failing = true;
    const { calls } = setup('/services/7', {
      overrides: (call) =>
        failing && call.path.startsWith('/api/v1/services/7/sonar')
          ? apiError(500, 'internal', 'Boom.')
          : undefined,
    });
    expect(await screen.findByText('SonarQube posture could not be loaded')).toBeVisible();
    // the Trivy section and the scans table are intact
    expect(await screen.findByText('Findings by severity')).toBeVisible();
    expect(await screen.findByRole('table', { name: 'Recent scans' })).toBeVisible();
    const before = requested(calls, '/api/v1/services/7/overview').length;
    failing = false;
    fireEvent.click(
      within(screen.getByRole('region', { name: 'SonarQube posture' })).getByRole('button', {
        name: 'Try again',
      }),
    );
    expect(await screen.findByText('Quality gate')).toBeVisible();
    expect(requested(calls, '/api/v1/services/7/overview')).toHaveLength(before);
  });

  it('shows a Trivy error on its own when the overview fails', async () => {
    setup('/services/7', {
      overrides: (call) =>
        call.path.startsWith('/api/v1/services/7/overview')
          ? apiError(500, 'internal', 'Boom.')
          : undefined,
    });
    expect(await screen.findByText('Trivy posture could not be loaded')).toBeVisible();
    expect(await screen.findByText('Quality gate')).toBeVisible();
    expect(screen.getByRole('heading', { name: 'Payments API', level: 1 })).toBeVisible();
  });
});
