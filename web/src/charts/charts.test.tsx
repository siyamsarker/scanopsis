// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import type { ChartOption } from './options';
import { lineSeries, stackedSeverityBars, treemap, escapeHtml } from './options';
import { mix, readPalette } from './palette';
import { summarizeOption } from './summary';
import { Chart } from './Chart';

// The echarts chunk is replaced by a stub: the test checks what Chart passes to it.
const coreProps = vi.fn();
vi.mock('./ChartCore', () => ({
  default: (props: { option: ChartOption; ariaLabel: string; onItemClick?: () => void }) => {
    coreProps(props);
    return (
      <button type="button" data-testid="core" onClick={props.onItemClick}>
        core
      </button>
    );
  },
}));

const palette = readPalette();
const counts = (c: number, h: number, m: number, l: number, u: number) => ({
  critical: c,
  high: h,
  medium: m,
  low: l,
  unknown: u,
});

type Series = {
  name: string;
  type: string;
  stack?: string;
  data: unknown[];
  itemStyle?: { color: string };
};
const seriesOf = (option: ChartOption) => (option as { series: Series[] }).series;

afterEach(() => {
  document.documentElement.removeAttribute('style');
  document.documentElement.removeAttribute('data-theme');
  coreProps.mockClear();
});

describe('readPalette', () => {
  it('reads colours from the CSS tokens and falls back to the dark palette', () => {
    expect(readPalette().sev.critical).toBe('#e5484d');
    document.documentElement.style.setProperty('--sev-critical', ' #112233 ');
    document.documentElement.style.setProperty('--surface', '#ffffff');
    const p = readPalette();
    expect(p.sev.critical).toBe('#112233');
    expect(p.surface).toBe('#ffffff');
  });

  it('mixes hex colours like color-mix and leaves other values alone', () => {
    expect(mix('#ffffff', '#000000', 0.5)).toBe('#808080');
    expect(mix('#f00', '#000', 1)).toBe('#ff0000');
    expect(mix('color-mix(in srgb, red, blue)', '#000000', 0.5)).toBe(
      'color-mix(in srgb, red, blue)',
    );
  });
});

describe('stackedSeverityBars', () => {
  const points = [
    { label: '#101', counts: counts(2, 44, 0, 3, 0), detail: '0123abcd' },
    { label: '#102', counts: counts(1, 40, 5, 3, 1) },
  ];
  const option = stackedSeverityBars(points)(palette);

  it('stacks the five severities with critical first, hence at the bottom', () => {
    const series = seriesOf(option);
    expect(series.map((s) => s.name)).toEqual(['Critical', 'High', 'Medium', 'Low', 'Unknown']);
    expect(new Set(series.map((s) => s.stack))).toEqual(new Set(['severity']));
    expect(series.every((s) => s.type === 'bar')).toBe(true);
    expect(series[0].data).toEqual([2, 1]);
    expect(series[1].data).toEqual([44, 40]);
  });

  it('takes the colours from the tokens', () => {
    expect(seriesOf(option).map((s) => s.itemStyle?.color)).toEqual([
      palette.sev.critical,
      palette.sev.high,
      palette.sev.medium,
      palette.sev.low,
      palette.sev.unknown,
    ]);
    document.documentElement.style.setProperty('--sev-high', '#010203');
    const themed = stackedSeverityBars(points)(readPalette());
    expect(seriesOf(themed)[1].itemStyle?.color).toBe('#010203');
  });

  it('starts the value axis at zero, with one axis and aria on', () => {
    const o = option as { yAxis: { min: number }; aria: { enabled: boolean } };
    expect(Array.isArray(o.yAxis)).toBe(false);
    expect(o.yAxis.min).toBe(0);
    expect(o.aria.enabled).toBe(true);
  });

  it('shows exact values in the tooltip using classes, not inline styles', () => {
    const tooltip = (option as { tooltip: { formatter: (p: unknown) => string } }).tooltip;
    const html = tooltip.formatter([{ dataIndex: 0 }]);
    expect(html).toContain('>#101<');
    expect(html).toContain('0123abcd');
    expect(html).toContain('<b>44</b>');
    expect(html).toContain('<b>49</b>'); // total
    expect(html).toContain('tone-critical');
    expect(html).not.toMatch(/style=/);
  });

  it('adds zoom for long histories', () => {
    const many = Array.from({ length: 80 }, (_, i) => ({
      label: `#${i}`,
      counts: counts(1, 1, 1, 1, 1),
    }));
    expect((stackedSeverityBars(many)(palette) as { dataZoom?: unknown[] }).dataZoom).toHaveLength(
      2,
    );
    expect((option as { dataZoom?: unknown }).dataZoom).toBeUndefined();
  });
});

