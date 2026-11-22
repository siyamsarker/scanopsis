// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import Gallery from './Gallery';
import { HeatStrip, type HeatRow } from './HeatStrip';
import { PipelineSpark } from './PipelineSpark';
import { PipelineStrip } from './PipelineStrip';
import { RiskIris } from './RiskIris';
import { SAMPLE_NOW, samplePipelines, sampleServices, sampleUploads } from './samples';
import { DistributionMini, SeverityBar } from './SeverityBar';
import { Delta, Sparkline } from './Sparkline';
import type { PipelinePoint } from './stripLayout';
import { TrendArea } from './TrendArea';

function reduceMotion(reduce: boolean) {
  vi.stubGlobal(
    'matchMedia',
    vi.fn((query: string) => ({
      matches: reduce && query.includes('reduce'),
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    })),
  );
}

const PAYMENTS = { critical: 2, high: 44, medium: 57, low: 29, unknown: 3 };

const POINTS: PipelinePoint[] = [
  {
    pipelineId: 900,
    at: '2026-11-17T10:00:00Z',
    ref: 'main',
    artifact: 'payments-api:900',
    newBySeverity: { high: 2 },
    fixedCount: 1,
    gate: 'pass',
    coverage: 70,
  },
  {
    pipelineId: 901,
    at: '2026-11-18T10:00:00Z',
    ref: 'feature/x',
    newBySeverity: {},
    fixedCount: 4,
    gate: 'fail',
    coverage: 66.25,
  },
  {
    pipelineId: 902,
    at: '2026-11-19T10:00:00Z',
    ref: 'main',
    newBySeverity: { critical: 1, low: 3 },
    fixedCount: 0,
    label: 'Nightly',
  },
];

describe('RiskIris', () => {
  it('names the risk and its arithmetic, and draws one segment per present severity', () => {
    const { container } = render(<RiskIris counts={PAYMENTS} gate="fail" name="payments-api" />);
    const iris = screen.getByRole('img');
    expect(iris).toHaveAccessibleName(
      'payments-api: Risk 455: 2 critical × 10 = 20, 44 high × 5 = 220, 57 medium × 3 = 171, 29 low × 1 = 29, 3 unknown × 5 = 15. Gate failed.',
    );
    expect(container.querySelectorAll('.viz-iris-seg')).toHaveLength(5);
    expect(container.querySelector('.viz-iris-gate-fail')).not.toBeNull();
    expect(container.querySelector('.viz-iris-number')).toHaveTextContent('455');
    expect(container.querySelector('.viz-iris-caption')).toHaveTextContent('risk');
  });

  it('explains every term on focus and walks the segments with the arrow keys', () => {
    render(<RiskIris counts={{ critical: 1, low: 4 }} gate="pass" size={168} />);
    const iris = screen.getByRole('img');
    expect(iris).toHaveAttribute('tabindex', '0');
    expect(screen.queryByTestId('viz-tooltip')).toBeNull();
    fireEvent.focus(iris);
    const tip = screen.getByTestId('viz-tooltip');
    expect(tip).toHaveTextContent('Risk 14');
    expect(tip).toHaveTextContent('Critical 1 × 10');
    expect(tip).toHaveTextContent('Low 4 × 1');
    expect(tip).toHaveTextContent('OWASP Dependency-Track');
    expect(tip).toHaveTextContent('Gate passed');
    fireEvent.keyDown(iris, { key: 'ArrowRight' });
    expect(tip.querySelector('[data-active]')).toHaveTextContent('Critical');
    fireEvent.keyDown(iris, { key: 'ArrowRight' });
    expect(tip.querySelector('[data-active]')).toHaveTextContent('Low');
    fireEvent.keyDown(iris, { key: 'ArrowLeft' });
    expect(tip.querySelector('[data-active]')).toHaveTextContent('Critical');
    fireEvent.keyDown(iris, { key: 'Escape' });
    expect(screen.queryByTestId('viz-tooltip')).toBeNull();
  });

  it('highlights a segment on hover and hides the card on leave', () => {
    const { container } = render(<RiskIris counts={{ critical: 1, low: 4 }} />);
    const iris = screen.getByRole('img');
    fireEvent.pointerEnter(iris);
    fireEvent.pointerEnter(container.querySelectorAll('.viz-iris-seg')[1]);
    expect(container.querySelectorAll('.viz-iris-seg[data-dim]')).toHaveLength(1);
    fireEvent.pointerLeave(iris);
    expect(screen.queryByTestId('viz-tooltip')).toBeNull();
  });

  it('accepts a custom tooltip', () => {
    render(
      <RiskIris counts={{ high: 1 }} renderTooltip={(_, total) => <span>Total {total}</span>} />,
    );
    fireEvent.focus(screen.getByRole('img'));
    expect(screen.getByTestId('viz-tooltip')).toHaveTextContent('Total 5');
  });

  it('shows a calm ring at zero risk and a dashed one without a scan', () => {
    const { container, rerender } = render(<RiskIris counts={{}} />);
    expect(container.querySelector('.viz-iris')).toHaveAttribute('data-state', 'calm');
    expect(screen.getByRole('img')).toHaveAccessibleName('Risk 0: no open findings.');
    rerender(<RiskIris counts={null} name="new" />);
    expect(container.querySelector('.viz-iris')).toHaveAttribute('data-state', 'none');
    expect(screen.getByRole('img')).toHaveAccessibleName('new: no scan yet');
    expect(screen.getByRole('img')).not.toHaveAttribute('tabindex');
    expect(container.querySelector('.viz-iris-caption')).toHaveTextContent('no scan');
  });

  it('draws once with a sweep, and not at all when motion is reduced', () => {
    reduceMotion(false);
    const { container, unmount } = render(<RiskIris counts={PAYMENTS} />);
    expect(container.querySelector('mask .viz-iris-sweep')).not.toBeNull();
    unmount();
    reduceMotion(true);
    const reduced = render(<RiskIris counts={PAYMENTS} />);
    expect(reduced.container.querySelector('mask')).toBeNull();
    reduced.unmount();
    const off = render(<RiskIris counts={PAYMENTS} animate={false} interactive={false} />);
    expect(off.container.querySelector('mask')).toBeNull();
    expect(screen.getByRole('img')).not.toHaveAttribute('tabindex');
  });

  it('counts to a new value when it changes', () => {
    vi.useFakeTimers();
    reduceMotion(false);
    const { container, rerender } = render(<RiskIris counts={{ high: 2 }} size={40} />);
    expect(container.querySelector('.viz-iris-number')).toHaveTextContent('10');
    rerender(<RiskIris counts={{ high: 4 }} size={40} />);
    act(() => {
      vi.advanceTimersByTime(400);
    });
    expect(container.querySelector('.viz-iris-number')).toHaveTextContent('20');
    vi.useRealTimers();
  });
});

