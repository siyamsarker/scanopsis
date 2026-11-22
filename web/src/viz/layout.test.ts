// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { scaleLinear } from 'd3-scale';
import { roundedBottom, roundedTop } from './geometry';
import { nextIndex } from './hooks';
import { heatLevel, rankRows } from './HeatStrip';
import { centreText, irisSegments } from './RiskIris';
import { riskTerms } from './risk';
import { samplePipelines } from './samples';
import {
  coverageDomain,
  laneOrder,
  layoutStrip,
  niceCeil,
  normalizePoints,
  stackSegments,
  stripTicks,
} from './stripLayout';
import {
  findGaps,
  formatAge,
  formatDateTime,
  formatDay,
  formatGap,
  formatMonth,
  pickTimeLabels,
  toTime,
} from './time';

const DAY = 86_400_000;
const deg = (rad: number) => (rad * 180) / Math.PI;

describe('irisSegments', () => {
  it('splits the ring by share of points with 2° gaps', () => {
    const segs = irisSegments(riskTerms({ critical: 1, high: 2 })); // 10 and 10 points
    expect(segs).toHaveLength(2);
    const sweep = segs.map((s) => deg(s.end - s.start));
    expect(sweep[0]).toBeCloseTo(178);
    expect(sweep[1]).toBeCloseTo(178);
    expect(deg(segs[1].start - segs[0].end)).toBeCloseTo(2);
    expect(deg(segs[0].start)).toBeCloseTo(1);
    // The gaps and segments add up to the full circle.
    expect(deg(segs[1].end) + 1).toBeCloseTo(360);
  });

  it('draws a single severity as a full ring without a gap', () => {
    const [seg] = irisSegments(riskTerms({ low: 4 }));
    expect(seg.start).toBe(0);
    expect(seg.end).toBeCloseTo(2 * Math.PI);
  });

  it('keeps a tiny share visible at 3°', () => {
    const segs = irisSegments(riskTerms({ critical: 1, low: 12_480 }));
    expect(deg(segs[0].end - segs[0].start)).toBeCloseTo(3);
    expect(deg(segs[1].end - segs[1].start)).toBeCloseTo(360 - 4 - 3);
  });

  it('has nothing to draw without risk', () => {
    expect(irisSegments(riskTerms({}))).toEqual([]);
  });

  it('compacts the centre number per size', () => {
    expect(centreText(168, 12_490)).toBe('12,490');
    expect(centreText(96, 12_490)).toBe('12.5K');
    expect(centreText(96, 9_999)).toBe('9,999');
    expect(centreText(40, 12_490)).toBe('12K');
    expect(centreText(40, 999)).toBe('999');
  });
});