describe('lineSeries', () => {
  it('draws one zero-based axis and fixes percentages to 0 to 100', () => {
    const o = lineSeries({
      categories: ['1 Nov', '2 Nov'],
      series: [{ name: 'Coverage', values: [80.5, null] }],
      percent: true,
    })(palette) as {
      yAxis: { min: number; max: number };
      series: Series[];
      aria: { enabled: boolean };
    };
    expect(o.yAxis.min).toBe(0);
    expect(o.yAxis.max).toBe(100);
    expect(o.series[0]).toMatchObject({ name: 'Coverage', type: 'line', data: [80.5, null] });
    expect(o.series[0].itemStyle?.color).toBe(palette.brand);
    expect(o.aria.enabled).toBe(true);
  });

  it('leaves the maximum open for counts and formats tooltip percentages', () => {
    const o = lineSeries({
      categories: ['a'],
      series: [{ name: 'Coverage', values: [84.25] }],
      percent: true,
    })(palette);
    const html = (o as { tooltip: { formatter: (p: unknown) => string } }).tooltip.formatter([
      { dataIndex: 0, seriesName: 'Coverage', value: 84.25 },
    ]);
    expect(html).toContain('84.3%');
    const counts = lineSeries({ categories: ['a'], series: [{ name: 'n', values: [1] }] })(palette);
    expect((counts as { yAxis: { max?: number } }).yAxis.max).toBeUndefined();
  });
});

describe('treemap', () => {
  const items = [
    {
      id: 'a',
      name: 'payments-api',
      value: 120,
      severity: 'critical' as const,
      detail: 'platform/payments-api',
    },
    { id: 'b', name: 'docs', value: 3, severity: null },
  ];
  const option = treemap(items)(palette) as {
    series: {
      type: string;
      data: { id: string; value: number; severity: string; itemStyle: { color: string } }[];
    }[];
    tooltip: { formatter: (p: unknown) => string };
  };

  it('sizes tiles by value and colours them by worst severity', () => {
    const data = option.series[0].data;
    expect(option.series[0].type).toBe('treemap');
    expect(data.map((d) => d.value)).toEqual([120, 3]);
    expect(data[0].itemStyle.color).toBe(mix(palette.sev.critical, palette.surface, 0.28));
    expect(data[1].itemStyle.color).toBe(mix(palette.pass, palette.surface, 0.28));
    expect(data[0].id).toBe('a');
  });

  it('names the severity in the tooltip and escapes names', () => {
    const html = option.tooltip.formatter({ dataIndex: 0 });
    expect(html).toContain('Critical');
    expect(html).toContain('<b>120</b>');
    expect(html).toContain('platform/payments-api');
    expect(escapeHtml('<img onerror="x">')).toBe('&#60;img onerror=&#34;x&#34;&#62;');
    expect(option.tooltip.formatter({ dataIndex: 1 })).toContain('No findings');
  });
});