describe('PipelineStrip', () => {
  it('summarises the chart and lists every pipeline in a hidden table', () => {
    render(<PipelineStrip points={POINTS} label="payments-api pipelines" timeZone="UTC" />);
    expect(
      screen.getByRole('img', {
        name: 'payments-api pipelines: 3 pipelines from 17 Nov to 19 Nov, 6 new findings, 5 fixed.',
      }),
    ).toBeInTheDocument();
    const table = screen.getByRole('table', { name: 'payments-api pipelines' });
    expect(within(table).getAllByRole('row')).toHaveLength(4);
    expect(within(table).getByRole('rowheader', { name: 'Nightly' })).toBeInTheDocument();
    expect(table).toHaveTextContent('66.3%');
  });

  it('moves between pipelines with the keyboard and opens one with Enter', () => {
    const onSelect = vi.fn();
    render(<PipelineStrip points={POINTS} onSelect={onSelect} timeZone="UTC" />);
    const group = screen.getByRole('group', { name: 'Pipeline strip: pipelines' });
    const marks = within(group).getAllByRole('button');
    expect(marks).toHaveLength(3);
    // One tab stop: the latest pipeline.
    expect(marks.map((m) => m.tabIndex)).toEqual([-1, -1, 0]);
    expect(marks[0]).toHaveAccessibleName(
      'Pipeline #900, 17 Nov 2026, 10:00, main, 2 new findings (2 high), 1 fixed, gate passed, coverage 70%',
    );
    act(() => marks[2].focus());
    expect(screen.getByTestId('viz-tooltip')).toHaveTextContent('Nightly');
    fireEvent.keyDown(marks[2], { key: 'ArrowLeft' });
    expect(document.activeElement).toBe(marks[1]);
    const tip = screen.getByTestId('viz-tooltip');
    expect(tip).toHaveTextContent('#901');
    expect(tip).toHaveTextContent('feature/x');
    expect(tip).toHaveTextContent('New findings0');
    expect(tip).toHaveTextContent('Fixed4');
    expect(tip).toHaveTextContent('Gate failed');
    expect(tip).toHaveTextContent('Coverage66.3%');
    expect(tip).toHaveTextContent('Select to open');
    fireEvent.keyDown(marks[1], { key: 'Enter' });
    fireEvent.click(marks[1]);
    expect(onSelect).toHaveBeenCalledWith(POINTS[1]);
    fireEvent.keyDown(marks[1], { key: 'Home' });
    expect(document.activeElement).toBe(marks[0]);
    fireEvent.keyDown(marks[0], { key: 'x' });
    fireEvent.keyDown(marks[0], { key: 'Escape' });
    expect(screen.queryByTestId('viz-tooltip')).toBeNull();
  });

  it('shows the detail card on hover with a crosshair, and accepts a custom card', () => {
    const { container } = render(
      <PipelineStrip points={POINTS} renderTooltip={(p) => <b>Custom {p.pipelineId}</b>} />,
    );
    const marks = container.querySelectorAll('.viz-mark');
    fireEvent.pointerEnter(marks[0]);
    expect(container.querySelector('.viz-crosshair')).not.toBeNull();
    expect(container.querySelectorAll('.viz-columns > g[data-dim]')).toHaveLength(2);
    expect(screen.getByTestId('viz-tooltip')).toHaveTextContent('Custom 900');
    fireEvent.pointerLeave(container.querySelector('.viz-marks') as Element);
    expect(screen.queryByTestId('viz-tooltip')).toBeNull();
  });

  it('draws stacks, fixed bars, gate dots, coverage and the threshold', () => {
    const { container } = render(<PipelineStrip points={POINTS} gateCoverageThreshold={80} />);
    expect(container.querySelectorAll('.viz-sev-critical.viz-bar')).toHaveLength(1);
    expect(container.querySelectorAll('.viz-fixed')).toHaveLength(2);
    expect(container.querySelectorAll('.viz-gate-pass')).toHaveLength(1);
    expect(container.querySelectorAll('.viz-gate-fail')).toHaveLength(1);
    expect(container.querySelector('.viz-coverage .viz-line')).not.toBeNull();
    expect(container.querySelector('.viz-threshold')).not.toBeNull();
    expect(screen.getByText('Gate 80%')).toBeInTheDocument();
  });

  it('splits into branch lanes on request', () => {
    const { container } = render(<PipelineStrip points={POINTS} />);
    const toggle = screen.getByRole('button', { name: 'Split by branch' });
    expect(toggle).toHaveAttribute('aria-pressed', 'false');
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute('aria-pressed', 'true');
    expect(container.querySelectorAll('.viz-lane-label')).toHaveLength(2);
    expect(container.querySelector('.viz-coverage')).toBeNull();
  });

  it('has no lane toggle for a single branch or when turned off', () => {
    render(<PipelineStrip points={POINTS.filter((p) => p.ref === 'main')} />);
    expect(screen.queryByRole('button', { name: 'Split by branch' })).toBeNull();
  });

  it('notes when older pipelines do not fit and marks long pauses', () => {
    const many = samplePipelines(300, { seed: 19, spacingHours: 6 });
    const narrow = render(<PipelineStrip points={many} width={390} lanes={false} />);
    expect(screen.getByText(/^Latest \d+ of 300 pipelines$/)).toBeInTheDocument();
    narrow.unmount();
    const { container } = render(<PipelineStrip points={many} width={1400} lanes={false} />);
    expect(screen.queryByText(/^Latest/)).toBeNull();
    expect(container.querySelector('.viz-break')).not.toBeNull();
  });

  it('handles one pipeline', () => {
    render(<PipelineStrip points={POINTS.slice(0, 1)} />);
    expect(screen.getAllByRole('button')).toHaveLength(1);
  });

  it('shows what to expect when there are no pipelines', () => {
    render(<PipelineStrip points={[]} />);
    expect(screen.getByRole('note')).toHaveTextContent('No pipelines yet');
    expect(screen.queryByRole('img')).toBeNull();
  });

  it('grows the columns once, and not when motion is reduced', () => {
    reduceMotion(false);
    const a = render(<PipelineStrip points={POINTS} />);
    expect(a.container.querySelector('.viz-draw-grow')).not.toBeNull();
    expect(a.container.querySelector('.viz-draw-line')).not.toBeNull();
    a.unmount();
    reduceMotion(true);
    const b = render(<PipelineStrip points={POINTS} />);
    expect(b.container.querySelector('.viz-draw-grow')).toBeNull();
    expect(b.container.querySelector('.viz-draw-line')).toBeNull();
  });

  it('positions marks through the CSSOM', () => {
    const { container } = render(<PipelineStrip points={POINTS} width={600} />);
    const mark = container.querySelector<HTMLElement>('.viz-mark') as HTMLElement;
    expect(mark.style.left).toMatch(/px$/);
    expect(mark.style.width).toMatch(/px$/);
  });
});

