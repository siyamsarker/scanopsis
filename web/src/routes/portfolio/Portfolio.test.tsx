// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router';
import { createQueryClient } from '../../api/queryClient';
import type { Portfolio, PortfolioHistory, PortfolioItem } from '../../api/portfolio';
import { apiError, chooseOption, json, makeUser, mockFetch } from '../../test-utils';
import { PortfolioPage } from '../PortfolioPage';
import {
  biggestMover,
  effectiveView,
  gateOf,
  historyFor,
  indexHistory,
  LIST_VIEW_FROM,
  portfolioRisk,
  readParams,
  rememberedView,
  rememberView,
  riskChange,
  sparkPoints,
  visibleItems,
  writeParams,
} from './model';

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
      default_ref: null,
    },
    ref: 'main',
    ref_source: 'latest',
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

// Risk points: alpha 10 + 2×5 = 20, bravo 3×10 = 30, charlie and echo 5×5 + 3 = 28.
const alpha = item(
  1,
  'alpha',
  { fixable: 4, quality_gate_status: 'ERROR' },
  { critical: 1, high: 2 },
);
const bravo = item(2, 'bravo', { fixable: 0, last_scan_at: daysAgo(10) }, { critical: 3 });
const charlie = item(3, 'charlie', { fixable: 9 }, { high: 5, medium: 1 });
const delta = item(4, 'delta', {
  ref: null,
  last_scan_at: null,
  quality_gate_status: null,
  reliability_rating: null,
  security_rating: null,
  maintainability_rating: null,
  coverage: null,
  last_analysis_at: null,
});
const echo = item(5, 'echo', { last_scan_at: daysAgo(3) }, { high: 5, medium: 1 });

const portfolio: Portfolio = {
  items: [alpha, bravo, charlie, delta, echo],
  totals: { services: 5, failing_quality_gates: 1, services_with_critical: 2, fixable: 13 },
  generated_at: daysAgo(0),
};

const zero = { critical: 0, high: 0, medium: 0, low: 0, unknown: 0 };
const history: PortfolioHistory = {
  services: [
    {
      service_id: 1,
      ref: 'main',
      points: [
        { pipeline_id: 10, at: daysAgo(3), counts: { ...zero, high: 4 } },
        { pipeline_id: 11, at: daysAgo(2), counts: { ...zero, high: 2 } },
        { pipeline_id: 12, at: daysAgo(1), counts: { ...zero, critical: 1, high: 2 } },
      ],
    },
    {
      service_id: 3,
      ref: 'main',
      points: [
        { pipeline_id: 30, at: daysAgo(2), counts: { ...zero, high: 6, medium: 1 } },
        { pipeline_id: 31, at: daysAgo(1), counts: { ...zero, high: 5, medium: 1 } },
      ],
    },
  ],
  // A week ago the portfolio had 50 risk points.
  days: Array.from({ length: 7 }, (_, i) => ({
    at: daysAgo(7 - i),
    counts: { ...zero, critical: i === 0 ? 5 : 6 },
  })),
  generated_at: daysAgo(0),
};

function Probe() {
  const location = useLocation();
  return <output data-testid="location">{location.pathname + location.search}</output>;
}

