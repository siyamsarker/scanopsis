// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import { GateGlyph, SeverityGlyph } from './frame';
import { nextIndex, useStyles } from './hooks';
import { riskPoints } from './risk';
import {
  countOf,
  formatCount,
  GATE_NAME,
  SEVERITIES,
  SEVERITY_NAME,
  type GateState,
  type Severity,
  type SeverityCounts,
} from './severity';
import { formatAge, formatDateTime, toTime, type TimeInput } from './time';
import './viz.css';

export interface HeatRow {
  id: string | number;
  name: string;
  /** Open findings; null when the service has no scan yet. */
  counts: SeverityCounts | null | undefined;
  gate?: GateState;
  /** Time of the latest scan. */
  scannedAt?: TimeInput | null;
  /** Secondary line, e.g. the GitLab project path. */
  path?: string;
  /** Makes the service name a link. */
  href?: string;
}

export interface HeatStripProps {
  rows: readonly HeatRow[];
  /** Row click, Enter or Space. Without it rows are not focusable. */
  onSelect?: (row: HeatRow) => void;
  /** Reference time for freshness; defaults to now. */
  now?: TimeInput;
  /** A scan older than this many days is marked stale. */
  staleAfterDays?: number;
  /** Scroll inside this height with the header kept in view. */
  maxHeight?: number;
  caption?: string;
  /** Render the service name cell differently, e.g. as a router link. */
  renderName?: (row: HeatRow) => ReactNode;
  /** Shown when there are no rows. */
  emptyMessage?: ReactNode;
  className?: string;
}

export interface RankedRow {
  row: HeatRow;
  risk: number;
}

/** Highest risk first; ties by name. Services without a scan go last. */
export function rankRows(rows: readonly HeatRow[]): RankedRow[] {
  return rows
    .map((row) => ({ row, risk: row.counts == null ? -1 : riskPoints(row.counts) }))
    .sort((a, b) => b.risk - a.risk || a.row.name.localeCompare(b.row.name))
    .map((r) => ({ row: r.row, risk: Math.max(0, r.risk) }));
}

/** A column whose largest count is below this is scaled as if it reached it. */
export const HEAT_FLOOR = 10;

/**
 * Intensity level 0 to 5 of a count against its column's maximum, on a square
 * root scale so a handful of findings is still visible next to hundreds. The
 * floor keeps a column of threes from looking as hot as a column of hundreds.
 */
export function heatLevel(count: number, columnMax: number): number {
  if (count <= 0) return 0;
  const top = Math.max(columnMax, HEAT_FLOOR);
  return Math.min(5, Math.max(1, Math.ceil(Math.sqrt(count / top) * 5)));
}

function Scroller({ maxHeight, children }: { maxHeight?: number; children: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null);
  useStyles(ref, { maxHeight: maxHeight ? `${maxHeight}px` : null });
  return (
    <div ref={ref} className="viz-heat-scroll" data-bounded={maxHeight ? true : undefined}>
      {children}
    </div>
  );
}

function RiskMeter({ share }: { share: number }) {
  const ref = useRef<HTMLSpanElement>(null);
  useStyles(ref, { transform: `scaleX(${Math.max(0.02, share)})` });
  return (
    <span className="viz-heat-meter" aria-hidden="true">
      <span ref={ref} />
    </span>
  );
}

/**
 * The portfolio matrix: services as rows ranked by risk, severities as cells
 * whose intensity grows with the count (per severity hue), then the quality
 * gate and how fresh the latest scan is. Rows open the service.
 */
