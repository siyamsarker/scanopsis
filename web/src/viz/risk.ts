// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { countOf, SEVERITIES, type Severity, type SeverityCounts } from './severity';

/**
 * Points per open finding, as OWASP Dependency-Track weighs them for its
 * "inherited risk score". Unknown counts as much as high: an unrated finding is
 * not assumed harmless.
 */
export const RISK_WEIGHTS: Readonly<Record<Severity, number>> = {
  critical: 10,
  high: 5,
  medium: 3,
  low: 1,
  unknown: 5,
};

export const RISK_SOURCE = 'OWASP Dependency-Track weights';

/** One addend of the risk number: count × weight = points. */
export interface RiskTerm {
  severity: Severity;
  count: number;
  weight: number;
  points: number;
  /** points / total points, 0 to 1; 0 when the total is 0. */
  share: number;
}

/** critical×10 + high×5 + medium×3 + low×1 + unknown×5. */
export function riskPoints(counts: SeverityCounts | null | undefined): number {
  return SEVERITIES.reduce((sum, s) => sum + countOf(counts, s) * RISK_WEIGHTS[s], 0);
}

/** Every severity's contribution in fixed order, zero terms included. */
export function riskTerms(counts: SeverityCounts | null | undefined): RiskTerm[] {
  const total = riskPoints(counts);
  return SEVERITIES.map((severity) => {
    const count = countOf(counts, severity);
    const weight = RISK_WEIGHTS[severity];
    const points = count * weight;
    return { severity, count, weight, points, share: total > 0 ? points / total : 0 };
  });
}

/** The arithmetic in words: "2 critical × 10 = 20". */
export function describeTerm(term: RiskTerm): string {
  return `${term.count} ${term.severity} × ${term.weight} = ${term.points}`;
}

/** "Risk 240: 2 critical × 10 = 20, 44 high × 5 = 220." Zero terms are left out. */
export function explainRisk(counts: SeverityCounts | null | undefined): string {
  const total = riskPoints(counts);
  const terms = riskTerms(counts).filter((t) => t.points > 0);
  if (terms.length === 0) return 'Risk 0: no open findings.';
  return `Risk ${total}: ${terms.map(describeTerm).join(', ')}.`;
}

export type RiskDirection = 'up' | 'down' | 'none';

export interface RiskDelta {
  previous: number;
  current: number;
  /** current − previous. */
  delta: number;
  direction: RiskDirection;
  /** Point change per severity, in fixed order. */
  terms: { severity: Severity; delta: number }[];
}

/** How risk moved between two count sets. A missing previous set counts as zero. */
export function riskDelta(
  previous: SeverityCounts | null | undefined,
  current: SeverityCounts | null | undefined,
): RiskDelta {
  const before = riskPoints(previous);
  const after = riskPoints(current);
  const delta = after - before;
  return {
    previous: before,
    current: after,
    delta,
    direction: delta > 0 ? 'up' : delta < 0 ? 'down' : 'none',
    terms: SEVERITIES.map((severity) => ({
      severity,
      delta: (countOf(current, severity) - countOf(previous, severity)) * RISK_WEIGHTS[severity],
    })),
  };
}
