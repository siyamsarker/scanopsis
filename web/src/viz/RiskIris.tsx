// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { arc } from 'd3-shape';
import {
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactNode,
} from 'react';
import { GateGlyph, TipRow, SeverityGlyph } from './frame';
import { useCountUp, useReducedMotion } from './hooks';
import { explainRisk, RISK_SOURCE, riskPoints, riskTerms, type RiskTerm } from './risk';
import {
  formatCount,
  GATE_NAME,
  SEVERITY_NAME,
  type GateState,
  type SeverityCounts,
} from './severity';
import './viz.css';

export type IrisSize = 40 | 96 | 168;

export interface RiskIrisProps {
  /** Open findings per severity; null or undefined means the service has no scan yet. */
  counts: SeverityCounts | null | undefined;
  /** Quality gate of the latest analysis; drawn as the outer hairline ring. */
  gate?: GateState;
  size?: IrisSize;
  /** Who the number belongs to, for the accessible name: "payments-api". */
  name?: string;
  /** Caption under the number at sizes 96 and 168; "risk points" or "risk" by default. */
  caption?: string;
  /** One-time 600 ms draw on mount (skipped when motion is reduced). */
  animate?: boolean;
  /** Focusable with an explaining tooltip. Turn off inside another interactive element. */
  interactive?: boolean;
  /** Replace the tooltip content, e.g. to use the app's shared Tooltip. */
  renderTooltip?: (terms: RiskTerm[], total: number, gate: GateState | undefined) => ReactNode;
  className?: string;
}

const GEOMETRY: Record<IrisSize, { ring: number; gap: number; gateWidth: number; corner: number }> =
  {
    40: { ring: 4, gap: 2, gateWidth: 1, corner: 0 },
    96: { ring: 8, gap: 4, gateWidth: 1.5, corner: 1 },
    168: { ring: 12, gap: 6, gateWidth: 1.5, corner: 1.5 },
  };

const GAP_RAD = (2 * Math.PI) / 180;

export interface IrisSegment {
  term: RiskTerm;
  start: number;
  end: number;
}

const MIN_RAD = (3 * Math.PI) / 180;

/**
 * Angles (radians, clockwise from 12 o'clock) for each non-zero term:
 * proportional to its share of points, with a 2° gap between segments. A
 * present severity never shrinks below 3°, so one critical next to thousands
 * of lows still shows.
 */
export function irisSegments(terms: RiskTerm[]): IrisSegment[] {
  const parts = terms.filter((t) => t.points > 0);
  if (parts.length === 0) return [];
  if (parts.length === 1) return [{ term: parts[0], start: 0, end: 2 * Math.PI }];
  const available = 2 * Math.PI - parts.length * GAP_RAD;
  const small = parts.filter((t) => t.share * available < MIN_RAD);
  const rest = available - small.length * MIN_RAD;
  const restShare = parts.reduce((sum, t) => sum + (small.includes(t) ? 0 : t.share), 0);
  let angle = GAP_RAD / 2;
  return parts.map((term) => {
    const sweep = small.includes(term) ? MIN_RAD : (term.share / restShare) * rest;
    const start = angle;
    const end = start + sweep;
    angle = end + GAP_RAD;
    return { term, start, end };
  });
}

/** Compact the centre number where the full one would not fit inside the ring. */
export function centreText(size: IrisSize, value: number) {
  const limit = size === 40 ? 1000 : size === 96 ? 10_000 : 100_000;
  if (value < limit) return formatCount(value);
  const text = formatCount(value, true);
  return size === 40 ? text.replace(/\.\d(?=[KMB])/, '') : text;
}

function IrisTip({ children }: { children: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    if (rect.right > window.innerWidth - 8) el.dataset.flip = 'true';
  }, []);
  return (
    <div ref={ref} className="viz-tip viz-iris-tip" data-testid="viz-tooltip" aria-hidden="true">
      {children}
    </div>
  );
}

/**
 * The risk iris: one risk number inside a ring whose segments are each
 * severity's share of the risk points, with the quality gate as a hairline
 * outer ring. Hover or focus explains the arithmetic.
 */
