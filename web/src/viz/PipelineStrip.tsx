// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { line } from 'd3-shape';
import { useMemo, useState, type ReactNode } from 'react';
import {
  AxisX,
  AxisY,
  ChartTooltip,
  EmptyChart,
  GateGlyph,
  GateShape,
  Gridlines,
  Legend,
  MarkLayer,
  severityLegend,
  SeverityGlyph,
  SrTable,
  TipRow,
  type LegendItem,
  type Tick,
} from './frame';
import { px, roundedBottom, roundedTop } from './geometry';
import { useReducedMotion, useWidth } from './hooks';
import {
  countOf,
  describeCounts,
  formatCount,
  GATE_NAME,
  plural,
  SEVERITIES,
  SEVERITY_NAME,
} from './severity';
import {
  layoutStrip,
  normalizePoints,
  stackSegments,
  stripTicks,
  type PipelinePoint,
  type StripLayout,
  type StripPoint,
} from './stripLayout';
import { formatDateTime, formatDay, formatGap, pickTimeLabels } from './time';
import './viz.css';

export type { PipelinePoint } from './stripLayout';

export interface PipelineStripProps {
  points: readonly PipelinePoint[];
  /** The quality gate's coverage condition, 0 to 100; drawn dashed on the coverage track. */
  gateCoverageThreshold?: number;
  /** Height of the findings plot in px (one lane). */
  height?: number;
  /** Offer the "split by branch" toggle when more than one ref is present. */
  lanes?: boolean;
  /** Start split into branch lanes. */
  defaultSplit?: boolean;
  /** Click or Enter on a pipeline column, e.g. to open its scan. */
  onSelect?: (point: PipelinePoint) => void;
  /** Replace the detail card content. */
  renderTooltip?: (point: PipelinePoint) => ReactNode;
  /** Accessible name of the chart. */
  label?: string;
  /** One-time draw on mount (columns grow from the baseline). */
  animate?: boolean;
  timeZone?: string;
  /** Shown when there are no pipelines. */
  emptyMessage?: ReactNode;
  /** Fixed width instead of measuring the container. */
  width?: number;
  /** Narrowest column step in px; older pipelines that do not fit are left out. */
  minStep?: number;
  className?: string;
}

function formatPct(v: number | null | undefined) {
  if (v == null || !Number.isFinite(v)) return '–';
  return `${Math.round(v * 10) / 10}%`;
}

function gateWord(p: StripPoint) {
  return p.gate ? GATE_NAME[p.gate].toLowerCase() : 'no gate result';
}

/** What a column says to assistive technology; the tooltip shows the same. */
export function describePipeline(p: StripPoint, timeZone?: string): string {
  const parts = [
    `Pipeline ${p.name}, ${formatDateTime(p.time, timeZone)}, ${p.ref}`,
    p.newTotal > 0
      ? `${plural(p.newTotal, 'new finding')} (${describeCounts(p.newBySeverity)})`
      : 'no new findings',
    `${formatCount(p.fixed)} fixed`,
    gateWord(p),
  ];
  if (p.coverage != null && Number.isFinite(p.coverage))
    parts.push(`coverage ${formatPct(p.coverage)}`);
  return parts.join(', ');
}

function DetailCard({ p, timeZone, hint }: { p: StripPoint; timeZone?: string; hint: boolean }) {
  return (
    <>
      <p className="viz-tip-title">
        <strong>{p.name}</strong>{' '}
        <span className="viz-tip-muted">{formatDateTime(p.time, timeZone)}</span>
      </p>
      <p className="viz-tip-code">
        {p.ref}
        {p.artifact ? <span className="viz-tip-artifact">{p.artifact}</span> : null}
      </p>
      {p.newTotal === 0 ? (
        <TipRow label="New findings" value="0" />
      ) : (
        SEVERITIES.filter((s) => countOf(p.newBySeverity, s) > 0).map((s) => (
          <TipRow
            key={s}
            glyph={<SeverityGlyph severity={s} />}
            label={`New ${SEVERITY_NAME[s].toLowerCase()}`}
            value={formatCount(countOf(p.newBySeverity, s))}
          />
        ))
      )}
      <TipRow
        glyph={<span className="viz-key-fixed" />}
        label="Fixed"
        value={formatCount(p.fixed)}
      />
      {p.gate && (
        <TipRow glyph={<GateGlyph gate={p.gate} size={10} />} label={GATE_NAME[p.gate]} value="" />
      )}
      {p.coverage != null && Number.isFinite(p.coverage) && (
        <TipRow
          glyph={<span className="viz-key-line" />}
          label="Coverage"
          value={formatPct(p.coverage)}
        />
      )}
      {hint && <p className="viz-tip-note">Select to open this pipeline’s scans.</p>}
    </>
  );
}