describe('PipelineSpark', () => {
  it('summarises the latest pipelines and shows the latest gate', () => {
    const { container } = render(<PipelineSpark points={POINTS} label="payments-api" />);
    expect(screen.getByRole('img')).toHaveAccessibleName(
      'payments-api: 3 pipelines, 6 new findings, 5 fixed',
    );
    expect(container.querySelector('.viz-spark-gate')).toBeNull();
    render(<PipelineSpark points={POINTS.slice(0, 2)} label="b" animate={false} />);
    expect(
      screen.getByRole('img', {
        name: 'b: 2 pipelines, 2 new findings, 5 fixed, latest gate failed',
      }),
    ).toBeInTheDocument();
  });

  it('draws a dashed baseline when empty', () => {
    render(<PipelineSpark points={[]} />);
    expect(screen.getByRole('img')).toHaveAccessibleName('Recent pipelines: no pipelines yet');
  });
});

describe('Sparkline and Delta', () => {
  it('describes the range and draws a line with an end dot', () => {
    const { container } = render(<Sparkline values={[3, null, 5, 4]} label="Risk" fill />);
    expect(screen.getByRole('img')).toHaveAccessibleName(
      'Risk: 3 to 4 over 3 values, low 3, high 5',
    );
    expect(container.querySelector('.viz-line')).not.toBeNull();
    expect(container.querySelector('.viz-area')).not.toBeNull();
    expect(container.querySelector('.viz-dot-end')).not.toBeNull();
  });

  it('handles one value, a flat line and no data', () => {
    const one = render(<Sparkline values={[7]} label="Coverage" />);
    expect(one.getByRole('img')).toHaveAccessibleName('Coverage: 7');
    expect(one.container.querySelector('.viz-line')).toBeNull();
    one.unmount();
    const flat = render(<Sparkline values={[2, 2]} animate={false} />);
    expect(flat.container.querySelector('.viz-line')).not.toBeNull();
    flat.unmount();
    render(<Sparkline values={[null, undefined]} label="Uploads" />);
    expect(screen.getByRole('img')).toHaveAccessibleName('Uploads: no data yet');
  });

  it('colours more risk as worse, less as better and zero as muted, with words for screen readers', () => {
    const { container, rerender } = render(
      <Delta value={12} unit="risk points" since="since the last scan" />,
    );
    const el = container.querySelector('.viz-delta') as HTMLElement;
    expect(el).toHaveAttribute('data-tone', 'bad');
    expect(el).toHaveTextContent('▲12');
    expect(screen.getByText('up 12 risk points since the last scan, worse')).toHaveClass('viz-sr');
    rerender(<Delta value={-3} />);
    expect(el).toHaveAttribute('data-tone', 'good');
    expect(screen.getByText('down 3, better')).toBeInTheDocument();
    rerender(<Delta value={4} better="up" />);
    expect(el).toHaveAttribute('data-tone', 'good');
    rerender(<Delta value={0} since="this week" />);
    expect(el).toHaveAttribute('data-tone', 'none');
    expect(screen.getByText('no change this week')).toBeInTheDocument();
    rerender(<Delta value={Number.NaN} />);
    expect(el).toHaveAttribute('data-tone', 'none');
  });
});