export function HeatStrip({
  rows,
  onSelect,
  now,
  staleAfterDays = 7,
  maxHeight,
  caption = 'Services ranked by risk',
  renderName,
  emptyMessage = 'No services yet. Add a service to see it here.',
  className = '',
}: HeatStripProps) {
  const ranked = useMemo(() => rankRows(rows), [rows]);
  const columnMax = useMemo(() => {
    const out = {} as Record<Severity, number>;
    for (const s of SEVERITIES)
      out[s] = Math.max(0, ...ranked.map((r) => countOf(r.row.counts, s)));
    return out;
  }, [ranked]);
  const maxRisk = ranked[0]?.risk ?? 0;
  const [mountedAt] = useState(() => Date.now());
  const reference = now != null ? toTime(now) : mountedAt;
  const staleMs = staleAfterDays * 86_400_000;
  const body = useRef<HTMLTableSectionElement>(null);
  const interactive = Boolean(onSelect);
  const [focusIndex, setFocusIndex] = useState(0);

  if (ranked.length === 0) {
    return (
      <div className={`viz-heat viz-heat-empty ${className}`} role="note">
        <p>{emptyMessage}</p>
      </div>
    );
  }

  const focusRow = (index: number) => {
    setFocusIndex(index);
    body.current?.rows[index]?.focus();
  };

  const onKeyDown = (index: number) => (e: KeyboardEvent<HTMLTableRowElement>) => {
    if (e.target !== e.currentTarget) return;
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      onSelect?.(ranked[index].row);
      return;
    }
    if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') return;
    const next = nextIndex(e.key, index, ranked.length);
    if (next === null) return;
    e.preventDefault();
    focusRow(next);
  };

  return (
    <div className={`viz-heat ${className}`}>
      <Scroller maxHeight={maxHeight}>
        <table className="viz-heat-table">
          <caption className="viz-sr">
            {caption}
            {interactive
              ? '. Use the arrow keys to move between rows and Enter to open a service.'
              : ''}
          </caption>
          <thead>
            <tr>
              <th scope="col" className="viz-heat-rank">
                <span className="viz-sr">Rank</span>
              </th>
              <th scope="col" className="viz-heat-name">
                Service
              </th>
              <th scope="col" className="viz-heat-risk">
                Risk
              </th>
              {SEVERITIES.map((s) => (
                <th key={s} scope="col" className="viz-heat-sev">
                  <span className="viz-heat-head">
                    <SeverityGlyph severity={s} />
                    <span className="viz-heat-head-text">{SEVERITY_NAME[s]}</span>
                  </span>
                </th>
              ))}
              <th scope="col" className="viz-heat-gate">
                Gate
              </th>
              <th scope="col" className="viz-heat-fresh">
                Scanned
              </th>
            </tr>
          </thead>
          <tbody ref={body}>
            {ranked.map(({ row, risk }, i) => {
              const scanned = toTime(row.scannedAt);
              const stale = Number.isFinite(scanned) && reference - scanned > staleMs;
              const noScan = row.counts == null;
              return (
                <tr
                  key={row.id}
                  className="viz-heat-row"
                  data-interactive={interactive || undefined}
                  tabIndex={
                    interactive
                      ? i === Math.min(focusIndex, ranked.length - 1)
                        ? 0
                        : -1
                      : undefined
                  }
                  onFocus={interactive ? () => setFocusIndex(i) : undefined}
                  onClick={interactive ? () => onSelect?.(row) : undefined}
                  onKeyDown={interactive ? onKeyDown(i) : undefined}
                  aria-label={
                    interactive
                      ? `${row.name}, risk ${noScan ? 'not scanned' : risk}${row.gate ? `, ${GATE_NAME[row.gate].toLowerCase()}` : ''}`
                      : undefined
                  }
                >
                  <td className="viz-heat-rank">{i + 1}</td>
                  <th scope="row" className="viz-heat-name">
                    {renderName ? (
                      renderName(row)
                    ) : row.href ? (
                      <a href={row.href} tabIndex={interactive ? -1 : undefined}>
                        {row.name}
                      </a>
                    ) : (
                      <span>{row.name}</span>
                    )}
                    {row.path && <span className="viz-heat-path">{row.path}</span>}
                  </th>
                  <td className="viz-heat-risk">
                    {noScan ? (
                      <span className="viz-faint">–</span>
                    ) : (
                      <>
                        <span className="viz-heat-risk-num">{formatCount(risk, true)}</span>
                        <RiskMeter share={maxRisk > 0 ? risk / maxRisk : 0} />
                      </>
                    )}
                  </td>
                  {SEVERITIES.map((s) => {
                    const c = countOf(row.counts, s);
                    const level = noScan ? 0 : heatLevel(c, columnMax[s]);
                    return (
                      <td key={s} className="viz-heat-cell">
                        <span className={`viz-heat-fill viz-tint-${s}`} data-level={level}>
                          {noScan ? '–' : formatCount(c, true)}
                          <span className="viz-sr"> {SEVERITY_NAME[s].toLowerCase()}</span>
                        </span>
                      </td>
                    );
                  })}
                  <td className="viz-heat-gate">
                    {row.gate ? (
                      <span className="viz-gate-label">
                        <GateGlyph gate={row.gate} />
                        <span>
                          {row.gate === 'pass' ? 'Passed' : row.gate === 'fail' ? 'Failed' : 'None'}
                        </span>
                      </span>
                    ) : (
                      <span className="viz-faint">–</span>
                    )}
                  </td>
                  <td className="viz-heat-fresh" data-stale={stale || undefined}>
                    {Number.isFinite(scanned) ? (
                      <time
                        dateTime={new Date(scanned).toISOString()}
                        title={formatDateTime(scanned)}
                      >
                        {stale && (
                          <span className="viz-stale-mark" aria-hidden="true">
                            !
                          </span>
                        )}
                        {formatAge(scanned, reference)}
                        {stale && <span className="viz-sr">, stale</span>}
                      </time>
                    ) : (
                      <span className="viz-faint">Never</span>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </Scroller>
    </div>
  );
}