describe('strip layout', () => {
  const points = normalizePoints(samplePipelines(96));

  it('sorts by time, drops bad dates and keeps the source point', () => {
    const raw = [
      { pipelineId: 2, at: '2026-11-02', ref: 'main', newBySeverity: {}, fixedCount: -1 },
      { pipelineId: 1, at: '2026-11-01', ref: 'main', newBySeverity: { high: 2 }, fixedCount: 3 },
      { pipelineId: 3, at: 'not a date', ref: 'main', newBySeverity: {}, fixedCount: 0 },
    ];
    const out = normalizePoints(raw);
    expect(out.map((p) => p.pipelineId)).toEqual([1, 2]);
    expect(out[0]).toMatchObject({ newTotal: 2, fixed: 3, name: '#1' });
    expect(out[1].fixed).toBe(0);
    expect(out[0].source).toBe(raw[1]);
  });

  it('fits every pipeline when the width allows, with a unit of air for each long pause', () => {
    const layout = layoutStrip(points, { width: 1200, plotHeight: 160, split: false, minStep: 3 });
    expect(layout.hidden).toBe(0);
    expect(layout.columns).toHaveLength(96);
    expect(layout.gaps.length).toBeGreaterThanOrEqual(1);
    expect(layout.barWidth).toBeLessThanOrEqual(24);
    const xs = layout.columns.map((c) => c.cx);
    expect(xs.every((x, i) => i === 0 || x > xs[i - 1])).toBe(true);
    expect(layout.coverage).not.toBeNull();
    expect(layout.lanes).toHaveLength(1);
  });

  it('leaves the oldest pipelines out when the width cannot fit them', () => {
    const many = normalizePoints(samplePipelines(300, { seed: 19, spacingHours: 6 }));
    const layout = layoutStrip(many, { width: 390, plotHeight: 160, split: false, minStep: 3 });
    expect(layout.hidden).toBeGreaterThan(0);
    expect(layout.columns.length + layout.hidden).toBe(300);
    expect(layout.columns.at(-1)?.point).toBe(many.at(-1));
    expect(layout.step).toBeGreaterThanOrEqual(3);
  });

  it('caps a single column at 24 px', () => {
    const layout = layoutStrip(points.slice(0, 1), {
      width: 600,
      plotHeight: 100,
      split: false,
      minStep: 3,
    });
    expect(layout.columns).toHaveLength(1);
    expect(layout.barWidth).toBe(24);
  });

  it('splits into lanes ordered by pipeline count without a coverage track', () => {
    const layout = layoutStrip(points, { width: 1200, plotHeight: 160, split: true, minStep: 3 });
    expect(layout.lanes.map((l) => l.ref)).toEqual(laneOrder(points));
    expect(layout.lanes[0].ref).toBe('main');
    expect(layout.coverage).toBeNull();
    for (let i = 1; i < layout.lanes.length; i++) {
      expect(layout.lanes[i].top).toBeGreaterThan(layout.lanes[i - 1].plotBottom);
    }
  });

  it('fits the coverage domain to the data and threshold in steps of 5', () => {
    expect(coverageDomain([61, 64], 80)).toEqual([55, 85]);
    expect(coverageDomain([99.5])).toEqual([90, 100]);
    expect(coverageDomain([])).toEqual([0, 100]);
    expect(coverageDomain([1])).toEqual([0, 10]);
  });

  it('rounds up to readable numbers and ticks both sides of the baseline', () => {
    expect(niceCeil(27)).toBe(30);
    expect(niceCeil(1180)).toBe(1200);
    expect(niceCeil(0)).toBe(0);
    expect(niceCeil(7)).toBe(8);
    const y = scaleLinear().domain([-20, 40]).range([100, 0]);
    expect(stripTicks(y, 3)).toEqual([-20, -10, 0, 10, 20, 30, 40]);
  });

  it('stacks critical at the baseline with gaps and a flagged top segment', () => {
    const y = scaleLinear().domain([0, 100]).range([200, 0]);
    const segs = stackSegments({ critical: 10, high: 20, low: 0, unknown: 0.2 }, y);
    expect(segs.map((s) => s.severity)).toEqual(['critical', 'high']);
    expect(segs[0]).toMatchObject({ y: 180, height: 20, top: false });
    expect(segs[1].top).toBe(true);
    expect(segs[1].y + segs[1].height).toBeCloseTo(180 - 1);
    // A count far too small for a pixel still gets one.
    const tiny = stackSegments({ low: 1 }, scaleLinear().domain([0, 10_000]).range([100, 0]));
    expect(tiny[0].height).toBeGreaterThanOrEqual(1);
  });
});

describe('bar paths', () => {
  it('rounds only the data end', () => {
    expect(roundedTop(0, 0, 10, 20, 4)).toContain('Q');
    expect(roundedBottom(0, 0, 10, 20, 4)).toContain('Q');
    expect(roundedTop(0, 0, 0.5, 20, 4)).not.toContain('Q');
    expect(roundedBottom(0, 0, 10, 0.2, 4)).not.toContain('Q');
  });
});

