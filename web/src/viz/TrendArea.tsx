// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { max } from 'd3-array';
import { scaleLinear } from 'd3-scale';
import { area, line } from 'd3-shape';
import { useMemo, useState, type ReactNode } from 'react';
import {
  AxisX,
  AxisY,
  ChartTooltip,
  EmptyChart,
  Gridlines,
  MarkLayer,
  SrTable,
  TipRow,
} from './frame';
import { px } from './geometry';
import { useReducedMotion, useWidth } from './hooks';
import { formatCount } from './severity';
import type { SparkTone } from './Sparkline';
import { formatDateTime, formatDay, pickTimeLabels, toTime, type TimeInput } from './time';
import './viz.css';

export interface TrendPoint {
  at: TimeInput;
  /** null leaves a gap, e.g. a day the collector was down. */
  value: number | null;
}

export interface TrendAreaProps {
  points: readonly TrendPoint[];
  /** Accessible name and table caption: "Uploads per day". */
  label: string;
  /** Unit after values in the tooltip and table: "uploads". */
  unit?: string;
  /** A dashed reference line with a direct label, e.g. a limit or a target. */
  reference?: { value: number; label: string };
  height?: number;
  tone?: Exclude<SparkTone, 'risk'>;
  format?: (v: number) => string;
  /** Show the time of day in the tooltip (for hourly data). */
  withTime?: boolean;
  timeZone?: string;
  width?: number;
  animate?: boolean;
  emptyMessage?: ReactNode;
  className?: string;
}

