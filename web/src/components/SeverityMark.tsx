// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import type { ReactNode } from 'react';
import type { SeverityKey } from './severityModel';

/*
 * Shapes that carry severity and status next to colour, so nothing depends on
 * colour alone: critical a filled diamond, high a filled triangle, medium a
 * filled circle, low a hollow circle, unknown a dashed circle; ok a check,
 * fail a cross, warn an exclamation mark. Decorative: the word beside them
 * names the level.
 */

const shapes: Record<SeverityKey, ReactNode> = {
  critical: <path d="M8 1.2 14.8 8 8 14.8 1.2 8Z" fill="currentColor" />,
  high: <path d="M8 1.8 15 14.2H1Z" fill="currentColor" strokeLinejoin="round" />,
  medium: <circle cx="8" cy="8" r="5.6" fill="currentColor" />,
  low: <circle cx="8" cy="8" r="5" fill="none" stroke="currentColor" strokeWidth="2.2" />,
  unknown: (
    <circle
      cx="8"
      cy="8"
      r="5.2"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeDasharray="2.6 2.2"
    />
  ),
};

/** The shape of one severity level, in its colour. */
export function SeverityMark({
  severity,
  size = 12,
  className = '',
}: {
  severity: SeverityKey;
  size?: number;
  className?: string;
}) {
  return (
    <svg
      viewBox="0 0 16 16"
      width={size}
      height={size}
      aria-hidden="true"
      focusable="false"
      data-severity={severity}
      className={`tone-${severity} tone-fg shrink-0 ${className}`}
    >
      {shapes[severity]}
    </svg>
  );
}

export type StatusKind = 'ok' | 'fail' | 'warn' | 'none';

const statusTone: Record<StatusKind, string> = {
  ok: 'tone-ok',
  fail: 'tone-fail',
  warn: 'tone-warn',
  none: 'tone-neutral',
};

/** A status in a small disc: check (ok), cross (fail), exclamation (warn), dashed ring (none). */
export function StatusMark({
  status,
  size = 14,
  className = '',
}: {
  status: StatusKind;
  size?: number;
  className?: string;
}) {
  return (
    <svg
      viewBox="0 0 16 16"
      width={size}
      height={size}
      aria-hidden="true"
      focusable="false"
      data-status={status}
      className={`${statusTone[status]} tone-fg shrink-0 ${className}`}
    >
      {status === 'none' ? (
        <circle
          cx="8"
          cy="8"
          r="6.2"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.6"
          strokeDasharray="2.4 2.2"
        />
      ) : (
        <>
          <circle cx="8" cy="8" r="7.2" fill="currentColor" />
          <g
            fill="none"
            stroke="var(--surface)"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
          >
            {status === 'ok' ? <path d="m4.9 8.2 2.1 2.1 4.1-4.4" /> : null}
            {status === 'fail' ? <path d="m5.6 5.6 4.8 4.8m0-4.8-4.8 4.8" /> : null}
            {status === 'warn' ? <path d="M8 4.6v4.2m0 2.6v.1" /> : null}
          </g>
        </>
      )}
    </svg>
  );
}
