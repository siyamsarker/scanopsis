// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useRef, type ReactNode } from 'react';
import { Link, type To } from 'react-router';
import { formatInt } from '../format';
import { useCssom } from './cssom';
import { SeverityMark } from './SeverityMark';
import {
  SEVERITY_LABEL,
  SEVERITY_ORDER,
  severityKey,
  summarizeCounts,
  type SeverityCountValues,
  type SeverityInput,
  type SeverityKey,
} from './severityModel';

/** Optional destination per severity level, e.g. the findings list filtered to that level. */
export type SeverityLinks = Partial<Record<SeverityKey, To>>;

/**
 * A severity as shape, colour and word together: a status pill with the
 * level's shape (diamond, triangle, filled circle, hollow circle, dashed
 * circle), so colour is never the only carrier. Pass `count` to append a number.
 */
export function SeverityBadge({
  severity,
  count,
  className = '',
}: {
  severity: SeverityInput;
  count?: number;
  className?: string;
}) {
  const key = severityKey(severity);
  return (
    <span className={`chip tone-${key} ${className}`}>
      <SeverityMark severity={key} size={11} />
      {SEVERITY_LABEL[key]}
      {count !== undefined ? (
        <span className="font-semibold tabular-nums">{formatInt(count)}</span>
      ) : null}
    </span>
  );
}

function Segment({
  value,
  tone,
  label,
  to,
}: {
  value: number;
  tone: SeverityKey;
  label: string;
  to?: To;
}) {
  const ref = useRef<HTMLElement>(null);
  useCssom(ref, { flexGrow: value, flexBasis: 0 });
  const className = `tone-${tone} tone-bg block h-full min-w-1 transition-[filter] duration-micro ease-enter ${to !== undefined ? 'hover:brightness-110' : ''}`;
  if (to !== undefined) {
    return (
      <Link
        ref={ref as React.Ref<HTMLAnchorElement>}
        to={to}
        aria-label={label}
        title={label}
        className={className}
      />
    );
  }
  return <span ref={ref} title={label} className={className} />;
}

/**
 * A precise segmented bar: one segment per non-zero severity, widths in
 * proportion to the counts, fixed order critical to unknown. Zero levels are
 * omitted. The summary ("2 critical, 44 high") is real text for screen readers
 * and `showSummary` also prints it. With `links`, segments become links.
 */
export function SeverityBar({
  counts,
  links,
  size = 'md',
  showSummary = false,
  className = '',
}: {
  counts: SeverityCountValues;
  links?: SeverityLinks;
  size?: 'sm' | 'md' | 'lg';
  /** Print the text summary under the bar (it is always available to screen readers). */
  showSummary?: boolean;
  className?: string;
}) {
  const summary = summarizeCounts(counts);
  const visible = SEVERITY_ORDER.filter((key) => counts[key] > 0);
  const height = { sm: 'h-1.5', md: 'h-2.5', lg: 'h-4' }[size];
  const linked = links !== undefined && visible.some((key) => links[key] !== undefined);

  return (
    <div className={className}>
      <div
        role={linked ? 'group' : 'img'}
        aria-label={`Severity breakdown: ${summary}`}
        className={`flex w-full gap-0.5 overflow-hidden rounded-full bg-line ${height}`}
      >
        {visible.map((key) => (
          <Segment
            key={key}
            value={counts[key]}
            tone={key}
            to={links?.[key]}
            label={`${formatInt(counts[key])} ${key}`}
          />
        ))}
      </div>
      {showSummary ? (
        <p className="mt-2 text-body-s text-text-muted tabular-nums">{summary}</p>
      ) : null}
    </div>
  );
}

/**
 * A compact legend of all five levels with their counts, in fixed order. Each
 * entry has a coloured marker and the level name. With `links`, entries are links;
 * `hideZero` leaves out levels without findings.
 */
export function SeverityCounts({
  counts,
  links,
  hideZero = false,
  layout = 'inline',
  className = '',
}: {
  counts: SeverityCountValues;
  links?: SeverityLinks;
  hideZero?: boolean;
  /** "inline" wraps in a row; "stack" is one level per line with counts right-aligned. */
  layout?: 'inline' | 'stack';
  className?: string;
}) {
  const keys = SEVERITY_ORDER.filter((key) => !hideZero || counts[key] > 0);
  const stack = layout === 'stack';
  if (keys.length === 0) return <p className={`text-text-muted ${className}`}>No findings</p>;
  return (
    <ul
      aria-label={`Findings by severity: ${summarizeCounts(counts)}`}
      className={`flex ${stack ? 'flex-col gap-1' : 'flex-wrap gap-x-3.5 gap-y-1'} ${className}`}
    >
      {keys.map((key) => {
        const body: ReactNode = (
          <>
            <SeverityMark
              severity={key}
              size={10}
              className={counts[key] === 0 ? 'opacity-45' : ''}
            />
            <span className={counts[key] === 0 ? 'text-text-faint' : 'text-text-muted'}>
              {SEVERITY_LABEL[key]}
            </span>
            <span
              className={`tabular-nums ${stack ? 'ml-auto' : ''} ${counts[key] === 0 ? 'text-text-faint' : 'font-semibold text-text'}`}
            >
              {formatInt(counts[key])}
            </span>
          </>
        );
        const row = 'flex items-center gap-1.5 text-body-s';
        const to = links?.[key];
        return (
          <li key={key}>
            {to !== undefined ? (
              <Link
                to={to}
                className={`${row} -mx-1.5 rounded-[6px] px-1.5 transition-colors duration-micro hover:bg-wash`}
              >
                {body}
              </Link>
            ) : (
              <span className={row}>{body}</span>
            )}
          </li>
        );
      })}
    </ul>
  );
}
