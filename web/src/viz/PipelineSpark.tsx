// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { max } from 'd3-array';
import { scaleLinear } from 'd3-scale';
import { useMemo } from 'react';
import { px, roundedBottom, roundedTop } from './geometry';
import { useReducedMotion } from './hooks';
import { formatCount, GATE_NAME, plural } from './severity';
import { normalizePoints, stackSegments, type PipelinePoint } from './stripLayout';
import './viz.css';

export interface PipelineSparkProps {
  points: readonly PipelinePoint[];
  width?: number;
  height?: number;
  /** Most recent pipelines to show; older ones are left out. */
  limit?: number;
  /** Accessible name prefix, e.g. the service name. */
  label?: string;
  animate?: boolean;
  className?: string;
}

/**
 * The pipeline strip in card size: the latest pipelines as tiny columns, new
 * above the baseline by severity and fixed below it, with no axes or labels.
 * The latest gate result is a dot at the right end.
 */
export function PipelineSpark({
  points,
  width = 120,
  height = 28,
  limit = 30,
  label = 'Recent pipelines',
  animate = true,
  className = '',
}: PipelineSparkProps) {
  const reduced = useReducedMotion();
  const all = useMemo(() => normalizePoints(points).slice(-limit), [points, limit]);
  const gateSpace = all.some((p) => p.gate) ? 10 : 0;
  const plotW = width - gateSpace;

  if (all.length === 0) {
    return (
      <svg
        className={`viz-spark ${className}`}
        width={width}
        height={height}
        role="img"
        aria-label={`${label}: no pipelines yet`}
      >
        <line
          className="viz-baseline viz-baseline-empty"
          x1={0}
          x2={width}
          y1={height * 0.65}
          y2={height * 0.65}
        />
      </svg>
    );
  }

  const maxNew = Math.max(1, max(all, (p) => p.newTotal) ?? 0);
  const maxFixed = max(all, (p) => p.fixed) ?? 0;
  const y = scaleLinear()
    .domain([-Math.max(maxFixed, maxNew * 0.25), maxNew])
    .range([height - 1, 1]);
  const step = plotW / Math.max(all.length, 8);
  const bar = Math.max(1, Math.min(6, step - 1));
  const base = y(0);
  const lastP = all[all.length - 1];
  const sumNew = all.reduce((s, p) => s + p.newTotal, 0);
  const sumFixed = all.reduce((s, p) => s + p.fixed, 0);
  const aria =
    `${label}: ${plural(all.length, 'pipeline')}, ${plural(sumNew, 'new finding')}, ` +
    `${formatCount(sumFixed)} fixed${lastP.gate ? `, latest ${GATE_NAME[lastP.gate].toLowerCase()}` : ''}`;
  // Right-aligned so the latest pipeline always sits next to the gate dot.
  const offset = plotW - all.length * step;

  return (
    <svg
      className={`viz-spark ${className}`}
      width={width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      role="img"
      aria-label={aria}
    >
      <line className="viz-baseline" x1={0} x2={plotW} y1={px(base)} y2={px(base)} />
      <g className={animate && !reduced ? 'viz-columns viz-draw-grow' : 'viz-columns'}>
        {all.map((p, i) => {
          const x = px(offset + i * step + (step - bar) / 2);
          const segs = stackSegments(p.newBySeverity, y);
          const fixedH = p.fixed > 0 ? Math.max(1, y(-p.fixed) - base) : 0;
          return (
            <g key={`${p.pipelineId}-${i}`}>
              <g className="viz-up">
                {segs.map((s) => (
                  <path
                    key={s.severity}
                    className={`viz-bar viz-sev-${s.severity}`}
                    d={roundedTop(x, px(s.y), px(bar), px(s.height), s.top ? 1.5 : 0)}
                  />
                ))}
              </g>
              {fixedH > 0 && (
                <path
                  className="viz-bar viz-fixed viz-down"
                  d={roundedBottom(x, px(base + 1), px(bar), px(fixedH), 1.5)}
                />
              )}
            </g>
          );
        })}
      </g>
      {lastP.gate && (
        <circle
          className={`viz-spark-gate viz-spark-gate-${lastP.gate}`}
          cx={width - 4}
          cy={px(base)}
          r={3}
        />
      )}
    </svg>
  );
}
