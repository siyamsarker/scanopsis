// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { SeverityMark } from '../../components/SeverityMark';
import type { SeverityCountValues } from '../../components/severityModel';
import { formatInt } from '../../format';
import { SEVERITIES, SEVERITY_NAME, SrTable } from '../../viz';

const FILL = {
  critical: 'fill-sev-critical',
  high: 'fill-sev-high',
  medium: 'fill-sev-medium',
  low: 'fill-sev-low',
  unknown: 'fill-sev-unknown',
} as const;

const GRID = 'grid grid-cols-[5.25rem_2.75rem_minmax(0,1fr)_2.75rem] items-center gap-x-2';

/**
 * Before and after as a diverging bar per severity: fixed findings grow to
 * the left of the axis (faded: they are gone), new findings to the right in
 * full colour. Both sides share one scale, so the longer side reads first.
 */
export function DiffChart({
  added,
  fixed,
}: {
  added: SeverityCountValues;
  fixed: SeverityCountValues;
}) {
  const max = Math.max(1, ...SEVERITIES.map((s) => Math.max(added[s], fixed[s])));
  const rows = SEVERITIES.filter((s) => added[s] > 0 || fixed[s] > 0);
  const shown = rows.length > 0 ? rows : (['critical', 'high'] as const);
  const sum = (c: SeverityCountValues) => SEVERITIES.reduce((n, s) => n + c[s], 0);
  return (
    <figure className="min-w-0">
      <div className={`${GRID} mb-2 text-caption text-text-muted`} aria-hidden="true">
        <span />
        <span />
        <span className="flex justify-between">
          <span>Fixed</span>
          <span>New</span>
        </span>
        <span />
      </div>
      <div className="space-y-1" aria-hidden="true">
        {shown.map((s) => {
          const f = (fixed[s] / max) * 480;
          const n = (added[s] / max) * 480;
          return (
            <div key={s} className={GRID}>
              <span className="inline-flex items-center gap-1.5 text-small text-text-muted">
                <SeverityMark severity={s} size={10} />
                {SEVERITY_NAME[s]}
              </span>
              <span
                className={`text-right text-small tabular-nums ${fixed[s] ? 'text-text' : 'text-text-faint'}`}
              >
                {fixed[s] ? `−${formatInt(fixed[s])}` : '0'}
              </span>
              <svg
                viewBox="0 0 1000 20"
                preserveAspectRatio="none"
                className="block h-6 w-full overflow-visible"
              >
                <rect x={0} y={9} width={1000} height={2} className="fill-line opacity-40" />
                {fixed[s] > 0 ? (
                  <rect
                    x={498 - Math.max(6, f)}
                    y={2}
                    width={Math.max(6, f)}
                    height={16}
                    className={`${FILL[s]} opacity-40`}
                  />
                ) : null}
                {added[s] > 0 ? (
                  <rect x={502} y={2} width={Math.max(6, n)} height={16} className={FILL[s]} />
                ) : null}
                <rect x={499} y={-2} width={2} height={24} className="fill-line-strong" />
              </svg>
              <span
                className={`text-small tabular-nums ${added[s] ? 'text-text' : 'text-text-faint'}`}
              >
                {added[s] ? `+${formatInt(added[s])}` : '0'}
              </span>
            </div>
          );
        })}
      </div>
      <div
        className={`${GRID} mt-2 border-t border-line pt-2 text-small tabular-nums`}
        aria-hidden="true"
      >
        <span className="text-text-muted">Total</span>
        <span className="text-right font-semibold text-text">
          {sum(fixed) ? `−${formatInt(sum(fixed))}` : '0'}
        </span>
        <span />
        <span className="font-semibold text-text">
          {sum(added) ? `+${formatInt(sum(added))}` : '0'}
        </span>
      </div>
      <SrTable
        caption="New and fixed findings by severity"
        columns={['Severity', 'Fixed', 'New']}
        rows={SEVERITIES.map((s) => [SEVERITY_NAME[s], formatInt(fixed[s]), formatInt(added[s])])}
      />
    </figure>
  );
}
