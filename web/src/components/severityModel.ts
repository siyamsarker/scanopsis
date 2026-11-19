// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { formatInt } from '../format';

/** Severity as the UI names it: lower case, matching the SeverityCounts keys of the API. */
export type SeverityKey = 'critical' | 'high' | 'medium' | 'low' | 'unknown';

/** The API's `Severity` enum values are accepted wherever a severity is taken. */
export type SeverityInput = SeverityKey | Uppercase<SeverityKey>;

/** The API's `SeverityCounts` shape. */
export type SeverityCountValues = Record<SeverityKey, number>;

/**
 * The one fixed severity order, most severe first: legends and bars read left
 * to right in this order, stacked charts draw critical at the bottom.
 */
export const SEVERITY_ORDER: readonly SeverityKey[] = [
  'critical',
  'high',
  'medium',
  'low',
  'unknown',
];

export const SEVERITY_LABEL: Record<SeverityKey, string> = {
  critical: 'Critical',
  high: 'High',
  medium: 'Medium',
  low: 'Low',
  unknown: 'Unknown',
};

/** Normalises "CRITICAL" or "critical"; anything unrecognised is "unknown". */
export function severityKey(value: string | null | undefined): SeverityKey {
  const lower = (value ?? '').toLowerCase();
  return SEVERITY_ORDER.find((key) => key === lower) ?? 'unknown';
}

export function totalOf(counts: SeverityCountValues): number {
  return SEVERITY_ORDER.reduce((sum, key) => sum + (counts[key] || 0), 0);
}

/** The most severe level with a non-zero count, or null when there are no findings. */
export function worstSeverity(counts: SeverityCountValues): SeverityKey | null {
  return SEVERITY_ORDER.find((key) => counts[key] > 0) ?? null;
}

/**
 * A spoken summary in fixed order with zero levels left out:
 * "2 critical, 44 high, 3 low". No findings at all reads "No findings".
 */
export function summarizeCounts(counts: SeverityCountValues): string {
  const parts = SEVERITY_ORDER.filter((key) => counts[key] > 0).map(
    (key) => `${formatInt(counts[key])} ${key}`,
  );
  return parts.length > 0 ? parts.join(', ') : 'No findings';
}
