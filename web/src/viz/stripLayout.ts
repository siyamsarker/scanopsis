// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { max } from 'd3-array';
import { scaleLinear, type ScaleLinear } from 'd3-scale';
import {
  countOf,
  SEVERITIES,
  totalCount,
  type GateState,
  type Severity,
  type SeverityCounts,
} from './severity';
import { findGaps, toTime, type TimeInput } from './time';

/** One pipeline as the strip draws it. */
export interface PipelinePoint {
  pipelineId: string | number;
  /** When the pipeline ran (or its first upload arrived). */
  at: TimeInput;
  /** Branch or tag. */
  ref: string;
  /** The scanned artifact, e.g. an image reference. */
  artifact?: string;
  /** Findings that this pipeline introduced. */
  newBySeverity: SeverityCounts;
  /** Findings that this pipeline resolved. */
  fixedCount: number;
  gate?: GateState;
  /** Line coverage 0 to 100, when a SonarQube analysis reported it. */
  coverage?: number | null;
  /** Display name; defaults to "#<pipelineId>". */
  label?: string;
}

export interface StripPoint extends PipelinePoint {
  time: number;
  newTotal: number;
  fixed: number;
  name: string;
  /** The point as it was passed in. */
  source: PipelinePoint;
}

/** Sorted by time; unparseable times are dropped; counts made safe. */
export function normalizePoints(points: readonly PipelinePoint[]): StripPoint[] {
  return points
    .map((p) => ({
      ...p,
      time: toTime(p.at),
      newTotal: totalCount(p.newBySeverity),
      fixed: Number.isFinite(p.fixedCount) && p.fixedCount > 0 ? Math.round(p.fixedCount) : 0,
      name: p.label ?? `#${p.pipelineId}`,
      source: p,
    }))
    .filter((p) => Number.isFinite(p.time))
    .sort((a, b) => a.time - b.time || String(a.pipelineId).localeCompare(String(b.pipelineId)));
}

export interface StripColumn {
  point: StripPoint;
  /** Index in the visible points. */
  index: number;
  /** Centre x in px. */
  cx: number;
  /** Lane the column is drawn in. */
  lane: number;
  /** True when an unusually long pause precedes this pipeline. */
  afterGap: boolean;
}

export interface StripLane {
  ref: string | null;
  top: number;
  labelY: number;
  gateY: number;
  plotTop: number;
  plotBottom: number;
  baseline: number;
  y: ScaleLinear<number, number>;
}

export interface StripLayout {
  width: number;
  height: number;
  left: number;
  right: number;
  step: number;
  barWidth: number;
  columns: StripColumn[];
  lanes: StripLane[];
  /** Pipelines left out at the start because the width cannot fit them. */
  hidden: number;
  coverage: { top: number; bottom: number; y: ScaleLinear<number, number> } | null;
  axisY: number;
  gaps: { x: number; ms: number }[];
}

export interface LayoutOptions {
  width: number;
  /** Height of the findings plot in one-lane mode. */
  plotHeight: number;
  split: boolean;
  minStep: number;
  threshold?: number;
}

/** Fitted coverage domain: data and threshold with a little air, in steps of 5. */
export function coverageDomain(values: number[], threshold?: number): [number, number] {
  const all = threshold != null ? [...values, threshold] : values;
  if (all.length === 0) return [0, 100];
  let lo = Math.max(0, Math.floor((Math.min(...all) - 2) / 5) * 5);
  let hi = Math.min(100, Math.ceil((Math.max(...all) + 2) / 5) * 5);
  if (hi - lo < 10) {
    if (hi + 5 <= 100) hi += 5;
    if (lo - 5 >= 0) lo -= 5;
  }
  return [lo, hi];
}

/** Lanes ordered by how many pipelines ran on them, then by name. */
export function laneOrder(points: readonly StripPoint[]): string[] {
  const counts = new Map<string, number>();
  for (const p of points) counts.set(p.ref, (counts.get(p.ref) ?? 0) + 1);
  return [...counts.entries()]
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
    .map(([ref]) => ref);
}

/** The next round number at or above v: 1, 2, 5 times a power of ten. */
export function niceCeil(v: number): number {
  if (v <= 0) return 0;
  const p = 10 ** Math.floor(Math.log10(v));
  for (const m of [1, 1.2, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10]) if (v <= m * p) return m * p;
  return 10 * p;
}

/**
 * Round ticks on both sides of the baseline: [0, top] up, [0, bottom] down.
 * The strip counts findings, so only whole numbers are ticks: a scale of
 * small counts would otherwise label 0.5 as a second "1".
 */
export function stripTicks(y: ScaleLinear<number, number>, count = 2): number[] {
  const [lo, hi] = y.domain();
  const up = scaleLinear().domain([0, hi]).ticks(count);
  const down =
    lo < 0
      ? scaleLinear()
          .domain([0, -lo])
          .ticks(Math.max(1, count - 1))
      : [];
  return [...down.filter((v) => v > 0 && v <= -lo).map((v) => -v), ...up.filter((v) => v <= hi)]
    .filter((v) => Number.isInteger(v))
    .sort((a, b) => a - b);
}