function yTicks(layout: StripLayout): Tick[] {
  const lane = layout.lanes[0];
  // Outermost first, so a crowded side keeps its extreme tick.
  const ticks = stripTicks(lane.y, 3).sort((a, b) => Math.abs(b) - Math.abs(a));
  const kept: Tick[] = [{ value: 0, y: lane.y(0), text: '0' }];
  for (const v of ticks) {
    if (v === 0) continue;
    const y = lane.y(v);
    if (kept.some((t) => Math.abs(t.y - y) < 14)) continue;
    kept.push({ value: v, y, text: formatCount(Math.abs(v), true) });
  }
  return kept;
}

/**
 * The pipeline strip: one column per pipeline on a time axis. New findings
 * stack above the baseline by severity, fixed findings hang below it, gate
 * results sit in a dot row on top and coverage runs on its own fitted track
 * underneath with the gate threshold dashed.
 */
export function PipelineStrip({
  points,
  gateCoverageThreshold,
  height = 168,
  lanes = true,
  defaultSplit = false,
  onSelect,
  renderTooltip,
  label = 'Pipeline strip',
  animate = true,
  timeZone,
  emptyMessage = 'No pipelines yet. Columns appear after the first pipeline uploads a scan.',
  width: fixedWidth,
  minStep = 3,
  className = '',
}: PipelineStripProps) {
  const [box, measured] = useWidth<HTMLDivElement>(720);
  const width = fixedWidth ?? measured;
  const reduced = useReducedMotion();
  const [split, setSplit] = useState(defaultSplit);
  const [active, setActive] = useState<number | null>(null);

  const all = useMemo(() => normalizePoints(points), [points]);
  const refCount = useMemo(() => new Set(all.map((p) => p.ref)).size, [all]);
  const canSplit = lanes && refCount > 1;
  const isSplit = canSplit && split;
  const layout = useMemo(
    () =>
      layoutStrip(all, {
        width,
        plotHeight: height,
        split: isSplit,
        minStep,
        threshold: gateCoverageThreshold,
      }),
    [all, width, height, isSplit, minStep, gateCoverageThreshold],
  );

  if (all.length === 0) {
    return (
      <div className={`viz-strip ${className}`}>
        <EmptyChart>{emptyMessage}</EmptyChart>
      </div>
    );
  }

  const { columns, lanes: laneList, coverage, barWidth, step } = layout;
  const first = columns[0].point;
  const last = columns[columns.length - 1].point;
  const sumNew = columns.reduce((s, c) => s + c.point.newTotal, 0);
  const sumFixed = columns.reduce((s, c) => s + c.point.fixed, 0);
  const summary =
    `${label}: ${plural(columns.length, 'pipeline')} from ${formatDay(first.time, timeZone)} to ` +
    `${formatDay(last.time, timeZone)}, ${plural(sumNew, 'new finding')}, ${formatCount(sumFixed)} fixed` +
    `${last.gate ? `, latest ${gateWord(last)}` : ''}.`;

  const labels = pickTimeLabels(
    columns.map((c) => c.cx),
    columns.map((c) => c.point.time),
    72,
    timeZone,
  ).map((l) => ({ key: l.index, x: columns[l.index].cx, text: l.text }));

  const ticks = isSplit ? [] : yTicks(layout);
  const draw = animate && !reduced;
  const half = barWidth / 2;
  const radius = Math.min(4, barWidth / 3);
  const dotR = Math.min(4, (step - 2) / 2);

  const coveragePath =
    coverage &&
    line<(typeof columns)[number]>()
      .x((c) => c.cx)
      .y((c) => coverage.y(c.point.coverage as number))(
      columns.filter((c) => c.point.coverage != null && Number.isFinite(c.point.coverage)),
    );
  const lastCovered = coverage
    ? [...columns]
        .reverse()
        .find((c) => c.point.coverage != null && Number.isFinite(c.point.coverage))
    : undefined;

  const rects = columns.map((c) => {
    const lane = laneList[c.lane];
    const top = isSplit ? lane.top : 0;
    const bottom = isSplit ? lane.plotBottom : (coverage?.bottom ?? lane.plotBottom);
    return {
      key: `${c.point.pipelineId}-${c.index}`,
      x: c.cx - step / 2,
      y: top,
      width: step,
      height: bottom - top,
      label: describePipeline(c.point, timeZone),
    };
  });

  const activeColumn = active !== null ? columns[active] : undefined;
  const legend: LegendItem[] = [
    ...severityLegend(
      SEVERITIES.filter((s) => columns.some((c) => countOf(c.point.newBySeverity, s) > 0)),
    ),
    { key: 'fixed', label: 'Fixed', glyph: <span className="viz-key-fixed" /> },
  ];
  if (coverage) {
    legend.push({ key: 'coverage', label: 'Coverage', glyph: <span className="viz-key-line" /> });
    if (gateCoverageThreshold != null)
      legend.push({
        key: 'threshold',
        label: `Gate ${formatPct(gateCoverageThreshold)}`,
        glyph: <span className="viz-key-dash" />,
      });
  }

  return (
    <div className={`viz-strip ${className}`} data-split={isSplit || undefined}>
      {(canSplit || layout.hidden > 0) && (
        <div className="viz-strip-bar">
          {layout.hidden > 0 && (
            <p className="viz-strip-note">
              Latest {formatCount(columns.length)} of {formatCount(all.length)} pipelines
            </p>
          )}
          {canSplit && (
            <button
              type="button"
              className="viz-toggle"
              aria-pressed={isSplit}
              onClick={() => {
                setActive(null);
                setSplit((v) => !v);
              }}
            >
              <span className="viz-toggle-box" aria-hidden="true" />
              Split by branch
            </button>
          )}
        </div>
      )}
      <div ref={box} className="viz-stage">
        <svg
          className="viz-svg"
          width={layout.width}
          height={layout.height}
          viewBox={`0 0 ${layout.width} ${layout.height}`}
          role="img"
          aria-label={summary}
        >
          {activeColumn && (
            <rect
              className="viz-crosshair"
              x={px(activeColumn.cx - step / 2)}
              y={rects[active as number].y}
              width={px(step)}
              height={rects[active as number].height}
              rx={Math.min(4, step / 4)}
            />
          )}
          {!isSplit && (
            <>
              <Gridlines
                ticks={ticks.filter((t) => t.value !== 0)}
                x1={layout.left}
                x2={layout.width - layout.right}
              />
              <AxisY ticks={ticks} x={layout.left - 8} side="left" />
              <text
                className="viz-direct"
                x={layout.width - layout.right + 8}
                y={px(laneList[0].baseline - 6)}
              >
                New
              </text>
              <text
                className="viz-direct"
                x={layout.width - layout.right + 8}
                y={px(laneList[0].baseline + 15)}
              >
                Fixed
              </text>
            </>
          )}
          {laneList.map((lane) => (
            <g key={lane.ref ?? 'all'}>
              {lane.ref !== null && (
                <text className="viz-lane-label" x={layout.left} y={lane.labelY}>
                  {lane.ref}
                </text>
              )}
              <line
                className="viz-baseline"
                x1={layout.left}
                x2={layout.width - layout.right}
                y1={px(lane.baseline)}
                y2={px(lane.baseline)}
              />
            </g>
          ))}
          {layout.gaps.map((g) => {
            const base = laneList[0].baseline;
            return (
              <g key={g.x} className="viz-break" aria-hidden="true">
                <path
                  d={`M${px(g.x - 4)},${px(base + 5)}L${px(g.x)},${px(base - 5)}M${px(g.x)},${px(base + 5)}L${px(g.x + 4)},${px(base - 5)}`}
                />
                {step >= 28 && (
                  <text x={px(g.x)} y={layout.axisY} textAnchor="middle">
                    {formatGap(g.ms)}
                  </text>
                )}
              </g>
            );
          })}
          <g className={draw ? 'viz-columns viz-draw-grow' : 'viz-columns'}>
            {columns.map((c, i) => {
              const lane = laneList[c.lane];
              const x = px(c.cx - half);
              const segs = stackSegments(c.point.newBySeverity, lane.y);
              const fixedH =
                c.point.fixed > 0 ? Math.max(1, lane.y(-c.point.fixed) - lane.baseline) : 0;
              return (
                <g key={rects[i].key} data-dim={active !== null && active !== i ? true : undefined}>
                  <g className="viz-up">
                    {segs.map((s) =>
                      s.top ? (
                        <path
                          key={s.severity}
                          className={`viz-bar viz-sev-${s.severity}`}
                          d={roundedTop(x, px(s.y), px(barWidth), px(s.height), radius)}
                        />
                      ) : (
                        <rect
                          key={s.severity}
                          className={`viz-bar viz-sev-${s.severity}`}
                          x={x}
                          y={px(s.y)}
                          width={px(barWidth)}
                          height={px(s.height)}
                        />
                      ),
                    )}
                  </g>
                  {fixedH > 0 && (
                    <path
                      className="viz-bar viz-fixed viz-down"
                      d={roundedBottom(x, px(lane.baseline + 1), px(barWidth), px(fixedH), radius)}
                    />
                  )}
                  {c.point.gate &&
                    (dotR >= 2.5 ? (
                      <g transform={`translate(${px(c.cx)},${px(lane.gateY)})`}>
                        <GateShape gate={c.point.gate} r={dotR} />
                      </g>
                    ) : (
                      <rect
                        className={`viz-gate-tick viz-gate-tick-${c.point.gate}`}
                        x={x}
                        y={px(lane.gateY - 3)}
                        width={px(barWidth)}
                        height={6}
                      />
                    ))}
                </g>
              );
            })}
          </g>
          {coverage && (
            <g className="viz-coverage">
              <text className="viz-direct" x={layout.left} y={coverage.top - 6}>
                Coverage
              </text>
              <Gridlines
                ticks={coverage.y.domain().map((v) => ({ value: v, y: coverage.y(v), text: '' }))}
                x1={layout.left}
                x2={layout.width - layout.right}
              />
              <AxisY
                ticks={coverage.y
                  .domain()
                  .map((v) => ({ value: v, y: coverage.y(v), text: `${v}%` }))}
                x={layout.width - layout.right + 8}
                side="right"
              />
              {gateCoverageThreshold != null && (
                <line
                  className="viz-threshold"
                  x1={layout.left}
                  x2={layout.width - layout.right}
                  y1={px(coverage.y(gateCoverageThreshold))}
                  y2={px(coverage.y(gateCoverageThreshold))}
                />
              )}
              {coveragePath && (
                <path
                  className={draw ? 'viz-line viz-draw-line' : 'viz-line'}
                  d={coveragePath}
                  pathLength={draw ? 1 : undefined}
                />
              )}
              {lastCovered && (
                <circle
                  className="viz-dot viz-dot-end"
                  cx={px(lastCovered.cx)}
                  cy={px(coverage.y(lastCovered.point.coverage as number))}
                  r={4}
                />
              )}
            </g>
          )}
          <AxisX labels={labels} y={layout.axisY} width={layout.width} />
        </svg>
        <MarkLayer
          label={`${label}: pipelines`}
          rects={rects}
          active={active}
          onActiveChange={setActive}
          onActivate={onSelect ? (i) => onSelect(columns[i].point.source) : undefined}
        />
        {activeColumn && (
          <ChartTooltip
            x={activeColumn.cx + step / 2}
            y={Math.max(0, laneList[activeColumn.lane].plotTop - 8)}
            containerWidth={layout.width}
          >
            {renderTooltip ? (
              renderTooltip(activeColumn.point.source)
            ) : (
              <DetailCard p={activeColumn.point} timeZone={timeZone} hint={Boolean(onSelect)} />
            )}
          </ChartTooltip>
        )}
      </div>
      <Legend items={legend} />
      <SrTable
        caption={label}
        columns={[
          'Pipeline',
          'Time',
          'Branch',
          ...SEVERITIES.map((s) => `New ${s}`),
          'Fixed',
          'Gate',
          'Coverage',
        ]}
        rows={all.map((p) => [
          p.name,
          formatDateTime(p.time, timeZone),
          p.ref,
          ...SEVERITIES.map((s) => countOf(p.newBySeverity, s)),
          p.fixed,
          p.gate ? GATE_NAME[p.gate] : '–',
          formatPct(p.coverage),
        ])}
      />
    </div>
  );
}
