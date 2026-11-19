// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router';
import { createQueryClient } from '../../api/queryClient';
import type { Portfolio, PortfolioItem } from '../../api/portfolio';
import { readPalette } from '../../charts/palette';
import type { ChartProps } from '../../charts/Chart';
import { apiError, json, mockFetch } from '../../test-utils';
import { PortfolioPage } from '../PortfolioPage';
import { readParams, visibleItems, writeParams } from './model';

const chartProps = vi.fn();
vi.mock('../../charts/Chart', () => ({
  Chart: (props: ChartProps) => {
    chartProps(props);
    return (
      <button
        type="button"
        onClick={() => props.onItemClick?.({ dataIndex: 0, name: 'x', data: { id: '2' } })}
      >
        Mock treemap
      </button>
    );
  },
}));

const NOW = new Date('2026-11-19T12:00:00Z');
const daysAgo = (d: number) => new Date(NOW.getTime() - d * 86_400_000).toISOString();

function item(
  id: number,
  name: string,
  overrides: Partial<PortfolioItem> = {},
  counts: Partial<PortfolioItem['counts']> = {},
): PortfolioItem {
  return {
    service: {
      id,
      name,
      gitlab_project_id: id,
      gitlab_project_path: `platform/${name}`,
      sonar_project_key: null,
      created_via: 'ingest',
      archived: false,
      created_at: '2026-01-01T00:00:00Z',
    },
    ref: 'main',
    counts: { critical: 0, high: 0, medium: 0, low: 0, unknown: 0, ...counts },
    fixable: 0,
    secrets: 0,
    last_scan_at: daysAgo(1),
    quality_gate_status: 'OK',
    reliability_rating: 'A',
    security_rating: 'B',
    maintainability_rating: 'C',
    coverage: 84.2,
    last_analysis_at: daysAgo(2),
    ...overrides,
  };
}

const alpha = item(
  1,
  'alpha',
  { fixable: 4, quality_gate_status: 'ERROR' },
  { critical: 1, high: 2 },
);
const bravo = item(2, 'bravo', { fixable: 0, last_scan_at: daysAgo(10) }, { critical: 3 });
const charlie = item(3, 'charlie', { fixable: 9 }, { high: 5, medium: 1 });
const delta = item(
  4,
  'delta',
  {
    last_scan_at: null,
    quality_gate_status: null,
    reliability_rating: null,
    security_rating: null,
    maintainability_rating: null,
    coverage: null,
    last_analysis_at: null,
  },
  {},
);
const echo = item(5, 'echo', { last_scan_at: daysAgo(3) }, { high: 5, medium: 1 });

const portfolio: Portfolio = {
  items: [alpha, bravo, charlie, delta, echo],
  totals: { services: 5, failing_quality_gates: 1, services_with_critical: 2, fixable: 13 },
  generated_at: daysAgo(0),
};

function Probe() {
  const location = useLocation();
  return <output data-testid="location">{location.pathname + location.search}</output>;
}