const GATE_ROW = 18;
const LANE_LABEL = 18;
const COVERAGE_HEIGHT = 56;
const AXIS_BAND = 22;

export function layoutStrip(all: readonly StripPoint[], opts: LayoutOptions): StripLayout {
  const { width, plotHeight, split, minStep, threshold } = opts;
  const times = all.map((p) => p.time);
  const gapSet = findGaps(times);
  const hasCoverage = !split && all.some((p) => p.coverage != null && Number.isFinite(p.coverage));
  const left = 40;
  // The right gutter carries the New / Fixed labels and the coverage axis.
  const right = 44;
  const plotW = Math.max(40, width - left - right);

  // Fit as many of the latest pipelines as the width allows; gaps take one unit.
  const maxUnits = Math.max(1, Math.floor(plotW / minStep));
  let units = 0;
  let first = all.length;
  while (first > 0) {
    // Prepending a point makes the pause before the current first one visible.
    const cost = 1 + (first < all.length && gapSet.has(first) ? 1 : 0);
    if (units + cost > maxUnits) break;
    units += cost;
    first -= 1;
  }
  const visible = all.slice(first);
  const step = plotW / Math.max(units, 1);
  const barWidth = Math.max(1, Math.min(24, step - (step >= 4 ? 2 : 1)));

  const refs = split ? laneOrder(visible) : [null];
  const laneIndex = new Map(refs.map((r, i) => [r, i]));

  const maxNew = niceCeil(Math.max(1, max(visible, (p) => p.newTotal) ?? 0));
  const maxFixed = max(visible, (p) => p.fixed) ?? 0;
  // Fixed hangs below on the same scale; it keeps a little room even when empty.
  const below = Math.max(maxFixed > 0 ? niceCeil(maxFixed) : 0, maxNew * 0.2);

  const lanes: StripLane[] = [];
  let cursor = 0;
  const laneHeight = split ? Math.max(56, Math.round(plotHeight * 0.5)) : plotHeight;
  for (const ref of refs) {
    const top = cursor;
    const labelY = top + 12;
    const gateY = top + (split ? LANE_LABEL : 0) + 8;
    const plotTop = top + (split ? LANE_LABEL : 0) + GATE_ROW;
    const plotBottom = plotTop + laneHeight;
    const y = scaleLinear().domain([-below, maxNew]).range([plotBottom, plotTop]);
    lanes.push({ ref, top, labelY, gateY, plotTop, plotBottom, baseline: y(0), y });
    cursor = plotBottom + (split ? 14 : 0);
  }

  let coverage: StripLayout['coverage'] = null;
  if (hasCoverage) {
    const values = visible
      .map((p) => p.coverage)
      .filter((v): v is number => v != null && Number.isFinite(v));
    const top = cursor + 18;
    const bottom = top + COVERAGE_HEIGHT;
    coverage = {
      top,
      bottom,
      y: scaleLinear().domain(coverageDomain(values, threshold)).range([bottom, top]),
    };
    cursor = bottom;
  }
  const axisY = cursor + 16;

  let u = 0;
  const gaps: StripLayout['gaps'] = [];
  const columns: StripColumn[] = visible.map((point, index) => {
    const globalIndex = first + index;
    const afterGap = index > 0 && gapSet.has(globalIndex);
    if (afterGap) {
      gaps.push({ x: left + (u + 0.5) * step, ms: point.time - visible[index - 1].time });
      u += 1;
    }
    const cx = left + (u + 0.5) * step;
    u += 1;
    return { point, index, cx, lane: laneIndex.get(split ? point.ref : null) ?? 0, afterGap };
  });

  return {
    width,
    height: axisY + AXIS_BAND - 10,
    left,
    right,
    step,
    barWidth,
    columns,
    lanes,
    hidden: first,
    coverage,
    axisY,
    gaps,
  };
}

export interface StackSegment {
  severity: Severity;
  y: number;
  height: number;
  top: boolean;
}

/**
 * Stacked segments of one column, critical at the baseline. Each segment keeps
 * at least 1 px; a 1 px gap in the ground colour separates segments tall
 * enough to afford it. Segments are drawn in full colour, never blended.
 */
export function stackSegments(
  counts: SeverityCounts,
  y: ScaleLinear<number, number>,
): StackSegment[] {
  const out: StackSegment[] = [];
  let cum = 0;
  let floor = y(0);
  for (const severity of SEVERITIES) {
    const c = countOf(counts, severity);
    if (c === 0) continue;
    cum += c;
    const top = Math.min(y(cum), floor - 1);
    const gap = out.length > 0 && floor - top > 2 ? 1 : 0;
    out.push({ severity, y: top, height: floor - gap - top, top: false });
    floor = top;
  }
  if (out.length > 0) out[out.length - 1].top = true;
  return out;
}