export function RiskIris({
  counts,
  gate,
  size = 96,
  name,
  caption,
  animate = true,
  interactive = true,
  renderTooltip,
  className = '',
}: RiskIrisProps) {
  const reduced = useReducedMotion();
  const rawId = useId();
  const maskId = `viz-iris-${rawId.replace(/[^a-zA-Z0-9_-]/g, '')}`;
  const [open, setOpen] = useState(false);
  const [activeIndex, setActiveIndex] = useState<number | null>(null);

  const hasData = counts != null;
  const terms = useMemo(() => riskTerms(counts), [counts]);
  const total = useMemo(() => riskPoints(counts), [counts]);
  const segments = useMemo(() => irisSegments(terms), [terms]);
  const shown = useCountUp(total, reduced);

  const g = GEOMETRY[size];
  const half = size / 2;
  const gateR = half - g.gateWidth;
  const outer = gateR - g.gap;
  const inner = outer - g.ring;
  const mid = (outer + inner) / 2;
  const path = arc<{ start: number; end: number }>()
    .innerRadius(inner)
    .outerRadius(outer)
    .startAngle((d) => d.start)
    .endAngle((d) => d.end)
    .cornerRadius(g.corner);

  const draw = animate && !reduced && hasData && total > 0;
  const active = activeIndex !== null ? segments[activeIndex]?.term.severity : undefined;

  const who = name ? `${name}: ` : '';
  const label = !hasData
    ? `${who}no scan yet`
    : `${who}${explainRisk(counts)}${gate ? ` ${GATE_NAME[gate]}.` : ''}`;

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key === 'Escape') {
      setOpen(false);
      setActiveIndex(null);
      return;
    }
    if (segments.length === 0) return;
    if (e.key === 'ArrowRight' || e.key === 'ArrowDown') {
      e.preventDefault();
      setOpen(true);
      setActiveIndex((i) => (i === null ? 0 : (i + 1) % segments.length));
    } else if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') {
      e.preventDefault();
      setOpen(true);
      setActiveIndex((i) =>
        i === null ? segments.length - 1 : (i - 1 + segments.length) % segments.length,
      );
    }
  };

  const numberText = hasData ? centreText(size, shown) : '–';
  const showCaption = size !== 40;

  const tooltip =
    open && interactive && hasData ? (
      <IrisTip>
        {renderTooltip ? (
          renderTooltip(terms, total, gate)
        ) : (
          <>
            <p className="viz-tip-title">
              Risk <strong>{formatCount(total)}</strong>
            </p>
            {terms.map((t) => (
              <div
                key={t.severity}
                className="viz-iris-term"
                data-active={active === t.severity || undefined}
                data-zero={t.points === 0 || undefined}
              >
                <TipRow
                  glyph={<SeverityGlyph severity={t.severity} />}
                  label={`${SEVERITY_NAME[t.severity]} ${formatCount(t.count)} × ${t.weight}`}
                  value={formatCount(t.points)}
                />
              </div>
            ))}
            {gate && (
              <div className="viz-tip-foot">
                <TipRow
                  glyph={<GateGlyph gate={gate} size={10} />}
                  label={GATE_NAME[gate]}
                  value=""
                />
              </div>
            )}
            <p className="viz-tip-note">Weights: {RISK_SOURCE}.</p>
          </>
        )}
      </IrisTip>
    ) : null;

  return (
    <div
      className={`viz-iris viz-iris-${size} ${className}`}
      data-state={!hasData ? 'none' : total === 0 ? 'calm' : 'risk'}
      role="img"
      aria-label={label}
      tabIndex={interactive && hasData ? 0 : undefined}
      onFocus={() => setOpen(true)}
      onBlur={() => {
        setOpen(false);
        setActiveIndex(null);
      }}
      onPointerEnter={() => setOpen(true)}
      onPointerLeave={() => {
        setOpen(false);
        setActiveIndex(null);
      }}
      onKeyDown={interactive ? onKeyDown : undefined}
    >
      <svg
        width={size}
        height={size}
        viewBox={`${-half} ${-half} ${size} ${size}`}
        aria-hidden="true"
        focusable="false"
      >
        {draw && (
          <mask id={maskId}>
            <circle
              className="viz-iris-sweep"
              r={mid}
              fill="none"
              stroke="#fff"
              strokeWidth={g.ring + 4}
              pathLength={1}
              transform="rotate(-90)"
            />
          </mask>
        )}
        {gate && (
          <circle
            className={`viz-iris-gate viz-iris-gate-${gate}`}
            r={gateR}
            strokeWidth={g.gateWidth}
          />
        )}
        {!hasData || total === 0 ? (
          <circle className="viz-iris-track" r={mid} strokeWidth={g.ring} />
        ) : (
          <g mask={draw ? `url(#${maskId})` : undefined}>
            {segments.map((s, i) => (
              <path
                key={s.term.severity}
                className={`viz-iris-seg viz-sev-${s.term.severity}`}
                data-dim={active && active !== s.term.severity ? true : undefined}
                d={path(s) ?? ''}
                onPointerEnter={() => setActiveIndex(i)}
              />
            ))}
          </g>
        )}
      </svg>
      <span className="viz-iris-centre" aria-hidden="true">
        <span className="viz-iris-number" data-long={numberText.length > 3 || undefined}>
          {numberText}
        </span>
        {showCaption && (
          <span className="viz-iris-caption">
            {hasData ? (caption ?? (size === 168 ? 'risk points' : 'risk')) : 'no scan'}
          </span>
        )}
      </span>
      {tooltip}
    </div>
  );
}
