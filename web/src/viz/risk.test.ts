// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { describeTerm, explainRisk, RISK_WEIGHTS, riskDelta, riskPoints, riskTerms } from './risk';
import { countOf, describeCounts, formatCount, plural, totalCount, worstOf } from './severity';

describe('riskPoints', () => {
  it('weighs critical 10, high 5, medium 3, low 1 and unknown 5', () => {
    expect(RISK_WEIGHTS).toEqual({ critical: 10, high: 5, medium: 3, low: 1, unknown: 5 });
    expect(riskPoints({ critical: 2, high: 44, medium: 57, low: 29, unknown: 3 })).toBe(
      20 + 220 + 171 + 29 + 15,
    );
  });

  it('treats missing, negative, fractional and non-finite counts safely', () => {
    expect(riskPoints(null)).toBe(0);
    expect(riskPoints(undefined)).toBe(0);
    expect(riskPoints({})).toBe(0);
    expect(riskPoints({ critical: -3, high: Number.NaN, low: Infinity, medium: 1.6 })).toBe(6);
  });
});

describe('riskTerms', () => {
  it('lists every severity in fixed order with its share of the points', () => {
    const terms = riskTerms({ critical: 1, low: 10 });
    expect(terms.map((t) => t.severity)).toEqual(['critical', 'high', 'medium', 'low', 'unknown']);
    expect(terms[0]).toEqual({
      severity: 'critical',
      count: 1,
      weight: 10,
      points: 10,
      share: 0.5,
    });
    expect(terms[3].share).toBe(0.5);
    expect(terms.reduce((s, t) => s + t.share, 0)).toBeCloseTo(1);
  });

  it('gives zero shares when there is no risk', () => {
    expect(riskTerms({}).every((t) => t.share === 0)).toBe(true);
  });

  it('explains the arithmetic in words', () => {
    expect(describeTerm(riskTerms({ high: 3 })[1])).toBe('3 high × 5 = 15');
    expect(explainRisk({ critical: 2, high: 3 })).toBe(
      'Risk 35: 2 critical × 10 = 20, 3 high × 5 = 15.',
    );
    expect(explainRisk({})).toBe('Risk 0: no open findings.');
  });
});

describe('riskDelta', () => {
  it('reports the change, its direction and the change per severity', () => {
    const d = riskDelta({ critical: 1, high: 2 }, { critical: 0, high: 5, low: 4 });
    expect(d.previous).toBe(20);
    expect(d.current).toBe(29);
    expect(d.delta).toBe(9);
    expect(d.direction).toBe('up');
    expect(d.terms).toEqual([
      { severity: 'critical', delta: -10 },
      { severity: 'high', delta: 15 },
      { severity: 'medium', delta: 0 },
      { severity: 'low', delta: 4 },
      { severity: 'unknown', delta: 0 },
    ]);
  });

  it('reads down and none, and counts a missing previous set as zero', () => {
    expect(riskDelta({ high: 2 }, { high: 1 }).direction).toBe('down');
    expect(riskDelta({ high: 2 }, { high: 2 }).direction).toBe('none');
    expect(riskDelta(null, { low: 3 })).toMatchObject({ previous: 0, current: 3, delta: 3 });
  });
});

describe('severity helpers', () => {
  it('counts, totals and names', () => {
    expect(countOf({ high: 2.4 }, 'high')).toBe(2);
    expect(totalCount({ high: 2, low: 1 })).toBe(3);
    expect(worstOf({ low: 1, medium: 2 })).toBe('medium');
    expect(worstOf({})).toBeNull();
    expect(describeCounts({ critical: 1, low: 1200 })).toBe('1 critical, 1,200 low');
    expect(describeCounts(null)).toBe('no findings');
    expect(formatCount(12_345, true)).toBe('12.3K');
    expect(formatCount(9_999, true)).toBe('9,999');
    expect(formatCount(Number.NaN)).toBe('–');
    expect(plural(1, 'pipeline')).toBe('1 pipeline');
    expect(plural(2, 'pipeline')).toBe('2 pipelines');
  });
});
