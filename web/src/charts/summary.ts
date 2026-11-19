// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { SEVERITY_LABEL, severityKey } from '../components/severityModel';
import type { ChartOption } from './options';

/** The data behind a chart as a plain table, for the visually hidden fallback. */
export interface ChartSummary {
  columns: string[];
  rows: (string | number)[][];
}

interface SeriesLike {
  type?: string;
  name?: string;
  data?: unknown[];
}

/**
 * Reads the series of an option object into a table: categories down the side
 * for bar and line charts, name / severity / value rows for a treemap. This is
 * what screen readers get instead of the canvas.
 */
export function summarizeOption(option: ChartOption): ChartSummary {
  const o = option as { series?: SeriesLike[]; xAxis?: { data?: unknown[] } };
  const series = o.series ?? [];

  if (series[0]?.type === 'treemap') {
    const data = (series[0].data ?? []) as { name: string; value: number; severity?: string }[];
    return {
      columns: ['Name', 'Worst severity', 'Value'],
      rows: data.map((d) => [
        d.name,
        d.severity && d.severity !== 'none' ? SEVERITY_LABEL[severityKey(d.severity)] : 'None',
        d.value,
      ]),
    };
  }

  const categories = (o.xAxis?.data ?? []).map(String);
  return {
    columns: ['', ...series.map((s) => s.name ?? '')],
    rows: categories.map((category, i) => [
      category,
      ...series.map((s) => {
        const value = s.data?.[i];
        return typeof value === 'number' ? value : '–';
      }),
    ]),
  };
}
