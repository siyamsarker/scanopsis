// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Severity vocabulary of the visual kit. The keys match the API's
 * SeverityCounts, so a counts object from the API can be passed as is.
 */

export type Severity = 'critical' | 'high' | 'medium' | 'low' | 'unknown';

/** Counts per severity. Missing keys count as zero. */
export type SeverityCounts = Partial<Record<Severity, number>>;

/** Quality gate result as the charts show it. */
export type GateState = 'pass' | 'fail' | 'none';

/** Most severe first: legends, tables and stacks follow this order. */
export const SEVERITIES: readonly Severity[] = ['critical', 'high', 'medium', 'low', 'unknown'];

export const SEVERITY_NAME: Record<Severity, string> = {
  critical: 'Critical',
  high: 'High',
  medium: 'Medium',
  low: 'Low',
  unknown: 'Unknown',
};

export const GATE_NAME: Record<GateState, string> = {
  pass: 'Gate passed',
  fail: 'Gate failed',
  none: 'No quality gate',
};

/** A count that is safe to add up: negative, fractional or missing values become whole numbers ≥ 0. */
export function countOf(counts: SeverityCounts | null | undefined, severity: Severity): number {
  const value = counts?.[severity];
  if (typeof value !== 'number' || !Number.isFinite(value) || value <= 0) return 0;
  return Math.round(value);
}

export function totalCount(counts: SeverityCounts | null | undefined): number {
  return SEVERITIES.reduce((sum, s) => sum + countOf(counts, s), 0);
}

/** The most severe level with findings, or null when there are none. */
export function worstOf(counts: SeverityCounts | null | undefined): Severity | null {
  return SEVERITIES.find((s) => countOf(counts, s) > 0) ?? null;
}

const intFormat = new Intl.NumberFormat('en-US', { maximumFractionDigits: 0 });
const compactFormat = new Intl.NumberFormat('en-US', {
  notation: 'compact',
  maximumFractionDigits: 1,
});

/** 4217 → "4,217"; with compact, values from 10 000 read "12.3K". */
export function formatCount(value: number, compact = false): string {
  if (!Number.isFinite(value)) return '–';
  if (compact && Math.abs(value) >= 10_000) return compactFormat.format(value);
  return intFormat.format(Math.round(value));
}

/** "2 critical, 44 high"; nothing at all reads "no findings". */
export function describeCounts(counts: SeverityCounts | null | undefined): string {
  const parts = SEVERITIES.filter((s) => countOf(counts, s) > 0).map(
    (s) => `${formatCount(countOf(counts, s))} ${s}`,
  );
  return parts.length > 0 ? parts.join(', ') : 'no findings';
}

export function plural(count: number, one: string, other = `${one}s`): string {
  return `${formatCount(count)} ${count === 1 ? one : other}`;
}