describe('SeverityBar and DistributionMini', () => {
  it('is one image with a spoken summary when not linked', () => {
    const { container } = render(<SeverityBar counts={PAYMENTS} />);
    expect(screen.getByRole('img')).toHaveAccessibleName(
      'Open findings: 2 critical, 44 high, 57 medium, 29 low, 3 unknown',
    );
    expect(container.querySelectorAll('.viz-seg')).toHaveLength(5);
    expect((container.querySelector('.viz-seg') as HTMLElement).style.flexGrow).toBe('2');
  });

  it('links each segment and prints counts only where they fit', () => {
    render(<SeverityBar counts={PAYMENTS} size="lg" hrefFor={(s) => `/findings?severity=${s}`} />);
    const group = screen.getByRole('group');
    const links = within(group).getAllByRole('link');
    expect(links).toHaveLength(5);
    expect(links[1]).toHaveAttribute('href', '/findings?severity=high');
    expect(links[1]).toHaveAccessibleName('44 high, 33% of open findings');
    // At the 320 px fallback width, 44 of 135 fits and 2 of 135 does not.
    expect(within(links[1]).getByText('44')).toBeInTheDocument();
    expect(links[0]).toHaveTextContent('');
    expect(screen.getByRole('list')).toHaveTextContent('Critical');
  });

  it('scales against a shared maximum and shows an empty state', () => {
    const { container, rerender } = render(<SeverityBar counts={{ low: 5 }} max={20} />);
    expect((container.querySelector('.viz-seg-rest') as HTMLElement).style.flexGrow).toBe('15');
    rerender(<SeverityBar counts={{}} size="lg" />);
    expect(screen.getByRole('img')).toHaveAccessibleName('Open findings: none');
    expect(screen.getByText('No open findings')).toBeInTheDocument();
  });

  it('draws five micro columns with a total', () => {
    const { container } = render(<DistributionMini counts={{ high: 9, low: 1 }} showTotal />);
    expect(container.querySelectorAll('rect')).toHaveLength(5);
    expect(container.querySelectorAll('.viz-mini-zero')).toHaveLength(3);
    expect(screen.getByRole('img')).toHaveAccessibleName('Open findings: 9 high, 1 low');
    expect(screen.getByText('10')).toBeInTheDocument();
    render(<DistributionMini counts={null} label="Nothing" />);
    expect(screen.getByRole('img', { name: 'Nothing: none' })).toBeInTheDocument();
  });
});

