// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import type { EChartsCoreOption } from 'echarts/core';
import {
  SEVERITY_LABEL,
  SEVERITY_ORDER,
  type SeverityCountValues,
  type SeverityKey,
} from '../components/severityModel';
import { formatInt, formatPercent } from '../format';
import { mix, type Palette } from './palette';

/** A chart's option object. Types only; echarts itself is loaded on demand. */
export type ChartOption = EChartsCoreOption;

/**
 * Chart helpers return a factory instead of a finished option, because colours
 * come from the theme: the Chart component calls it again with the new palette
 * whenever the theme changes. Memoise the factory (useMemo) so it is not rebuilt
 * on every render of the parent.
 */
export type ChartFactory = (palette: Palette) => ChartOption;

const FONT = "'Inter Variable', ui-sans-serif, system-ui, sans-serif";

/** Escapes text that goes into tooltip HTML (labels can come from scan data). */
export function escapeHtml(text: string): string {
  return text.replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`);
}

/**
 * Tooltip rows use CSS classes from index.css (chart-tip, chart-dot, tone-*)
 * because inline style attributes in markup are blocked by the CSP.
 */
function tipRow(tone: string, label: string, value: string): string {
  return `<div class="chart-tip__row"><i class="chart-dot tone-${tone}"></i><span>${escapeHtml(label)}</span><b>${value}</b></div>`;
}

function tipShell(title: string, rows: string, sub?: string): string {
  return `<div class="chart-tip"><div class="chart-tip__title">${escapeHtml(title)}</div>${
    sub ? `<div class="chart-tip__sub">${escapeHtml(sub)}</div>` : ''
  }${rows}</div>`;
}

function base(p: Palette): ChartOption {
  return {
    backgroundColor: 'transparent',
    textStyle: { fontFamily: FONT, color: p.textMuted },
    aria: { enabled: true, decal: { show: false } },
    tooltip: {
      confine: true,
      backgroundColor: p.surfaceRaised,
      borderColor: p.borderStrong,
      borderWidth: 1,
      padding: [8, 12],
      textStyle: { color: p.text, fontFamily: FONT, fontSize: 12 },
      extraCssText: 'border-radius: 8px;',
    },
  };
}

function axes(p: Palette, categories: string[], yMax?: number): ChartOption {
  return {
    xAxis: {
      type: 'category',
      data: categories,
      axisLine: { lineStyle: { color: p.borderStrong } },
      axisTick: { show: false },
      axisLabel: { color: p.textMuted, hideOverlap: true, fontFamily: FONT },
    },
    // Counts and percentages always start at zero; there is one value axis, never two.
    yAxis: {
      type: 'value',
      min: 0,
      ...(yMax !== undefined ? { max: yMax } : {}),
      minInterval: 1,
      splitLine: { lineStyle: { color: p.border } },
      axisLabel: {
        color: p.textMuted,
        fontFamily: FONT,
        formatter: (value: number) => formatInt(value),
      },
    },
  };
}

// ---------------------------------------------------------------------------

export interface SeverityPoint {
  /** The axis label: a date, a pipeline number or a short commit. */
  label: string;
  counts: SeverityCountValues;
  /** The commit or pipeline, shown under the title in the tooltip. */
  detail?: string;
}

interface AxisTipParam {
  seriesName: string;
  value: number | null;
  axisValueLabel?: string;
  dataIndex: number;
}

/**
 * Question: "How did the findings of this service change scan by scan?"
 * One stacked bar per scan. Series are added in the fixed severity order, so
 * critical is at the bottom, then high, medium, low and unknown. The value axis
 * starts at zero and the tooltip lists the exact counts and the total.
 */
export function stackedSeverityBars(points: SeverityPoint[]): ChartFactory {
  return (p) => ({
    ...base(p),
    ...axes(
      p,
      points.map((point) => point.label),
    ),
    grid: { left: 8, right: 8, top: 40, bottom: points.length > 60 ? 40 : 8, containLabel: true },
    legend: {
      top: 0,
      left: 0,
      icon: 'roundRect',
      itemWidth: 10,
      itemHeight: 10,
      itemGap: 16,
      textStyle: { color: p.text, fontFamily: FONT },
      inactiveColor: p.textMuted,
      data: SEVERITY_ORDER.map((key) => SEVERITY_LABEL[key]),
    },
    ...(points.length > 60
      ? {
          dataZoom: [
            { type: 'inside', start: 50, end: 100 },
            { type: 'slider', height: 16, bottom: 4 },
          ],
        }
      : {}),
    tooltip: {
      ...(base(p).tooltip as object),
      trigger: 'axis',
      axisPointer: { type: 'shadow', shadowStyle: { color: mix(p.text, p.surface, 0.06) } },
      formatter: (raw: unknown) => {
        const params = raw as AxisTipParam[];
        const index = params[0]?.dataIndex ?? 0;
        const point = points[index];
        if (!point) return '';
        const rows = SEVERITY_ORDER.map((key) =>
          tipRow(key, SEVERITY_LABEL[key], formatInt(point.counts[key])),
        ).join('');
        const total = SEVERITY_ORDER.reduce((sum, key) => sum + point.counts[key], 0);
        return tipShell(
          point.label,
          `${rows}${tipRow('neutral', 'Total', formatInt(total))}`,
          point.detail,
        );
      },
    },
    series: SEVERITY_ORDER.map((key) => ({
      name: SEVERITY_LABEL[key],
      type: 'bar',
      stack: 'severity',
      data: points.map((point) => point.counts[key]),
      barMaxWidth: 32,
      itemStyle: { color: p.sev[key], borderColor: p.surface, borderWidth: 1 },
      emphasis: { focus: 'series' },
    })),
  });
}

// ---------------------------------------------------------------------------

export interface LineSeries {
  name: string;
  /** One value per category; null leaves a gap. */
  values: (number | null)[];
}

/**
 * Question: "How is this measure moving over time?" (coverage, for example).
 * One line per series on a single value axis that starts at zero. With
 * `percent` the axis is fixed to 0 to 100 and values print with a % sign.
 */
export function lineSeries(options: {
  categories: string[];
  series: LineSeries[];
  percent?: boolean;
}): ChartFactory {
  const { categories, series, percent = false } = options;
  const fmt = (value: number | null) =>
    value === null ? '–' : percent ? formatPercent(value) : formatInt(value);
  return (p) => {
    const colours = [p.brand, p.textMuted, p.sev.low, p.pass];
    return {
      ...base(p),
      ...axes(p, categories, percent ? 100 : undefined),
      grid: { left: 8, right: 16, top: series.length > 1 ? 40 : 16, bottom: 8, containLabel: true },
      ...(series.length > 1
        ? {
            legend: {
              top: 0,
              left: 0,
              icon: 'roundRect',
              itemWidth: 10,
              itemHeight: 10,
              textStyle: { color: p.text, fontFamily: FONT },
              data: series.map((s) => s.name),
            },
          }
        : {}),
      tooltip: {
        ...(base(p).tooltip as object),
        trigger: 'axis',
        axisPointer: { type: 'line', lineStyle: { color: p.borderStrong } },
        formatter: (raw: unknown) => {
          const params = raw as AxisTipParam[];
          const label = categories[params[0]?.dataIndex ?? 0] ?? '';
          const rows = params
            .map((param, i) => {
              const tone = ['brand', 'neutral', 'low', 'pass'][i % 4];
              return tipRow(tone, param.seriesName, fmt(param.value));
            })
            .join('');
          return tipShell(label, rows);
        },
      },
      series: series.map((s, i) => ({
        name: s.name,
        type: 'line',
        data: s.values,
        showSymbol: categories.length <= 40,
        symbolSize: 6,
        lineStyle: { width: 2, color: colours[i % colours.length] },
        itemStyle: { color: colours[i % colours.length] },
        ...(series.length === 1 ? { areaStyle: { color: colours[0], opacity: 0.08 } } : {}),
        emphasis: { focus: 'series' },
      })),
    };
  };
}

// ---------------------------------------------------------------------------

export interface TreemapItem {
  /** Stable id, returned to onItemClick through `data.id`. */
  id: string;
  name: string;
  /** Tile area is proportional to this (findings, for example). */
  value: number;
  /** Tile colour; the worst severity of the item, or null for none. */
  severity: SeverityKey | null;
  /** Extra line in the tooltip, such as the project path. */
  detail?: string;
}

/**
 * Question: "Which services carry the most findings, and how bad are they?"
 * Tile area is the value, tile colour is the worst severity. Fills are a tint of
 * the severity colour so the label text stays readable in both themes, and every
 * tile names its severity in words ("Critical") or "No findings".
 */
export function treemap(items: TreemapItem[], options: { valueLabel?: string } = {}): ChartFactory {
  const valueLabel = options.valueLabel ?? 'findings';
  // echarts sorts treemap tiles by value, so tiles are matched by id, never by position.
  const byId = new Map(items.map((item) => [item.id, item]));
  const lookup = (raw: unknown) => byId.get((raw as { data?: { id?: string } }).data?.id ?? '');
  const levelText = (severity: SeverityKey | null) =>
    severity ? SEVERITY_LABEL[severity] : 'No findings';
  return (p) => ({
    ...base(p),
    tooltip: {
      ...(base(p).tooltip as object),
      trigger: 'item',
      formatter: (raw: unknown) => {
        const item = lookup(raw);
        if (!item) return '';
        return tipShell(
          item.name,
          `${tipRow(item.severity ?? 'pass', levelText(item.severity), '')}${tipRow(
            'neutral',
            valueLabel[0].toUpperCase() + valueLabel.slice(1),
            formatInt(item.value),
          )}`,
          item.detail,
        );
      },
    },
    series: [
      {
        type: 'treemap',
        roam: false,
        nodeClick: false,
        breadcrumb: { show: false },
        left: 0,
        right: 0,
        top: 0,
        bottom: 0,
        width: '100%',
        height: '100%',
        squareRatio: 0.9,
        itemStyle: { borderColor: p.surface, borderWidth: 2, gapWidth: 2, borderRadius: 6 },
        label: {
          show: true,
          position: 'insideTopLeft',
          padding: [8, 10],
          color: p.text,
          fontFamily: FONT,
          fontSize: 12,
          lineHeight: 16,
          overflow: 'truncate',
          formatter: (raw: unknown) => {
            const item = lookup(raw);
            return item
              ? `${item.name}\n${levelText(item.severity)} · ${formatInt(item.value, { compact: true })}`
              : '';
          },
        },
        emphasis: { itemStyle: { borderColor: p.text } },
        data: items.map((item) => {
          const colour = item.severity ? p.sev[item.severity] : p.pass;
          return {
            id: item.id,
            name: item.name,
            value: item.value,
            severity: item.severity ?? 'none',
            itemStyle: { color: mix(colour, p.surface, 0.28) },
          };
        }),
      },
    ],
  });
}