describe('summarizeOption', () => {
  it('turns bar series into a table', () => {
    const s = summarizeOption(
      stackedSeverityBars([{ label: '#1', counts: counts(1, 2, 3, 4, 5) }])(palette),
    );
    expect(s.columns).toEqual(['', 'Critical', 'High', 'Medium', 'Low', 'Unknown']);
    expect(s.rows).toEqual([['#1', 1, 2, 3, 4, 5]]);
  });

  it('turns treemap tiles into name, severity and value', () => {
    const s = summarizeOption(
      treemap([
        { id: 'a', name: 'api', value: 5, severity: 'high' },
        { id: 'b', name: 'docs', value: 1, severity: null },
      ])(palette),
    );
    expect(s.columns).toEqual(['Name', 'Worst severity', 'Value']);
    expect(s.rows).toEqual([
      ['api', 'High', 5],
      ['docs', 'None', 1],
    ]);
  });
});

describe('Chart', () => {
  const factory = stackedSeverityBars([{ label: '#1', counts: counts(1, 2, 3, 4, 5) }]);

  it('shows a skeleton of the same size, then the lazily loaded chart', async () => {
    render(<Chart option={factory} ariaLabel="Findings per scan" heightClass="h-80" />);
    expect(screen.getByRole('status', { name: 'Loading chart' })).toBeInTheDocument();
    expect(await screen.findByTestId('core')).toBeInTheDocument();
    expect(screen.getByRole('img', { name: 'Findings per scan' })).toBeInTheDocument();
  });

  it('gives the chart code a resolved option and the label', async () => {
    render(<Chart option={factory} ariaLabel="Findings per scan" />);
    await screen.findByTestId('core');
    const props = coreProps.mock.calls.at(-1)?.[0];
    expect(props.ariaLabel).toBe('Findings per scan');
    expect(seriesOf(props.option).map((s) => s.name)[0]).toBe('Critical');
  });

  it('renders a visually hidden data table with the numbers', async () => {
    render(<Chart option={factory} ariaLabel="Findings per scan" />);
    await screen.findByTestId('core');
    const table = screen.getByRole('table', { name: 'Findings per scan' });
    expect(table).toHaveClass('sr-only');
    const row = within(table).getByRole('row', { name: /#1/ });
    expect(
      within(row)
        .getAllByRole('cell')
        .map((c) => c.textContent),
    ).toEqual(['1', '2', '3', '4', '5']);
  });

  it('builds the option again with new colours when the theme changes', async () => {
    render(<Chart option={factory} ariaLabel="Findings per scan" />);
    await screen.findByTestId('core');
    const before = seriesOf(coreProps.mock.calls.at(-1)?.[0].option)[1].itemStyle?.color;
    act(() => {
      document.documentElement.style.setProperty('--sev-high', '#0a0b0c');
      document.documentElement.setAttribute('data-theme', 'light');
    });
    await waitFor(() => {
      const after = seriesOf(coreProps.mock.calls.at(-1)?.[0].option)[1].itemStyle?.color;
      expect(after).toBe('#0a0b0c');
    });
    expect(before).not.toBe('#0a0b0c');
  });

  it('forwards item clicks', async () => {
    const onItemClick = vi.fn();
    render(<Chart option={factory} ariaLabel="x" onItemClick={onItemClick} />);
    fireEvent.click(await screen.findByTestId('core'));
    expect(onItemClick).toHaveBeenCalled();
  });

  it('designs loading, empty and error states in the same box', () => {
    const onRetry = vi.fn();
    const { rerender } = render(
      <Chart option={factory} ariaLabel="x" status="loading" heightClass="h-64" />,
    );
    expect(screen.getByRole('status', { name: 'Loading chart' })).toHaveClass('h-64');
    rerender(
      <Chart
        option={factory}
        ariaLabel="x"
        status="empty"
        emptyTitle="No scans yet"
        heightClass="h-64"
      />,
    );
    expect(screen.getByRole('heading', { name: 'No scans yet' })).toBeInTheDocument();
    rerender(
      <Chart option={factory} ariaLabel="x" status="error" onRetry={onRetry} heightClass="h-64" />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(onRetry).toHaveBeenCalled();
    expect(coreProps).not.toHaveBeenCalled();
  });
});
