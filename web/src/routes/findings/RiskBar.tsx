// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import type { SeverityCountValues } from '../../components/severityModel';
import { RISK_WEIGHTS, SEVERITIES } from '../../viz';

const FILL = {
  critical: 'fill-sev-critical',
  high: 'fill-sev-high',
  medium: 'fill-sev-medium',
  low: 'fill-sev-low',
  unknown: 'fill-sev-unknown',
} as const;

/**
 * Risk points as a bar: one segment per severity, as wide as its points
 * (count × weight), on a track scaled to `max` points. Decorative; the
 * caller prints the number beside it.
 */
export function RiskBar({
  counts,
  max,
  className = 'h-2',
  muted = false,
}: {
  counts: SeverityCountValues;
  max: number;
  className?: string;
  /** Draws the segments faded, for what is left after a plan. */
  muted?: boolean;
}) {
  const scale = 1000 / Math.max(1, max);
  const segments: { s: (typeof SEVERITIES)[number]; x: number; w: number }[] = [];
  for (const s of SEVERITIES) {
    const w = counts[s] * RISK_WEIGHTS[s] * scale;
    const x = segments.reduce((end, seg) => end + seg.w, 0);
    if (w > 0) segments.push({ s, x, w });
  }
  return (
    <svg
      viewBox="0 0 1000 10"
      preserveAspectRatio="none"
      aria-hidden="true"
      focusable="false"
      className={`block w-full overflow-hidden rounded-full bg-line/60 ${className}`}
    >
      {segments.map((seg, i) => (
        <rect
          key={seg.s}
          x={seg.x}
          y={0}
          width={Math.max(2, seg.w - (i < segments.length - 1 ? 3 : 0))}
          height={10}
          className={`${FILL[seg.s]} ${muted ? 'opacity-35' : ''}`}
        />
      ))}
    </svg>
  );
}
