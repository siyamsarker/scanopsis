// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import type { ReactElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { MemoryRouter } from 'react-router';
import { GateBadge, RatingBadge } from './badges';
import { Freshness } from './Freshness';
import { Card, CardHeader, Grid, PageHeader, Section } from './layout';
import { Mono } from './Mono';
import { SeverityBadge, SeverityBar, SeverityCounts } from './Severity';
import { summarizeCounts, worstSeverity } from './severityModel';
import { StatTile } from './StatTile';

function renderInRouter(ui: ReactElement) {
  return render(<MemoryRouter>{ui}</MemoryRouter>);
}

const counts = { critical: 2, high: 44, medium: 0, low: 3, unknown: 0 };

describe('severity helpers', () => {
  it('summarises in fixed order and leaves out zero levels', () => {
    expect(summarizeCounts(counts)).toBe('2 critical, 44 high, 3 low');
    expect(summarizeCounts({ critical: 0, high: 0, medium: 0, low: 0, unknown: 0 })).toBe(
      'No findings',
    );
    expect(summarizeCounts({ ...counts, high: 1200 })).toBe('2 critical, 1,200 high, 3 low');
  });

  it('finds the worst level', () => {
    expect(worstSeverity(counts)).toBe('critical');
    expect(worstSeverity({ critical: 0, high: 0, medium: 0, low: 1, unknown: 0 })).toBe('low');
    expect(worstSeverity({ critical: 0, high: 0, medium: 0, low: 0, unknown: 0 })).toBeNull();
  });
});

describe('SeverityBadge', () => {
  it('prints the level as text with an icon, accepting API casing', () => {
    const { container } = render(<SeverityBadge severity="CRITICAL" count={1204} />);
    expect(screen.getByText('Critical')).toBeInTheDocument();
    expect(screen.getByText('1,204')).toBeInTheDocument();
    expect(container.querySelector('svg')).toHaveAttribute('aria-hidden', 'true');
    expect(container.firstChild).toHaveClass('tone-critical');
  });

  it('treats an unrecognised level as unknown', () => {
    render(<SeverityBadge severity={'weird' as 'low'} />);
    expect(screen.getByText('Unknown')).toBeInTheDocument();
  });
});

describe('SeverityBar', () => {
  it('describes the breakdown in text and omits zero segments', () => {
    const { container } = render(<SeverityBar counts={counts} />);
    expect(
      screen.getByRole('img', { name: 'Severity breakdown: 2 critical, 44 high, 3 low' }),
    ).toBeInTheDocument();
    const segments = container.querySelectorAll('[class*="tone-"]');
    expect(segments).toHaveLength(3);
    expect(segments[0]).toHaveClass('tone-critical');
    expect(segments[1]).toHaveClass('tone-high');
    expect(segments[2]).toHaveClass('tone-low');
  });

  it('sizes segments in proportion without inline style attributes', () => {
    const { container } = render(<SeverityBar counts={counts} />);
    const segments = container.querySelectorAll<HTMLElement>('[class*="tone-"]');
    expect(segments[0].style.flexGrow).toBe('2');
    expect(segments[1].style.flexGrow).toBe('44');
    // The markup React produces carries no style attribute; sizes are set through the CSSOM.
    expect(renderToStaticMarkup(<SeverityBar counts={counts} />)).not.toMatch(/\sstyle=/);
  });

  it('reads "No findings" for an empty bar and can print the summary', () => {
    render(
      <SeverityBar counts={{ critical: 0, high: 0, medium: 0, low: 0, unknown: 0 }} showSummary />,
    );
    expect(screen.getByRole('img')).toHaveAccessibleName('Severity breakdown: No findings');
    expect(screen.getByText('No findings')).toBeInTheDocument();
  });

  it('turns segments into links when given destinations', () => {
    renderInRouter(
      <SeverityBar
        counts={counts}
        links={{ critical: '/f?severity=critical', high: '/f?severity=high' }}
      />,
    );
    const group = screen.getByRole('group');
    expect(within(group).getByRole('link', { name: '2 critical' })).toHaveAttribute(
      'href',
      '/f?severity=critical',
    );
    expect(within(group).getByRole('link', { name: '44 high' })).toBeInTheDocument();
    expect(within(group).queryByRole('link', { name: /low/ })).toBeNull();
  });
});