describe('HeatStrip', () => {
  const rows: HeatRow[] = [
    { id: 1, name: 'auth', counts: { high: 3 }, gate: 'pass', scannedAt: SAMPLE_NOW - 3_600_000 },
    {
      id: 2,
      name: 'payments',
      counts: { critical: 2, medium: 40 },
      gate: 'fail',
      scannedAt: SAMPLE_NOW - 9 * 86_400_000,
      path: 'platform/payments',
      href: '/services/2',
    },
    { id: 3, name: 'new', counts: null },
  ];

  it('ranks by risk with intensity, gate and freshness columns', () => {
    const { container } = render(<HeatStrip rows={rows} now={SAMPLE_NOW} />);
    const table = screen.getByRole('table', { name: 'Services ranked by risk' });
    const bodyRows = within(table).getAllByRole('row').slice(1);
    expect(bodyRows.map((r) => within(r).getByRole('rowheader').textContent)).toEqual([
      'paymentsplatform/payments',
      'auth',
      'new',
    ]);
    expect(within(bodyRows[0]).getByRole('link', { name: 'payments' })).toHaveAttribute(
      'href',
      '/services/2',
    );
    expect(bodyRows[0]).toHaveTextContent('140');
    expect(bodyRows[0]).toHaveTextContent('Failed');
    expect(bodyRows[0]).toHaveTextContent('9 d ago, stale');
    expect(bodyRows[1]).toHaveTextContent('1 h ago');
    expect(bodyRows[2]).toHaveTextContent('Never');
    const fills = bodyRows[0].querySelectorAll('.viz-heat-fill');
    expect(fills[0]).toHaveAttribute('data-level', '3');
    expect(fills[2]).toHaveAttribute('data-level', '5');
    expect(fills[1]).toHaveAttribute('data-level', '0');
    expect(container.querySelector('.viz-heat-row[tabindex]')).toBeNull();
  });

  it('opens a row with click, Enter or Space and roves with the arrow keys', () => {
    const onSelect = vi.fn();
    render(<HeatStrip rows={rows} onSelect={onSelect} now={SAMPLE_NOW} maxHeight={300} />);
    const bodyRows = screen.getAllByRole('row').slice(1);
    expect(bodyRows.map((r) => r.tabIndex)).toEqual([0, -1, -1]);
    expect(bodyRows[0]).toHaveAccessibleName('payments, risk 140, gate failed');
    expect(bodyRows[2]).toHaveAccessibleName('new, risk not scanned');
    act(() => bodyRows[0].focus());
    fireEvent.keyDown(bodyRows[0], { key: 'ArrowDown' });
    expect(document.activeElement).toBe(bodyRows[1]);
    expect(bodyRows[1].tabIndex).toBe(0);
    fireEvent.keyDown(bodyRows[1], { key: 'Enter' });
    expect(onSelect).toHaveBeenLastCalledWith(rows[0]);
    fireEvent.keyDown(bodyRows[1], { key: 'End' });
    expect(document.activeElement).toBe(bodyRows[2]);
    fireEvent.keyDown(bodyRows[2], { key: ' ' });
    expect(onSelect).toHaveBeenLastCalledWith(rows[2]);
    fireEvent.keyDown(bodyRows[2], { key: 'ArrowRight' });
    fireEvent.keyDown(bodyRows[2], { key: 'q' });
    expect(document.activeElement).toBe(bodyRows[2]);
    fireEvent.click(bodyRows[0]);
    expect(onSelect).toHaveBeenLastCalledWith(rows[1]);
    expect((document.querySelector('.viz-heat-scroll') as HTMLElement).style.maxHeight).toBe(
      '300px',
    );
  });

  it('accepts a custom name cell and shows an empty state', () => {
    const { rerender } = render(<HeatStrip rows={rows} renderName={(r) => <em>{r.name}!</em>} />);
    expect(screen.getByText('auth!')).toBeInTheDocument();
    rerender(<HeatStrip rows={[]} />);
    expect(screen.getByRole('note')).toHaveTextContent('No services yet');
  });
});

