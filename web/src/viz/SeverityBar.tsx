// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { scaleSqrt } from 'd3-scale';
import { useRef, type ReactNode, type RefObject } from 'react';
import { Legend, severityLegend } from './frame';
import { useStyles, useWidth } from './hooks';
import {
  countOf,
  describeCounts,
  formatCount,
  SEVERITIES,
  SEVERITY_NAME,
  totalCount,
  type Severity,
  type SeverityCounts,
} from './severity';
import './viz.css';

export type SeverityBarSize = 'sm' | 'md' | 'lg';

export interface SeverityBarProps {
  counts: SeverityCounts | null | undefined;
  /** sm 6 px and md 10 px are thin bars without text; lg is 28 px with counts inside where they fit. */
  size?: SeverityBarSize;
  /** A link per segment, e.g. to the findings filtered by that severity. */
  hrefFor?: (severity: Severity) => string | undefined;
  /** What the bar counts, for the accessible name. */
  label?: string;
  /** Scale the bar against this total instead of filling the width, to compare rows. */
  max?: number;
  /** Glyph legend under the bar; on by default for lg. */
  legend?: boolean;
  className?: string;
}

/** Rough rendered width of a count label at 12 px, plus padding. */
function labelWidth(text: string) {
  return text.length * 7.2 + 12;
}

function Grow({
  grow,
  className,
  children,
  href,
  ariaLabel,
  tip,
}: {
  grow: number;
  className: string;
  children?: ReactNode;
  href?: string;
  ariaLabel?: string;
  tip?: string;
}) {
  const ref = useRef<HTMLElement | null>(null);
  useStyles(ref as RefObject<HTMLElement>, { flexGrow: grow });
  if (href) {
    return (
      <a
        ref={(el) => {
          ref.current = el;
        }}
        href={href}
        className={className}
        aria-label={ariaLabel}
        data-tip={tip}
      >
        {children}
      </a>
    );
  }
  return (
    <span
      ref={(el) => {
        ref.current = el;
      }}
      className={className}
      data-tip={tip}
      aria-hidden="true"
    >
      {children}
    </span>
  );
}

/**
 * One whole split into severities, most severe on the left. Each segment can
 * link to its findings. Counts print inside the large bar only where they fit.
 */
export function SeverityBar({
  counts,
  size = 'md',
  hrefFor,
  label = 'Open findings',
  max,
  legend,
  className = '',
}: SeverityBarProps) {
  const [box, width] = useWidth<HTMLDivElement>(320);
  const total = totalCount(counts);
  const denominator = Math.max(total, max ?? 0, 1);
  const present = SEVERITIES.filter((s) => countOf(counts, s) > 0);
  const showLegend = legend ?? size === 'lg';
  const summary = `${label}: ${total === 0 ? 'none' : describeCounts(counts)}`;
  const linked = Boolean(hrefFor) && total > 0;
  const gaps = Math.max(0, present.length - 1) * 2;

  return (
    <div className={`viz-sevbar-wrap ${className}`}>
      <div
        ref={box}
        className={`viz-sevbar viz-sevbar-${size}`}
        data-empty={total === 0 || undefined}
        role={linked ? 'group' : 'img'}
        aria-label={summary}
      >
        {present.map((s) => {
          const c = countOf(counts, s);
          const text = formatCount(c, true);
          const fits = size === 'lg' && ((width - gaps) * c) / denominator >= labelWidth(text);
          const tip = `${formatCount(c)} ${SEVERITY_NAME[s].toLowerCase()}`;
          return (
            <Grow
              key={s}
              grow={c}
              className={`viz-seg viz-bg-${s}`}
              href={hrefFor?.(s)}
              ariaLabel={`${tip}, ${Math.round((c / Math.max(total, 1)) * 100)}% of ${label.toLowerCase()}`}
              tip={tip}
            >
              {fits ? <span className="viz-seg-label">{text}</span> : null}
            </Grow>
          );
        })}
        {max != null && max > total && <Grow grow={max - total} className="viz-seg-rest" />}
        {total === 0 && size === 'lg' && <span className="viz-sevbar-none">No open findings</span>}
      </div>
      {showLegend && present.length > 0 && <Legend items={severityLegend(present)} />}
    </div>
  );
}

export interface DistributionMiniProps {
  counts: SeverityCounts | null | undefined;
  /** Shared maximum so several rows compare; defaults to this row's largest count. */
  max?: number;
  /** Print the total after the columns. */
  showTotal?: boolean;
  label?: string;
  className?: string;
}

/**
 * Five micro columns, one per severity in fixed order, square-root scaled so
 * small counts stay visible next to large ones. Sized for table cells.
 */
export function DistributionMini({
  counts,
  max,
  showTotal = false,
  label = 'Open findings',
  className = '',
}: DistributionMiniProps) {
  const total = totalCount(counts);
  const top = Math.max(1, max ?? Math.max(...SEVERITIES.map((s) => countOf(counts, s))));
  const h = 16;
  const w = 4;
  const gap = 2;
  const y = scaleSqrt().domain([0, top]).range([0, h]).clamp(true);
  return (
    <span className={`viz-mini ${className}`}>
      <svg
        width={SEVERITIES.length * (w + gap) - gap}
        height={h}
        viewBox={`0 0 ${SEVERITIES.length * (w + gap) - gap} ${h}`}
        role="img"
        aria-label={`${label}: ${total === 0 ? 'none' : describeCounts(counts)}`}
      >
        {SEVERITIES.map((s, i) => {
          const c = countOf(counts, s);
          const bh = c > 0 ? Math.max(2, y(c)) : 1;
          return (
            <rect
              key={s}
              className={c > 0 ? `viz-bar viz-sev-${s}` : 'viz-mini-zero'}
              x={i * (w + gap)}
              y={h - bh}
              width={w}
              height={bh}
              rx={c > 0 ? 1 : 0}
            />
          );
        })}
      </svg>
      {showTotal && <span className="viz-mini-total">{formatCount(total, true)}</span>}
    </span>
  );
}