describe('SeverityCounts', () => {
  it('lists every level with its label in fixed order', () => {
    render(<SeverityCounts counts={counts} />);
    const items = screen.getAllByRole('listitem').map((li) => li.textContent);
    expect(items).toEqual(['Critical2', 'High44', 'Medium0', 'Low3', 'Unknown0']);
  });

  it('can hide zero levels and link entries', () => {
    renderInRouter(<SeverityCounts counts={counts} hideZero links={{ high: '/h' }} />);
    expect(screen.queryByText('Medium')).toBeNull();
    expect(screen.getByRole('link', { name: /High/ })).toHaveAttribute('href', '/h');
  });

  it('says so when there is nothing to list', () => {
    render(
      <SeverityCounts counts={{ critical: 0, high: 0, medium: 0, low: 0, unknown: 0 }} hideZero />,
    );
    expect(screen.getByText('No findings')).toBeInTheDocument();
  });
});

describe('RatingBadge', () => {
  it.each(['A', 'B', 'C', 'D', 'E'] as const)('prints rating %s as text', (rating) => {
    const { container } = render(<RatingBadge rating={rating} label="Security" />);
    expect(screen.getByText('Security')).toBeInTheDocument();
    expect(container).toHaveTextContent(`Rating ${rating}`);
    expect(container.querySelector(`.tone-${rating.toLowerCase()}`)).not.toBeNull();
  });

  it('says "Not available" for null', () => {
    render(<RatingBadge rating={null} />);
    expect(screen.getByText('Not available')).toBeInTheDocument();
  });
});

describe('GateBadge', () => {
  it.each([
    ['OK', 'Passed'],
    ['ERROR', 'Failed'],
    ['WARN', 'Warning'],
    ['NONE', 'No gate'],
    ['', 'Unknown'],
    [null, 'Unknown'],
  ] as const)('shows %s as "%s" with an icon', (status, text) => {
    const { container } = render(<GateBadge status={status} />);
    expect(screen.getByText(text)).toBeInTheDocument();
    expect(container.querySelector('svg')).toHaveAttribute('aria-hidden', 'true');
  });
});

describe('StatTile', () => {
  it('formats numbers with separators and shows hint and delta', () => {
    render(
      <StatTile
        label="Findings"
        value={12345}
        hint="across 4 services"
        delta={{ value: -3, label: 'since the last scan', better: 'down' }}
      />,
    );
    expect(screen.getByText('Findings')).toBeInTheDocument();
    expect(screen.getByText('12,345')).toHaveClass('tabular-nums');
    expect(screen.getByText('across 4 services')).toBeInTheDocument();
    expect(screen.getByText('−3')).toBeInTheDocument();
    expect(screen.getByText('since the last scan')).toBeInTheDocument();
  });

  it('writes a plus sign for increases', () => {
    render(<StatTile label="Critical" value={5} delta={{ value: 2 }} />);
    expect(screen.getByText('+2')).toBeInTheDocument();
  });

  it('becomes a single link with `to`', () => {
    renderInRouter(<StatTile label="Critical" value={5} to="/findings?severity=critical" />);
    const link = screen.getByRole('link', { name: /Critical\s*5/ });
    expect(link).toHaveAttribute('href', '/findings?severity=critical');
  });

  it('is not a link without `to`', () => {
    render(<StatTile label="Critical" value="–" tone="critical" />);
    expect(screen.queryByRole('link')).toBeNull();
  });
});

describe('Freshness', () => {
  const now = new Date('2026-11-19T12:00:00Z');

  it('shows relative time with the exact time in the title', () => {
    render(<Freshness at="2026-11-19T11:57:00Z" now={now} />);
    const time = screen.getByText('3 minutes ago');
    expect(time.tagName).toBe('TIME');
    expect(time).toHaveAttribute('datetime', '2026-11-19T11:57:00.000Z');
    expect(time.getAttribute('title')).toMatch(/2026/);
    expect(screen.queryByText('Stale')).toBeNull();
  });

  it('shows a Stale warning with text past the threshold', () => {
    render(<Freshness at="2026-11-17T12:00:00Z" now={now} staleAfterMs={24 * 3600_000} />);
    expect(screen.getByText('Stale')).toBeInTheDocument();
    expect(screen.getByText('2 days ago')).toBeInTheDocument();
  });

  it('links to the pipeline inside the app or out to GitLab', () => {
    const { rerender } = renderInRouter(
      <Freshness at={now} now={now} link={{ label: 'Pipeline #42', to: '/scans/42' }} />,
    );
    expect(screen.getByRole('link', { name: 'Pipeline #42' })).toHaveAttribute('href', '/scans/42');
    rerender(
      <MemoryRouter>
        <Freshness
          at={now}
          now={now}
          link={{ label: 'Pipeline #42', href: 'https://gitlab.example/p/42' }}
        />
      </MemoryRouter>,
    );
    const external = screen.getByRole('link', { name: 'Pipeline #42' });
    expect(external).toHaveAttribute('href', 'https://gitlab.example/p/42');
    expect(external).toHaveAttribute('rel', 'noopener noreferrer');
  });

  it.each(['', 'not a date', 'undefined'])('does not throw on the invalid date "%s"', (bad) => {
    render(<Freshness at={bad} now={now} staleAfterMs={1000} />);
    expect(screen.getByText('Not available')).toBeInTheDocument();
    expect(screen.queryByText('Stale')).toBeNull();
    expect(document.querySelector('time')).toBeNull();
  });

  it.each([
    'javascript:alert(1)',
    'data:text/html,<script>alert(1)</script>',
    'not a url',
    '//evil.example/x',
    '',
  ])('renders the label as plain text for the unsafe link "%s"', (href) => {
    render(<Freshness at={now} now={now} link={{ label: 'Pipeline #42', href }} />);
    expect(screen.getByText('Pipeline #42')).toBeInTheDocument();
    expect(screen.queryByRole('link')).toBeNull();
  });

  it('keeps time moving when no fixed clock is given', () => {
    vi.useFakeTimers();
    vi.setSystemTime(now);
    render(<Freshness at={now} />);
    expect(screen.getByText('just now')).toBeInTheDocument();
    act(() => {
      vi.advanceTimersByTime(5 * 60_000);
    });
    expect(screen.getByText('5 minutes ago')).toBeInTheDocument();
    vi.useRealTimers();
  });
});