/** A plain time series: 2 px line over a 10% wash, crosshair and readout on hover or focus. */
export function TrendArea({
  points,
  label,
  unit = '',
  reference,
  height = 160,
  tone = 'brand',
  format = (v) => formatCount(v),
  withTime = false,
  timeZone,
  width: fixedWidth,
  animate = true,
  emptyMessage = 'No data for this period yet.',
  className = '',
}: TrendAreaProps) {
  const [box, measured] = useWidth<HTMLDivElement>(640);
  const width = fixedWidth ?? measured;
  const reduced = useReducedMotion();
  const [active, setActive] = useState<number | null>(null);
  const data = useMemo(
    () =>
      points
        .map((p) => ({
          time: toTime(p.at),
          value: typeof p.value === 'number' && Number.isFinite(p.value) ? p.value : null,
        }))
        .filter((p) => Number.isFinite(p.time))
        .sort((a, b) => a.time - b.time),
    [points],
  );

  const defined = data.filter((d) => d.value !== null);
  if (defined.length === 0) {
    return (
      <div className={`viz-trend ${className}`} ref={box}>
        <EmptyChart>{emptyMessage}</EmptyChart>
      </div>
    );
  }

  const left = 40;
  const right = reference ? 12 : 8;
  const top = 14;
  const bottom = height - 26;
  const times = data.map((d) => d.time);
  const t0 = times[0];
  const t1 = times[times.length - 1];
  // Times are plain milliseconds; labels come from pickTimeLabels, so no time scale is needed.
  const x = scaleLinear()
    .domain(t0 === t1 ? [t0 - 43_200_000, t1 + 43_200_000] : [t0, t1])
    .range([left + 4, width - right - 4]);
  const top1 = Math.max(max(defined, (d) => d.value as number) ?? 0, reference?.value ?? 0, 1);
  // A little headroom so a reference line at the maximum keeps its label inside.
  const y = scaleLinear()
    .domain([0, top1 * (reference ? 1.12 : 1)])
    .range([bottom, top])
    .nice(4);
  // Counts get whole-number ticks only: a small range such as 0 to 1 would
  // otherwise label 0.2, 0.4 … and round them to a column of 0s and 1s.
  const counts =
    defined.every((d) => Number.isInteger(d.value)) &&
    (reference === undefined || Number.isInteger(reference.value));
  const ticks = y
    .ticks(4)
    .filter((v) => !counts || Number.isInteger(v))
    .map((v) => ({ value: v, y: y(v), text: formatCount(v, true) }));
  const isDefined = (d: { value: number | null }) => d.value !== null;
  const linePath = line<(typeof data)[number]>()
    .defined(isDefined)
    .x((d) => px(x(d.time)))
    .y((d) => px(y(d.value as number)))(data);
  const areaPath = area<(typeof data)[number]>()
    .defined(isDefined)
    .x((d) => px(x(d.time)))
    .y0(bottom)
    .y1((d) => px(y(d.value as number)))(data);
  const xs = data.map((d) => x(d.time));
  const labels = pickTimeLabels(xs, times, 72, timeZone).map((l) => ({
    key: l.index,
    x: xs[l.index],
    text: l.text,
  }));
  const fmtTime = (t: number) => (withTime ? formatDateTime(t, timeZone) : formatDay(t, timeZone));
  const unitText = unit ? ` ${unit}` : '';
  const rects = data.map((d, i) => {
    const prev = i > 0 ? (xs[i - 1] + xs[i]) / 2 : left;
    const next = i < data.length - 1 ? (xs[i] + xs[i + 1]) / 2 : width - right;
    return {
      key: d.time,
      x: prev,
      y: top,
      width: next - prev,
      height: bottom - top,
      label: `${fmtTime(d.time)}: ${d.value === null ? 'no data' : `${format(d.value)}${unitText}`}`,
    };
  });
  const lastDefined = defined[defined.length - 1];
  const total = defined.reduce((s, d) => s + (d.value as number), 0);
  const summary = `${label}: ${defined.length} values from ${fmtTime(t0)} to ${fmtTime(t1)}, latest ${format(
    lastDefined.value as number,
  )}${unitText}, total ${format(total)}${unitText}.`;
  const draw = animate && !reduced && defined.length > 1;
  const activePoint = active !== null ? data[active] : undefined;

  return (
    <div className={`viz-trend viz-tone-${tone} ${className}`}>
      <div ref={box} className="viz-stage">
        <svg
          className="viz-svg"
          width={width}
          height={height}
          viewBox={`0 0 ${width} ${height}`}
          role="img"
          aria-label={summary}
        >
          <Gridlines ticks={ticks.filter((t) => t.value !== 0)} x1={left} x2={width - right} />
          <line className="viz-baseline" x1={left} x2={width - right} y1={bottom} y2={bottom} />
          <AxisY ticks={ticks} x={left - 8} side="left" />
          {areaPath && (
            <path className={draw ? 'viz-area viz-draw-fade' : 'viz-area'} d={areaPath} />
          )}
          {linePath && defined.length > 1 && (
            <path
              className={draw ? 'viz-line viz-draw-line' : 'viz-line'}
              d={linePath}
              pathLength={draw ? 1 : undefined}
            />
          )}
          {reference && (
            <g className="viz-reference">
              <line
                className="viz-threshold"
                x1={left}
                x2={width - right}
                y1={px(y(reference.value))}
                y2={px(y(reference.value))}
              />
              <text
                className="viz-direct"
                x={width - right}
                y={
                  y(reference.value) - 6 < 12
                    ? px(y(reference.value)) + 16
                    : px(y(reference.value)) - 6
                }
                textAnchor="end"
              >
                {reference.label}
              </text>
            </g>
          )}
          {activePoint && (
            <line
              className="viz-crosshair-line"
              x1={px(x(activePoint.time))}
              x2={px(x(activePoint.time))}
              y1={top}
              y2={bottom}
            />
          )}
          {activePoint && activePoint.value !== null ? (
            <circle
              className="viz-dot viz-dot-end"
              cx={px(x(activePoint.time))}
              cy={px(y(activePoint.value))}
              r={4}
            />
          ) : (
            <circle
              className="viz-dot viz-dot-end"
              cx={px(x(lastDefined.time))}
              cy={px(y(lastDefined.value as number))}
              r={4}
            />
          )}
          <AxisX labels={labels} y={height - 6} width={width} />
        </svg>
        <MarkLayer
          label={`${label}: values`}
          rects={rects}
          active={active}
          onActiveChange={setActive}
        />
        {activePoint && (
          <ChartTooltip
            x={x(activePoint.time)}
            y={Math.max(0, (activePoint.value !== null ? y(activePoint.value) : bottom) - 40)}
            containerWidth={width}
            cardWidth={176}
          >
            <p className="viz-tip-title viz-tip-muted">{fmtTime(activePoint.time)}</p>
            <TipRow
              glyph={<span className="viz-key-line" />}
              label={unit || label}
              value={activePoint.value === null ? 'No data' : format(activePoint.value)}
            />
          </ChartTooltip>
        )}
      </div>
      <SrTable
        caption={label}
        columns={['Time', unit ? unit[0].toUpperCase() + unit.slice(1) : 'Value']}
        rows={data.map((d) => [fmtTime(d.time), d.value === null ? 'No data' : format(d.value)])}
      />
    </div>
  );
}
