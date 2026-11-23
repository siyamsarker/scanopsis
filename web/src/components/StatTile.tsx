// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { ArrowDown, ArrowUp, ArrowUpRight, Minus } from 'lucide-react';
import type { ReactNode } from 'react';
import { Link, type To } from 'react-router';
import { formatInt } from '../format';
import type { SeverityKey } from './severityModel';

export interface StatDelta {
  /** Change since the comparison point; the sign sets the arrow. */
  value: number;
  /** What it is compared with: "since the last scan". */
  label?: string;
  /** Which direction is an improvement; sets the colour only, the sign is always written. */
  better?: 'down' | 'up';
}

export type StatTone = SeverityKey | 'ok' | 'pass' | 'warn' | 'fail' | 'brand';

function DeltaText({ delta }: { delta: StatDelta }) {
  const { value, better = 'down' } = delta;
  const Icon = value === 0 ? Minus : value > 0 ? ArrowUp : ArrowDown;
  const good = value !== 0 && value < 0 === (better === 'down');
  const tone = value === 0 ? 'tone-neutral' : good ? 'tone-ok' : 'tone-fail';
  return (
    <span className="inline-flex flex-wrap items-center gap-x-1 text-body-s text-text-muted">
      <span
        className={`${tone} tone-ink inline-flex items-center gap-0.5 font-semibold whitespace-nowrap tabular-nums`}
      >
        <Icon size={14} strokeWidth={2} aria-hidden="true" />
        {value > 0 ? '+' : value < 0 ? '−' : ''}
        {formatInt(Math.abs(value))}
      </span>
      {delta.label ? <span>{delta.label}</span> : null}
    </span>
  );
}

/**
 * The most useful number first: a label, the value in large tabular figures,
 * then an optional delta and hint line. `trend` takes a small chart (a
 * sparkline) shown beside the value. With `to` the whole tile is one link, so
 * every number leads to what it summarises. `tone` marks the label with a
 * small bar in that colour; the label still names the meaning.
 */
export function StatTile({
  label,
  value,
  hint,
  delta,
  trend,
  to,
  tone,
  className = '',
}: {
  label: string;
  /** A formatted string or a number (formatted with thousands separators). */
  value: ReactNode;
  hint?: ReactNode;
  delta?: StatDelta;
  /** A compact chart (sparkline, pipeline spark) next to the value; decorative or self-labelled. */
  trend?: ReactNode;
  to?: To;
  tone?: StatTone;
  className?: string;
}) {
  const shown = typeof value === 'number' ? formatInt(value) : value;
  const toneClass = tone === 'pass' ? 'ok' : tone;
  const classes = `group relative flex min-h-30 flex-col rounded-panel border border-line bg-surface p-5 shadow-1 ${
    to !== undefined
      ? 'transition-[border-color,background-color,translate] duration-micro ease-enter hover:-translate-y-px hover:border-line-strong hover:bg-surface-raised active:translate-y-0'
      : ''
  } ${className}`;
  const body = (
    <>
      <span className="flex items-start justify-between gap-2">
        <span className="flex min-w-0 items-center gap-2 text-body-s font-medium text-text-muted">
          {toneClass ? (
            <span
              aria-hidden="true"
              className={`tone-${toneClass} tone-bg h-3.5 w-1 shrink-0 rounded-full`}
            />
          ) : null}
          <span className="min-w-0">{label}</span>
        </span>
        {to !== undefined ? (
          <ArrowUpRight
            size={16}
            strokeWidth={1.75}
            aria-hidden="true"
            className="shrink-0 text-text-faint opacity-0 transition-opacity duration-micro group-hover:opacity-100 group-focus-visible:opacity-100"
          />
        ) : null}
      </span>
      <span className="mt-auto flex items-end justify-between gap-3 pt-3">
        <span className="block text-display-s text-text tabular-nums">{shown}</span>
        {trend ? <span className="mb-1.5 min-w-0 shrink text-text-muted">{trend}</span> : null}
      </span>
      {hint || delta ? (
        <span className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-body-s text-text-muted">
          {delta ? <DeltaText delta={delta} /> : null}
          {hint ? <span>{hint}</span> : null}
        </span>
      ) : null}
    </>
  );
  return to !== undefined ? (
    <Link to={to} className={classes}>
      {body}
    </Link>
  ) : (
    <div className={classes}>{body}</div>
  );
}
