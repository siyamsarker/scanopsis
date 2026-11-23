// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { median } from 'd3-array';

export type TimeInput = string | number | Date;

const HOUR = 3_600_000;
const DAY = 24 * HOUR;

/** Milliseconds since the epoch; NaN for anything unparseable. */
export function toTime(value: TimeInput | null | undefined): number {
  if (value == null) return Number.NaN;
  return new Date(value).getTime();
}

const formatters = new Map<string, Intl.DateTimeFormat>();
function formatter(key: string, options: Intl.DateTimeFormatOptions, timeZone?: string) {
  const id = `${key}|${timeZone ?? ''}`;
  let f = formatters.get(id);
  if (!f) {
    f = new Intl.DateTimeFormat('en-GB', { ...options, timeZone });
    formatters.set(id, f);
  }
  return f;
}

/** "19 Nov". */
export function formatDay(time: number, timeZone?: string): string {
  if (!Number.isFinite(time)) return '–';
  return formatter('day', { day: 'numeric', month: 'short' }, timeZone).format(time);
}

/** "Nov 2026". */
export function formatMonth(time: number, timeZone?: string): string {
  if (!Number.isFinite(time)) return '–';
  return formatter('month', { month: 'short', year: 'numeric' }, timeZone).format(time);
}

/** "19 Nov 2026, 14:03". */
export function formatDateTime(time: number, timeZone?: string): string {
  if (!Number.isFinite(time)) return '–';
  return formatter(
    'datetime',
    {
      day: 'numeric',
      month: 'short',
      year: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
      hourCycle: 'h23',
    },
    timeZone,
  ).format(time);
}

/** A pause between pipelines in the largest sensible unit: "5 h", "12 d", "3 wk". */
export function formatGap(ms: number): string {
  if (ms < DAY) return `${Math.max(1, Math.round(ms / HOUR))} h`;
  if (ms < 21 * DAY) return `${Math.round(ms / DAY)} d`;
  if (ms < 120 * DAY) return `${Math.round(ms / (7 * DAY))} wk`;
  return `${Math.round(ms / (30 * DAY))} mo`;
}

/**
 * Indexes i where the pause before point i is unusually long: longer than
 * `factor` times the median pause and at least `minMs`. Times must be sorted.
 */
export function findGaps(times: readonly number[], factor = 4, minMs = 2 * DAY): Set<number> {
  const gaps = new Set<number>();
  if (times.length < 3) return gaps;
  const diffs: number[] = [];
  for (let i = 1; i < times.length; i++) diffs.push(times[i] - times[i - 1]);
  const typical = median(diffs.filter((d) => d > 0)) ?? 0;
  const limit = Math.max(minMs, typical * factor);
  diffs.forEach((d, i) => {
    if (d > limit) gaps.add(i + 1);
  });
  return gaps;
}

/** Age in the largest sensible unit: "just now", "12 min ago", "3 h ago", "2 d ago", "5 wk ago", "4 mo ago". */
export function formatAge(time: number, now: number): string {
  if (!Number.isFinite(time)) return 'never';
  const ms = Math.max(0, now - time);
  if (ms < 60_000) return 'just now';
  if (ms < HOUR) return `${Math.round(ms / 60_000)} min ago`;
  if (ms < DAY) return `${Math.round(ms / HOUR)} h ago`;
  if (ms < 14 * DAY) return `${Math.round(ms / DAY)} d ago`;
  if (ms < 60 * DAY) return `${Math.round(ms / (7 * DAY))} wk ago`;
  return `${Math.round(ms / (30 * DAY))} mo ago`;
}

export interface TimeLabel {
  index: number;
  text: string;
}

/**
 * Sparse date labels for columns at pixel positions `xs`: a label only where
 * the date (or month, for long ranges) changes and at least `minSpacing` px
 * after the previous one, so labels never collide however many columns exist.
 */
export function pickTimeLabels(
  xs: readonly number[],
  times: readonly number[],
  minSpacing = 64,
  timeZone?: string,
): TimeLabel[] {
  const labels: TimeLabel[] = [];
  if (xs.length === 0) return labels;
  const span = times[times.length - 1] - times[0];
  const byMonth = span > 150 * DAY;
  const fmt = (t: number) => (byMonth ? formatMonth(t, timeZone) : formatDay(t, timeZone));
  let lastX = Number.NEGATIVE_INFINITY;
  let lastText = '';
  for (let i = 0; i < xs.length; i++) {
    const text = fmt(times[i]);
    if (text === lastText) continue;
    if (xs[i] - lastX < minSpacing) continue;
    labels.push({ index: i, text });
    lastX = xs[i];
    lastText = text;
  }
  return labels;
}