function renderPage(
  path = '/',
  body: Portfolio | Response = portfolio,
  extra: (path: string) => Response | undefined = () => undefined,
) {
  const mock = mockFetch((call) => {
    if (call.path === '/api/v1/portfolio') return body instanceof Response ? body : json(200, body);
    if (call.path === '/api/v1/portfolio/history') return json(200, history);
    return extra(call.path);
  });
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
  within(screen.getByRole('list', { name: 'Services' }))
    .getAllByRole('heading', { level: 3 })
    .map((heading) => heading.textContent);

const card = (name: string) => within(screen.getByRole('link', { name }).closest('li')!);

beforeEach(() => {
  window.localStorage.clear();
});

describe('portfolio model', () => {
  it('ranks by risk points, then name; sorts by name and freshness', () => {
    const params = { q: '', filter: 'all' } as const;
    const order = (sort: 'risk' | 'name' | 'freshness') =>
      visibleItems(portfolio.items, { ...params, sort }, NOW).map((i) => i.service.name);
    expect(order('risk')).toEqual(['bravo', 'charlie', 'echo', 'alpha', 'delta']);
    expect(order('name')).toEqual(['alpha', 'bravo', 'charlie', 'delta', 'echo']);
    expect(order('freshness')).toEqual(['alpha', 'charlie', 'echo', 'bravo', 'delta']);
  });

  it('filters stale by a fixed clock, counting unscanned services', () => {
    const stale = visibleItems(portfolio.items, { q: '', filter: 'stale', sort: 'name' }, NOW);
    expect(stale.map((i) => i.service.name)).toEqual(['bravo', 'delta']);
  });

  it('round-trips the URL state and drops defaults', () => {
    const params = { q: 'pay', filter: 'critical', sort: 'name', view: 'list' } as const;
    const search = writeParams(params);
    expect(search.toString()).toBe('q=pay&filter=critical&sort=name&view=list');
    expect(readParams(search)).toEqual(params);
    expect(
      writeParams(readParams(new URLSearchParams('filter=bogus&sort=x&view=y'))).toString(),
    ).toBe('');
  });

  it('picks the view from the address, then memory, then the number of services', () => {
    expect(effectiveView('cards', 'list', 500)).toBe('cards');
    expect(effectiveView(undefined, 'cards', 500)).toBe('cards');
    expect(effectiveView(undefined, undefined, LIST_VIEW_FROM - 1)).toBe('cards');
    expect(effectiveView(undefined, undefined, LIST_VIEW_FROM)).toBe('list');
  });

  it('remembers the view and survives blocked storage', () => {
    rememberView('list');
    expect(rememberedView()).toBe('list');
    const spy = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    expect(rememberedView()).toBeUndefined();
    spy.mockRestore();
    const set = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    expect(() => rememberView('cards')).not.toThrow();
    set.mockRestore();
  });

  it('maps quality gates for the iris', () => {
    expect(gateOf(alpha)).toBe('fail');
    expect(gateOf(charlie)).toBe('pass');
    expect(gateOf(delta)).toBeUndefined();
    expect(gateOf({ quality_gate_status: 'NONE', last_analysis_at: daysAgo(1) })).toBe('none');
  });

  it('turns a history into net changes per pipeline and a risk change', () => {
    const points = history.services[0].points;
    expect(sparkPoints(points, 'main')).toEqual([
      { pipelineId: 11, at: points[1].at, ref: 'main', newBySeverity: {}, fixedCount: 2 },
      {
        pipelineId: 12,
        at: points[2].at,
        ref: 'main',
        newBySeverity: { critical: 1 },
        fixedCount: 0,
      },
    ]);
    expect(riskChange(points)).toEqual({ delta: 10, sincePipeline: 11 });
    expect(riskChange(points.slice(0, 1))).toBeUndefined();
  });

  it('uses a history only when it ends in the counts the card shows', () => {
    const index = indexHistory(history);
    expect(historyFor(alpha, index)).toHaveLength(3);
    expect(historyFor({ ...alpha, ref: 'dev' }, index)).toBeUndefined();
    expect(historyFor({ ...alpha, counts: { ...alpha.counts, low: 1 } }, index)).toBeUndefined();
    expect(historyFor(bravo, index)).toBeUndefined();
  });

  it('sums the portfolio risk with its week and finds the biggest mover', () => {
    const risk = portfolioRisk(portfolio.items, history);
    expect(risk.now).toBe(20 + 30 + 28 + 28);
    expect(risk.series).toHaveLength(8);
    expect(risk.weekDelta).toBe(106 - 50);
    expect(portfolioRisk(portfolio.items).series).toEqual([]);
    const mover = biggestMover(portfolio.items, indexHistory(history));
    expect(mover?.item.service.name).toBe('alpha');
    expect(mover?.change.delta).toBe(10);
  });
});

describe('PortfolioPage', () => {
  it('sums the portfolio and lists what needs attention, each a filter', async () => {
    renderPage();
    const summary = within(await screen.findByRole('region', { name: 'Summary' }));
    expect(summary.getByTestId('portfolio-risk')).toHaveTextContent('106');
    expect(summary.getByText('of 4 analysed services fails its quality gate')).toBeInTheDocument();
    expect(
      await summary.findByText('up 56 risk points since 7 days ago, worse'),
    ).toBeInTheDocument();
    expect(summary.getByText(/open findings in 5 services/)).toHaveTextContent(
      '18 open findings in 5 services',
    );
    const critical = summary.getByRole('button', { name: /services have critical findings/ });
    expect(critical).toHaveTextContent('2');
    fireEvent.click(critical);
    expect(screen.getByTestId('location')).toHaveTextContent('/?filter=critical');
    expect(critical).toHaveAttribute('aria-pressed', 'true');
    expect(names()).toEqual(['bravo', 'alpha']);
    fireEvent.click(critical);
    expect(screen.getByTestId('location')).toHaveTextContent(/^\/$/);
    const fixable = summary.getByRole('button', { name: /findings have a fixed version/ });
    expect(fixable).toHaveTextContent('13');
    expect(summary.getByText('alpha')).toHaveAttribute('href', '/services/1');
  });

  it('orders the cards by risk and re-sorts through the URL', async () => {
    renderPage();
    await screen.findByText('bravo');
    expect(names()).toEqual(['bravo', 'charlie', 'echo', 'alpha', 'delta']);
    await chooseOption('Sort by', 'Sort by name');
    expect(names()).toEqual(['alpha', 'bravo', 'charlie', 'delta', 'echo']);
    expect(screen.getByTestId('location')).toHaveTextContent('/?sort=name');
  });

  it('honours the filter in the URL, updates it from the chips and counts', async () => {
    renderPage('/?filter=gate-failed');
    await screen.findByRole('link', { name: 'alpha' });
    expect(names()).toEqual(['alpha']);
    expect(screen.getByText(/Showing/)).toHaveTextContent('Showing 1 of 5 services');
    fireEvent.click(screen.getByRole('button', { name: /^Stale/ }));
    expect(screen.getByTestId('location')).toHaveTextContent('/?filter=stale');
    await waitFor(() => expect(names()).toEqual(['bravo', 'delta']));
    fireEvent.click(screen.getByRole('button', { name: /^Fixable/ }));
    await waitFor(() => expect(names()).toEqual(['charlie', 'alpha']));
  });

  it('searches by name or path', async () => {
    renderPage('/?q=platform/ech');
    await screen.findByRole('link', { name: 'echo' });
    expect(names()).toEqual(['echo']);
    fireEvent.change(screen.getByLabelText('Search services'), { target: { value: 'zzz' } });
    fireEvent.keyDown(screen.getByLabelText('Search services'), { key: 'Enter' });
    expect(await screen.findByText('No services match')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Clear search and filter' }));
    await waitFor(() => expect(names()).toHaveLength(5));
    expect(screen.getByTestId('location')).toHaveTextContent(/^\/$/);
  });

  it('shows the iris, counts, gate, coverage, freshness and pipeline change on a card', async () => {
    renderPage();
    await screen.findByRole('link', { name: 'alpha' });
    const a = card('alpha');
    expect(a.getByRole('link', { name: 'alpha' })).toHaveAttribute('href', '/services/1');
    expect(a.getByText('platform/alpha')).toBeInTheDocument();
    expect(
      a.getByRole('img', {
        name: 'alpha: Risk 20: 1 critical × 10 = 10, 2 high × 5 = 10. Gate failed.',
      }),
    ).toBeInTheDocument();
    expect(a.getByText('critical,')).toHaveClass('sr-only');
    expect(a.getByText('Gate failed')).toBeInTheDocument();
    expect(a.getByText('84.2%')).toBeInTheDocument();
    expect(a.getByText(/Scanned/)).toHaveTextContent('Scanned 1 day ago');
    expect(await a.findByText('up 10 risk points since pipeline #11, worse')).toBeInTheDocument();
    expect(a.getByRole('img', { name: /last 2 pipelines/ })).toBeInTheDocument();
    // A scan older than seven days is marked stale.
    expect(card('bravo').getByText(/Stale, scanned/)).toBeInTheDocument();
    // One pipeline only: no spark yet.
    expect(
      await card('echo').findByText('The spark fills in from the second pipeline.'),
    ).toBeInTheDocument();
  });

  it('names the branch the numbers come from', async () => {
    renderPage('/', {
      ...portfolio,
      items: [
        item(1, 'alpha', { ref: 'main', ref_source: 'default' }),
        item(2, 'bravo', { ref: 'feature/x', ref_source: 'latest' }),
        item(3, 'charlie', { ref: 'release', ref_source: 'default', last_scan_at: null }),
      ],
    });
    await screen.findByRole('link', { name: 'alpha' });
    expect(card('alpha').getByText('main').closest('p')).toHaveAttribute(
      'title',
      'The dashboard branch an administrator chose',
    );
    expect(card('bravo').getByText('feature/x').closest('p')).toHaveAttribute(
      'title',
      'The latest scanned branch; no dashboard branch is chosen',
    );
    expect(card('charlie').getByText(/No scans of release yet/)).toBeInTheDocument();
  });

  it('says when a service has no scan or no analysis', async () => {
    renderPage();
    await screen.findByRole('link', { name: 'delta' });
    const d = card('delta');
    expect(d.getByText('No SonarQube analysis yet')).toBeInTheDocument();
    expect(d.queryByText('Coverage')).not.toBeInTheDocument();
    expect(d.getByRole('img', { name: 'delta: no scan yet' })).toBeInTheDocument();
    const link = d.getByRole('link', { name: 'Add the upload step' });
    expect(link).toHaveAttribute(
      'href',
      'https://github.com/siyamsarker/scanopsis/blob/main/docs/integrations/gitlab-ci.md',
    );
    expect(link).toHaveAttribute('rel', 'noopener noreferrer');
  });

  it('switches to the ranked list, remembers it and opens a service from a row', async () => {
    renderPage();
    await screen.findByRole('link', { name: 'alpha' });
    fireEvent.click(screen.getByRole('radio', { name: 'List' }));
    expect(screen.getByTestId('location')).toHaveTextContent('/?view=list');
    expect(window.localStorage.getItem('scanopsis.portfolio.view')).toBe('list');
    const table = screen.getByRole('table', { name: /Services ranked by risk/ });
    const rows = within(table).getAllByRole('row').slice(1);
    expect(rows.map((r) => within(r).getByRole('rowheader').textContent)).toEqual([
      'bravoplatform/bravo',
      'charlieplatform/charlie',
      'echoplatform/echo',
      'alphaplatform/alpha',
      'deltaplatform/delta',
    ]);
    fireEvent.click(rows[0]);
    expect(screen.getByTestId('location')).toHaveTextContent('/services/2');
  });

  it('keeps the chosen sort in the list', async () => {
    window.localStorage.setItem('scanopsis.portfolio.view', 'list');
    renderPage('/?sort=name');
    const table = await screen.findByRole('table', { name: /Services sorted by name/ });
    const first = within(table).getAllByRole('row')[1];
    expect(within(first).getByRole('rowheader')).toHaveTextContent('alpha');
  });

  it('shows the setup steps on a fresh install, with progress for administrators', async () => {
    renderPage(
      '/',
      {
        items: [],
        totals: { services: 0, failing_quality_gates: 0, services_with_critical: 0, fixable: 0 },
        generated_at: daysAgo(0),
      },
      (path) => {
        if (path === '/api/v1/me') return json(200, makeUser({ role: 'admin' }));
        if (path === '/api/v1/settings')
          return json(200, {
            items: [{ key: 'gitlab.url', value: 'https://gitlab.example.com' }],
          });
        if (path === '/api/v1/admin/integrations/sonarqube')
          return json(200, { configured: false, recent_deliveries: [] });
        return undefined;
      },
    );
    expect(await screen.findByRole('heading', { name: 'No services yet' })).toBeInTheDocument();
    expect(await screen.findByText('1 of 3 done')).toBeInTheDocument();
    expect(screen.getByText(/Step 1, done/)).toBeInTheDocument();
    expect(screen.getByText(/Step 2, to do/)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Settings' })).toHaveAttribute('href', '/settings');
    expect(screen.getByRole('link', { name: 'Read the GitLab CI guide' })).toHaveAttribute(
      'href',
      'https://github.com/siyamsarker/scanopsis/blob/main/docs/integrations/gitlab-ci.md',
    );
    expect(screen.getByRole('link', { name: 'Open Integrations' })).toHaveAttribute(
      'href',
      '/admin/integrations',
    );
    expect(screen.queryByRole('region', { name: 'Summary' })).not.toBeInTheDocument();
  });

  it('shows the setup steps without progress to viewers', async () => {
    renderPage(
      '/',
      {
        items: [],
        totals: { services: 0, failing_quality_gates: 0, services_with_critical: 0, fixable: 0 },
        generated_at: daysAgo(0),
      },
      (path) => (path === '/api/v1/me' ? json(200, makeUser({ role: 'viewer' })) : undefined),
    );
    expect(await screen.findByText(/An administrator sets it in Settings/)).toBeInTheDocument();
    expect(screen.queryByText(/of 3 done/)).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'read the SonarQube guide' })).toBeInTheDocument();
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
  });

  it('shows an error with retry', async () => {
    let fail = true;
    const { calls } = mockFetch((call) =>
      call.path === '/api/v1/portfolio'
        ? fail
          ? apiError(500, 'internal', 'boom')
          : json(200, portfolio)
        : call.path === '/api/v1/portfolio/history'
          ? json(200, history)
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
    expect(await screen.findByRole('link', { name: 'alpha' })).toBeInTheDocument();
    expect(calls.length).toBeGreaterThanOrEqual(2);
  });

  it('keeps the cards when the history cannot be loaded', async () => {
    mockFetch((call) =>
      call.path === '/api/v1/portfolio'
        ? json(200, portfolio)
        : call.path === '/api/v1/portfolio/history'
          ? apiError(500, 'internal', 'boom')
          : undefined,
    );
    render(
      <QueryClientProvider client={createQueryClient()}>
        <MemoryRouter>
          <PortfolioPage now={NOW} />
        </MemoryRouter>
      </QueryClientProvider>,
    );
    expect(await screen.findByRole('link', { name: 'alpha' })).toBeInTheDocument();
    await waitFor(() =>
      expect(
        card('alpha').getByText('The spark fills in from the second pipeline.'),
      ).toBeInTheDocument(),
    );
    expect(screen.queryByText(/since 7 days ago/)).not.toBeInTheDocument();
  });
});
