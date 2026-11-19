// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { Clock, TriangleAlert } from 'lucide-react';
import { useEffect, useState } from 'react';
import { Link, type To } from 'react-router';
import { formatExactTime, relativeTime } from '../format';

/** A link either inside the app (`to`) or out to GitLab (`href`, opens in a new tab). */
export interface LinkTarget {
  label: string;
  to?: To;
  href?: string;
}

export interface FreshnessProps {
  /** When the data was produced or received. */
  at: string | Date;
  /** Text before the time: "Updated 3 minutes ago". Defaults to "Updated". */
  prefix?: string;
  /** After this many milliseconds the data counts as stale (warn state). Off by default. */
  staleAfterMs?: number;
  /** Optional link to the pipeline or commit that produced the data. */
  link?: LinkTarget;
  /** Fixed clock for tests and snapshots; otherwise the component re-reads the time every 30 s. */
  now?: Date;
  className?: string;
}

function useNow(fixed: Date | undefined): Date {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    if (fixed) return;
    const id = window.setInterval(() => setNow(new Date()), 30_000);
    return () => window.clearInterval(id);
  }, [fixed]);
  return fixed ?? now;
}

/** Renders a LinkTarget as a router link or an external anchor. */
export function TargetLink({ target, className }: { target: LinkTarget; className?: string }) {
  if (target.to !== undefined) {
    return (
      <Link to={target.to} className={className}>
        {target.label}
      </Link>
    );
  }
  return (
    <a href={target.href} target="_blank" rel="noopener noreferrer" className={className}>
      {target.label}
    </a>
  );
}

/**
 * How recent the data is: a relative time ("3 minutes ago") with the exact time
 * in the tooltip and in a machine-readable <time>. Past `staleAfterMs` it shows a
 * warning icon and the word "Stale", so the state never relies on colour.
 */
export function Freshness({
  at,
  prefix = 'Updated',
  staleAfterMs,
  link,
  now,
  className = '',
}: FreshnessProps) {
  const current = useNow(now);
  const date = new Date(at);
  const stale = staleAfterMs !== undefined && current.getTime() - date.getTime() > staleAfterMs;
  const Icon = stale ? TriangleAlert : Clock;

  return (
    <span
      className={`inline-flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-text-muted ${className}`}
    >
      <Icon
        size={14}
        strokeWidth={1.5}
        aria-hidden="true"
        className={stale ? 'text-warn' : undefined}
      />
      <span>
        {prefix}{' '}
        <time dateTime={date.toISOString()} title={formatExactTime(date)} className="text-text">
          {relativeTime(date, current)}
        </time>
      </span>
      {stale ? <span className="chip tone-warn">Stale</span> : null}
      {link ? (
        <TargetLink
          target={link}
          className="font-mono text-text underline decoration-border-strong underline-offset-2 hover:decoration-text"
        />
      ) : null}
    </span>
  );
}
