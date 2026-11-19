// Copyright 2026 The Scanopsis Authors
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
  /** Which direction is an improvement; sets the arrow colour only, the sign is always written. */
  better?: 'down' | 'up';
}

export type StatTone = SeverityKey | 'pass' | 'warn' | 'fail' | 'brand';

function Delta({ delta }: { delta: StatDelta }) {
  const { value, better = 'down' } = delta;
  const Icon = value === 0 ? Minus : value > 0 ? ArrowUp : ArrowDown;
  const good = value !== 0 && value < 0 === (better === 'down');
  const tone = value === 0 ? 'text-text-muted' : good ? 'text-pass' : 'text-sev-high';
  return (
    <span className="inline-flex items-center gap-1 whitespace-nowrap text-sm text-text-muted">
      <Icon size={14} strokeWidth={1.5} aria-hidden="true" className={tone} />
      <span className="font-medium tabular-nums text-text">
        {value > 0 ? '+' : value < 0 ? '−' : ''}
        {formatInt(Math.abs(value))}
      </span>
      {delta.label ? <span>{delta.label}</span> : null}
    </span>
  );
}

/**
 * The most useful number first. A label, the value in large tabular figures,
 * an optional hint line and delta. With `to` the whole tile is one link, so
 * every number leads to what it summarises. `tone` draws a thin accent along
 * the top edge; the label still names the meaning.
 */
export function StatTile({
  label,
  value,
  hint,
  delta,
  to,
  tone,
  className = '',
}: {
  label: string;
  /** A formatted string or a number (formatted with thousands separators). */
  value: ReactNode;
  hint?: ReactNode;
  delta?: StatDelta;
  to?: To;
  tone?: StatTone;
  className?: string;
}) {
  const shown = typeof value === 'number' ? formatInt(value) : value;
  const classes = `group relative block min-h-28 overflow-hidden rounded-card border border-border bg-surface p-4 shadow-[inset_0_1px_0_color-mix(in_srgb,var(--text)_5%,transparent)] ${
    to !== undefined
      ? 'transition-[border-color,transform] duration-150 ease-out hover:border-border-strong'
      : ''
  } ${className}`;
  const body = (
    <>
      {tone ? (
        <span
          aria-hidden="true"
          className={`tone-${tone} tone-bg absolute inset-x-4 top-0 h-0.5 rounded-b-full`}
        />
      ) : null}
      <span className="flex items-start justify-between gap-2">
        <span className="text-sm font-medium text-text-muted">{label}</span>
        {to !== undefined ? (
          <ArrowUpRight
            size={16}
            strokeWidth={1.5}
            aria-hidden="true"
            className="shrink-0 text-text-muted opacity-0 transition-opacity duration-150 ease-out group-hover:opacity-100 group-focus-visible:opacity-100"
          />
        ) : null}
      </span>
      <span className="mt-1 block text-3xl font-semibold leading-9 tracking-tight text-text tabular-nums">
        {shown}
      </span>
      {hint || delta ? (
        <span className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-sm text-text-muted">
          {delta ? <Delta delta={delta} /> : null}
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