function renderPage(path = '/', body: Portfolio | Response = portfolio) {
  const mock = mockFetch((call) =>
    call.path === '/api/v1/portfolio'
      ? body instanceof Response
        ? body
        : json(200, body)
      : undefined,
  );
  render(
    <QueryClientProvider client={createQueryClient()}>
      <MemoryRouter initialEntries={[path]}>
        <PortfolioPage now={NOW} />
        <Probe />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return mock;
}

const names = () =>
  screen.getAllByRole('heading', { level: 3 }).map((heading) => heading.textContent);

beforeEach(() => chartProps.mockClear());

describe('portfolio model', () => {
  it('sorts by risk, name and freshness', () => {
    const params = { q: '', filter: 'all', view: 'grid' } as const;
    const order = (sort: 'risk' | 'name' | 'freshness') =>
      visibleItems(portfolio.items, { ...params, sort }, NOW).map((i) => i.service.name);
    // Critical first (3 before 1), then high 5 (charlie, echo by name), then none.
    expect(order('risk')).toEqual(['bravo', 'alpha', 'charlie', 'echo', 'delta']);
    expect(order('name')).toEqual(['alpha', 'bravo', 'charlie', 'delta', 'echo']);
    // Newest scan first, never-scanned last.
    expect(order('freshness')).toEqual(['alpha', 'charlie', 'echo', 'bravo', 'delta']);
  });

  it('filters stale by a fixed clock, counting unscanned services', () => {
    const stale = visibleItems(
      portfolio.items,
      { q: '', filter: 'stale', sort: 'name', view: 'grid' },
      NOW,
    );
    expect(stale.map((i) => i.service.name)).toEqual(['bravo', 'delta']);
  });

  it('round-trips the URL state and drops defaults', () => {
    const params = { q: 'pay', filter: 'critical', sort: 'name', view: 'treemap' } as const;
    const search = writeParams(params);
    expect(search.toString()).toBe('q=pay&filter=critical&sort=name&view=treemap');
    expect(readParams(search)).toEqual(params);
    expect(writeParams(readParams(new URLSearchParams('filter=bogus&sort=x'))).toString()).toBe('');
  });
});

describe('PortfolioPage', () => {
  it('shows the API totals exactly and links each tile to its filtered grid', async () => {
    const { calls } = renderPage();
    await screen.findByText('bravo');
    expect(calls.map((c) => c.path)).toEqual(['/api/v1/portfolio']);
    const summary = within(screen.getByRole('region', { name: 'Summary' }));
    const tile = (label: string) => summary.getByRole('link', { name: new RegExp(label) });
    expect(tile('Services ?\\d')).toHaveTextContent('5');
    expect(tile('Services ?\\d')).toHaveAttribute('href', '/');
    expect(tile('Failing quality gates')).toHaveTextContent('1');
    expect(tile('Failing quality gates')).toHaveAttribute('href', '/?filter=gate-failed');
    expect(tile('Services with critical')).toHaveTextContent('2');
    expect(tile('Services with critical')).toHaveAttribute('href', '/?filter=critical');
    expect(tile('Fixable vulnerabilities')).toHaveTextContent('13');
    expect(tile('Fixable vulnerabilities')).toHaveAttribute('href', '/?filter=fixable');
    expect(screen.getByText(/Updated/)).toBeInTheDocument();
  });

  it('orders the grid by risk and re-sorts through the URL', async () => {
    renderPage();
    await screen.findByText('bravo');
    expect(names()).toEqual(['bravo', 'alpha', 'charlie', 'echo', 'delta']);
    fireEvent.change(screen.getByLabelText('Sort by'), { target: { value: 'name' } });
    expect(names()).toEqual(['alpha', 'bravo', 'charlie', 'delta', 'echo']);
    expect(screen.getByTestId('location')).toHaveTextContent('/?sort=name');
  });

  it('honours the filter in the URL and updates it from the chips', async () => {
    renderPage('/?filter=gate-failed');
    await screen.findByText('alpha');
    expect(names()).toEqual(['alpha']);
    expect(screen.getByRole('button', { name: /Failing gate/ })).toHaveAttribute(
      'aria-pressed',
      'true',
    );
    fireEvent.click(screen.getByRole('button', { name: /Stale/ }));
    expect(screen.getByTestId('location')).toHaveTextContent('/?filter=stale');
    expect(names()).toEqual(['bravo', 'delta']);
    fireEvent.click(screen.getByRole('button', { name: /Fixable/ }));
    expect(names()).toEqual(['alpha', 'charlie']);
  });

  it('searches by name or path', async () => {
    renderPage('/?q=platform/ech');
    await screen.findByText('echo');
    expect(names()).toEqual(['echo']);
    fireEvent.change(screen.getByLabelText('Search services'), { target: { value: 'zzz' } });
    fireEvent.keyDown(screen.getByLabelText('Search services'), { key: 'Enter' });
    expect(await screen.findByText('No services match')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Clear search and filter' }));
    await waitFor(() => expect(names()).toHaveLength(5));
    expect(screen.getByTestId('location')).toHaveTextContent(/^\/$/);
  });

  it('links the name, the severity segments and shows ratings', async () => {
    renderPage();
    await screen.findByText('alpha');
    const tile = within(screen.getByText('alpha').closest('li')!);
    expect(tile.getByRole('link', { name: 'alpha' })).toHaveAttribute('href', '/services/1');
    expect(tile.getByText('platform/alpha')).toBeInTheDocument();
    expect(tile.getByRole('link', { name: '1 critical' })).toHaveAttribute(
      'href',
      '/services/1/findings?severity=critical',
    );
    expect(tile.getByRole('link', { name: '2 high' })).toHaveAttribute(
      'href',
      '/services/1/findings?severity=high',
    );
    expect(tile.getByText('4 fixable')).toBeInTheDocument();
    expect(tile.getByText('Failed')).toBeInTheDocument();
    expect(tile.getByText('84.2%')).toBeInTheDocument();
    expect(tile.getByText((_, el) => el?.textContent === 'Rating A')).toBeInTheDocument();
    // A scan older than seven days is marked stale.
    expect(within(screen.getByText('bravo').closest('li')!).getByText('Stale')).toBeInTheDocument();
    expect(tile.queryByText('Stale')).not.toBeInTheDocument();
  });

  it('shows Not available for unreported metrics and a docs link without scans', async () => {
    renderPage();
    await screen.findByText('delta');
    const tile = within(screen.getByText('delta').closest('li')!);
    // One calm line replaces the rating chips, coverage and last analysis.
    expect(tile.getByText('No SonarQube analysis yet')).toBeInTheDocument();
    expect(tile.queryByText('Not available')).not.toBeInTheDocument();
    expect(tile.queryByText('Coverage')).not.toBeInTheDocument();
    expect(tile.queryByText(/Last analysis/)).not.toBeInTheDocument();
    expect(tile.getByText(/No scans yet/)).toBeInTheDocument();
    const link = tile.getByRole('link', { name: 'Add the upload step' });
    expect(link).toHaveAttribute(
      'href',
      'https://github.com/siyamsarker/scanopsis/blob/main/docs/integrations/gitlab-ci.md',
    );
    expect(link).toHaveAttribute('rel', 'noopener noreferrer');
  });

  it('builds the treemap from the visible items and opens a service on click', async () => {
    renderPage('/?view=treemap&filter=critical');
    await screen.findByText('Mock treemap');
    expect(screen.getByText('Treemap tiles are also listed in the grid view.')).toBeVisible();
    const props = chartProps.mock.calls.at(-1)![0] as ChartProps;
    const option = (props.option as (p: ReturnType<typeof readPalette>) => unknown)(
      readPalette(),
    ) as {
      series: { data: { id: string; name: string; value: number; severity: string }[] }[];
    };
    const data = option.series[0].data;
    expect(data.map((d) => [d.id, d.name, d.value, d.severity])).toEqual([
      ['2', 'bravo', 3, 'critical'],
      ['1', 'alpha', 3, 'critical'],
    ]);
    fireEvent.click(screen.getByText('Mock treemap'));
    expect(screen.getByTestId('location')).toHaveTextContent('/services/2');
  });

  it('keeps services without vulnerabilities in the treemap as small tiles', async () => {
    renderPage('/?view=treemap&filter=stale');
    await screen.findByText('Mock treemap');
    const props = chartProps.mock.calls.at(-1)![0] as ChartProps;
    const option = (props.option as (p: ReturnType<typeof readPalette>) => unknown)(
      readPalette(),
    ) as {
      series: { data: { name: string; value: number; severity: string }[] }[];
    };
    const delta = option.series[0].data.find((d) => d.name === 'delta')!;
    expect(delta.value).toBeGreaterThan(0);
    expect(delta.value).toBeLessThan(1);
    expect(delta.severity).toBe('none');
  });

  it('shows connect steps for an empty portfolio', async () => {
    renderPage('/', {
      items: [],
      totals: { services: 0, failing_quality_gates: 0, services_with_critical: 0, fixable: 0 },
      generated_at: daysAgo(0),
    });
    expect(await screen.findByText('No services yet')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'GitLab settings' })).toHaveAttribute(
      'href',
      '/settings',
    );
    expect(screen.getByRole('link', { name: 'Read the GitLab CI guide' })).toHaveAttribute(
      'href',
      'https://github.com/siyamsarker/scanopsis/blob/main/docs/integrations/gitlab-ci.md',
    );
    expect(screen.getByRole('link', { name: 'Read the SonarQube guide' })).toHaveAttribute(
      'href',
      'https://github.com/siyamsarker/scanopsis/blob/main/docs/integrations/sonarqube.md',
    );
  });

  it('shows skeletons while loading', () => {
    mockFetch(() => new Promise<Response>(() => {}));
    render(
      <QueryClientProvider client={createQueryClient()}>
        <MemoryRouter>
          <PortfolioPage now={NOW} />
        </MemoryRouter>
      </QueryClientProvider>,
    );
    expect(screen.getByRole('status', { name: 'Loading services' })).toBeInTheDocument();
    expect(screen.getAllByRole('status', { name: 'Loading' })).toHaveLength(4);
  });

  it('shows an error with retry', async () => {
    let fail = true;
    const { calls } = mockFetch((call) =>
      call.path === '/api/v1/portfolio'
        ? fail
          ? apiError(500, 'internal', 'boom')
          : json(200, portfolio)
        : undefined,
    );
    render(
      <QueryClientProvider client={createQueryClient()}>
        <MemoryRouter>
          <PortfolioPage now={NOW} />
        </MemoryRouter>
      </QueryClientProvider>,
    );
    expect(await screen.findByText('The portfolio could not be loaded')).toBeInTheDocument();
    fail = false;
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByText('alpha')).toBeInTheDocument();
    expect(calls.length).toBeGreaterThanOrEqual(2);
  });
});