describe('time helpers', () => {
  const t0 = Date.UTC(2026, 0, 1);

  it('finds pauses much longer than usual', () => {
    const times = [0, 1, 2, 3, 13, 14].map((d) => t0 + d * DAY);
    expect([...findGaps(times)]).toEqual([4]);
    expect(findGaps([t0, t0 + 30 * DAY]).size).toBe(0);
  });

  it('labels sparsely, only where the date changes and there is room', () => {
    const times = Array.from({ length: 40 }, (_, i) => t0 + Math.floor(i / 2) * DAY);
    const xs = times.map((_, i) => i * 10);
    const labels = pickTimeLabels(xs, times, 64, 'UTC');
    expect(labels[0]).toEqual({ index: 0, text: '1 Jan' });
    for (let i = 1; i < labels.length; i++) {
      expect(xs[labels[i].index] - xs[labels[i - 1].index]).toBeGreaterThanOrEqual(64);
      expect(labels[i].text).not.toBe(labels[i - 1].text);
    }
    expect(pickTimeLabels([], [])).toEqual([]);
  });

  it('switches to months for long ranges', () => {
    const times = [t0, t0 + 200 * DAY];
    expect(pickTimeLabels([0, 500], times, 64, 'UTC').map((l) => l.text)).toEqual([
      'Jan 2026',
      'Jul 2026',
    ]);
  });

  it('formats dates, pauses and ages', () => {
    expect(formatDay(t0, 'UTC')).toBe('1 Jan');
    expect(formatMonth(t0, 'UTC')).toBe('Jan 2026');
    expect(formatDateTime(t0, 'UTC')).toBe('1 Jan 2026, 00:00');
    expect(formatDay(Number.NaN)).toBe('–');
    expect(formatMonth(Number.NaN)).toBe('–');
    expect(formatDateTime(Number.NaN)).toBe('–');
    expect(toTime(null)).toBeNaN();
    expect(formatGap(5 * 3_600_000)).toBe('5 h');
    expect(formatGap(12 * DAY)).toBe('12 d');
    expect(formatGap(35 * DAY)).toBe('5 wk');
    expect(formatGap(200 * DAY)).toBe('7 mo');
    expect(formatAge(t0, t0 + 10_000)).toBe('just now');
    expect(formatAge(t0, t0 + 12 * 60_000)).toBe('12 min ago');
    expect(formatAge(t0, t0 + 3 * 3_600_000)).toBe('3 h ago');
    expect(formatAge(t0, t0 + 2 * DAY)).toBe('2 d ago');
    expect(formatAge(t0, t0 + 35 * DAY)).toBe('5 wk ago');
    expect(formatAge(t0, t0 + 120 * DAY)).toBe('4 mo ago');
    expect(formatAge(Number.NaN, t0)).toBe('never');
  });
});

describe('roving keys', () => {
  it('moves with arrows, jumps with Home, End and Page keys, ignores others', () => {
    expect(nextIndex('ArrowRight', 2, 5)).toBe(3);
    expect(nextIndex('ArrowDown', 4, 5)).toBe(4);
    expect(nextIndex('ArrowLeft', 0, 5)).toBe(0);
    expect(nextIndex('ArrowUp', 3, 5)).toBe(2);
    expect(nextIndex('Home', 3, 5)).toBe(0);
    expect(nextIndex('End', 0, 5)).toBe(4);
    expect(nextIndex('PageDown', 0, 50)).toBe(10);
    expect(nextIndex('PageUp', 5, 50)).toBe(0);
    expect(nextIndex('a', 1, 5)).toBeNull();
    expect(nextIndex('Home', 0, 0)).toBeNull();
  });
});

describe('heat scale and ranking', () => {
  it('maps counts to five levels on a square-root scale with a floor', () => {
    expect(heatLevel(0, 100)).toBe(0);
    expect(heatLevel(100, 100)).toBe(5);
    expect(heatLevel(1, 100)).toBe(1);
    expect(heatLevel(25, 100)).toBe(3);
    expect(heatLevel(3, 3)).toBe(3);
  });

  it('ranks by risk, then name, with unscanned services last', () => {
    const ranked = rankRows([
      { id: 1, name: 'b', counts: { high: 1 } },
      { id: 2, name: 'a', counts: { high: 1 } },
      { id: 3, name: 'c', counts: null },
      { id: 4, name: 'd', counts: { critical: 1 } },
    ]);
    expect(ranked.map((r) => r.row.name)).toEqual(['d', 'a', 'b', 'c']);
    expect(ranked.at(-1)?.risk).toBe(0);
  });
});