describe('Mono', () => {
  const sha = '0123456789abcdef0123456789abcdef01234567';

  it('shortens a commit hash and keeps the full value in the tooltip', () => {
    render(<Mono value={sha} kind="sha" />);
    const el = screen.getByText('01234567');
    expect(el).toHaveClass('font-mono');
    expect(el).toHaveAttribute('title', sha);
  });

  it('copies the full value', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal('navigator', { clipboard: { writeText } });
    render(<Mono value={sha} kind="sha" copy copyLabel="Copy commit" />);
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Copy commit' }));
    });
    expect(writeText).toHaveBeenCalledWith(sha);
    expect(screen.getByRole('status')).toHaveTextContent('Copied to the clipboard');
  });

  it('lets a long name wrap after each slash and keeps its text whole', () => {
    const name = 'registry.example.com/platform/payments-api:4.12.0';
    const { container } = render(<Mono value={name} />);
    const el = screen.getByText(name);
    expect(el).toHaveClass('wrap-anywhere');
    expect(el).not.toHaveClass('break-all');
    expect(container.querySelectorAll('wbr')).toHaveLength(2);
  });

  it('can be a link', () => {
    renderInRouter(<Mono value="openssl" to="/packages/openssl" />);
    expect(screen.getByRole('link', { name: 'openssl' })).toHaveAttribute(
      'href',
      '/packages/openssl',
    );
  });
});

describe('layout', () => {
  it('renders the header with title, subtitle, freshness and actions', () => {
    render(
      <PageHeader
        title="payments-api"
        subtitle="platform/payments-api"
        freshness={{ at: '2026-11-19T11:57:00Z', now: new Date('2026-11-19T12:00:00Z') }}
        actions={<button type="button">Rescan</button>}
      />,
    );
    expect(screen.getByRole('heading', { level: 1, name: 'payments-api' })).toBeInTheDocument();
    expect(screen.getByText('platform/payments-api')).toBeInTheDocument();
    expect(screen.getByText('3 minutes ago')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Rescan' })).toBeInTheDocument();
  });

  it('lays stat tiles out in two columns on phones unless single is set', () => {
    render(
      <>
        <Grid cols={4}>
          <span>a</span>
        </Grid>
        <Grid cols={4} single>
          <span>b</span>
        </Grid>
      </>,
    );
    expect(screen.getByText('a').parentElement).toHaveClass('grid-cols-2', 'xl:grid-cols-4');
    expect(screen.getByText('b').parentElement).toHaveClass('grid-cols-1', 'sm:grid-cols-2');
    expect(screen.getByText('b').parentElement).not.toHaveClass('grid-cols-2');
  });

  it('labels a section by its heading and a card by its header', () => {
    render(
      <Section title="Trend">
        <Card as="section" aria-labelledby="c1" padded={false}>
          <CardHeader id="c1" title="Findings over time" level={3} description="Last 30 scans" />
          <Grid cols={4}>
            <span>tile</span>
          </Grid>
        </Card>
      </Section>,
    );
    const section = screen.getByRole('region', { name: 'Trend' });
    expect(
      within(section).getByRole('heading', { level: 3, name: 'Findings over time' }),
    ).toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'Findings over time' })).toBeInTheDocument();
    expect(screen.getByText('Last 30 scans')).toBeInTheDocument();
  });
});