describe('TrendArea', () => {
  const points = [
    { at: '2026-11-17T00:00:00Z', value: 10 },
    { at: '2026-11-18T00:00:00Z', value: null },
    { at: '2026-11-19T00:00:00Z', value: 30 },
  ];

  it('summarises, tabulates and reads out values on hover and focus', () => {
    const { container } = render(
      <TrendArea
        points={points}
        label="Uploads per day"
        unit="uploads"
        reference={{ value: 25, label: 'Limit 25' }}
        timeZone="UTC"
      />,
    );
    expect(
      screen.getByRole('img', {
        name: 'Uploads per day: 2 values from 17 Nov to 19 Nov, latest 30 uploads, total 40 uploads.',
      }),
    ).toBeInTheDocument();
    expect(screen.getByRole('table', { name: 'Uploads per day' })).toHaveTextContent('No data');
    expect(screen.getByText('Limit 25')).toBeInTheDocument();
    const marks = screen.getAllByRole('button');
    expect(marks[1]).toHaveAccessibleName('18 Nov: no data');
    fireEvent.pointerEnter(marks[0]);
    expect(screen.getByTestId('viz-tooltip')).toHaveTextContent('17 Nov');
    expect(screen.getByTestId('viz-tooltip')).toHaveTextContent('10');
    expect(container.querySelector('.viz-crosshair-line')).not.toBeNull();
    fireEvent.pointerEnter(marks[1]);
    expect(screen.getByTestId('viz-tooltip')).toHaveTextContent('No data');
  });

  it('handles one value, hourly labels and no data', () => {
    const one = render(
      <TrendArea
        points={points.slice(0, 1)}
        label="Today"
        withTime
        timeZone="UTC"
        animate={false}
      />,
    );
    expect(one.getByRole('img')).toHaveAccessibleName(
      'Today: 1 values from 17 Nov 2026, 00:00 to 17 Nov 2026, 00:00, latest 10, total 10.',
    );
    one.unmount();
    render(<TrendArea points={[]} label="Empty" />);
    expect(screen.getByRole('note')).toHaveTextContent('No data for this period yet.');
  });
});

describe('Gallery', () => {
  it('shows every component in both palettes', () => {
    render(<Gallery />);
    for (const name of [
      'Risk iris',
      'Pipeline strip',
      'Cards',
      'Severity bar and distribution',
      'Heat strip',
      'Trend area',
    ]) {
      expect(screen.getByRole('region', { name })).toBeInTheDocument();
    }
    const root = document.querySelector('.viz-gallery') as HTMLElement;
    expect(root).toHaveAttribute('data-viz-theme', 'dark');
    fireEvent.click(screen.getByRole('button', { name: 'Light' }));
    expect(root).toHaveAttribute('data-viz-theme', 'light');
    fireEvent.click(screen.getAllByRole('row', { name: /risk/ })[0]);
    expect(
      screen.getByText(/^Charts and figures with sample data. Selected: /),
    ).not.toHaveTextContent('Nothing selected yet');
    const strip = screen.getByRole('group', { name: 'payments-api pipelines: pipelines' });
    fireEvent.click(within(strip).getAllByRole('button')[0]);
    expect(screen.getByText(/Selected: pipeline #/)).toBeInTheDocument();
  });

  it('uses the sample generators deterministically', () => {
    expect(samplePipelines(5)).toEqual(samplePipelines(5));
    expect(sampleServices(3)).toEqual(sampleServices(3));
    expect(sampleUploads(3)).toHaveLength(3);
  });
});
