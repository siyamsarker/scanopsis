// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { extent } from 'd3-array';
import { scaleLinear } from 'd3-scale';
import { area, line } from 'd3-shape';
import { px } from './geometry';
import { useReducedMotion } from './hooks';
import { formatCount } from './severity';
import './viz.css';

export type SparkTone = 'neutral' | 'brand' | 'risk' | 'ok';

export interface SparklineProps {
  /** Oldest first. null leaves a gap in the line. */
  values: readonly (number | null | undefined)[];
  width?: number;
  height?: number;
  /** What the values are, for the accessible name: "Risk". */
  label?: string;
  tone?: SparkTone;
  /** A 10% wash under the line. */
  fill?: boolean;
  animate?: boolean;
  /** Format for the accessible name. */
  format?: (v: number) => string;
  className?: string;
}

/** A 2 px line with an end dot and no axes, for tiles and table cells. */
export function Sparkline({
  values,
  width = 96,
  height = 24,
  label = 'Trend',
  tone = 'neutral',
  fill = false,
  animate = true,
  format = (v) => formatCount(v),
  className = '',
}: SparklineProps) {
  const reduced = useReducedMotion();
  const data = values.map((v, i) => ({
    i,
    v: typeof v === 'number' && Number.isFinite(v) ? v : null,
  }));
  const defined = data.filter((d): d is { i: number; v: number } => d.v !== null);

  if (defined.length === 0) {
    return (
      <svg
        className={`viz-sparkline ${className}`}
        width={width}
        height={height}
        role="img"
        aria-label={`${label}: no data yet`}
      >
        <line
          className="viz-baseline viz-baseline-empty"
          x1={0}
          x2={width}
          y1={height / 2}
          y2={height / 2}
        />
      </svg>
    );
  }

  const pad = 4;
  const [lo, hi] = extent(defined, (d) => d.v) as [number, number];
  const y = scaleLinear()
    .domain(lo === hi ? [lo - 1, hi + 1] : [lo, hi])
    .range([height - pad, pad]);
  const x = scaleLinear()
    .domain([0, Math.max(1, data.length - 1)])
    .range(data.length === 1 ? [width / 2, width / 2] : [pad, width - pad]);
  const isDefined = (d: { v: number | null }) => d.v !== null;
  const path = line<{ i: number; v: number | null }>()
    .defined(isDefined)
    .x((d) => px(x(d.i)))
    .y((d) => px(y(d.v as number)))(data);
  const wash = fill
    ? area<{ i: number; v: number | null }>()
        .defined(isDefined)
        .x((d) => px(x(d.i)))
        .y0(height)
        .y1((d) => px(y(d.v as number)))(data)
    : null;
  const last = defined[defined.length - 1];
  const firstV = defined[0].v;
  const aria =
    defined.length === 1
      ? `${label}: ${format(last.v)}`
      : `${label}: ${format(firstV)} to ${format(last.v)} over ${defined.length} values, low ${format(lo)}, high ${format(hi)}`;
  const draw = animate && !reduced && defined.length > 1;

  return (
    <svg
      className={`viz-sparkline viz-tone-${tone} ${className}`}
      width={width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      role="img"
      aria-label={aria}
    >
      {wash && <path className="viz-area" d={wash} />}
      {path && defined.length > 1 && (
        <path
          className={draw ? 'viz-line viz-draw-line' : 'viz-line'}
          d={path}
          pathLength={draw ? 1 : undefined}
        />
      )}
      <circle className="viz-dot viz-dot-end" cx={px(x(last.i))} cy={px(y(last.v))} r={3} />
    </svg>
  );
}

export interface DeltaProps {
  /** The change, e.g. riskDelta(prev, cur).delta. */
  value: number;
  /** Which direction is an improvement. Risk and findings: "down" (the default). */
  better?: 'down' | 'up';
  /** What changed, for screen readers: "risk points". */
  unit?: string;
  /** Since when, for screen readers: "since the previous pipeline". */
  since?: string;
  format?: (v: number) => string;
  className?: string;
}

/**
 * A signed change with an arrow: worse in the failure colour, better in the
 * ok colour, no change muted. The arrow is decoration; the text says it.
 */
export function Delta({
  value,
  better = 'down',
  unit = '',
  since = '',
  format = (v) => formatCount(v),
  className = '',
}: DeltaProps) {
  const v = Number.isFinite(value) ? value : 0;
  const direction = v > 0 ? 'up' : v < 0 ? 'down' : 'none';
  const tone = direction === 'none' ? 'none' : direction === better ? 'good' : 'bad';
  const arrow = direction === 'up' ? '▲' : direction === 'down' ? '▼' : '';
  const amount = format(Math.abs(v));
  const unitText = unit ? ` ${unit}` : '';
  const sinceText = since ? ` ${since}` : '';
  const spoken =
    direction === 'none'
      ? `no change${sinceText}`
      : `${direction === 'up' ? 'up' : 'down'} ${amount}${unitText}${sinceText}, ${tone === 'good' ? 'better' : 'worse'}`;
  return (
    <span className={`viz-delta ${className}`} data-tone={tone}>
      <span aria-hidden="true">
        {arrow && <span className="viz-delta-arrow">{arrow}</span>}
        {direction === 'none' ? '0' : amount}
      </span>
      <span className="viz-sr">{spoken}</span>
    </span>
  );
}
