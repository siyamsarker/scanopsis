// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Pieces every chart in the kit is built from: glyphs, axes, gridlines, the
 * visually hidden data table, the tooltip layer and the focusable mark layer.
 * Colours come from CSS custom properties through the classes in viz.css.
 */

import { useRef, type KeyboardEvent, type ReactNode } from 'react';
import { nextIndex, useStyles } from './hooks';
import { GATE_NAME, SEVERITY_NAME, type GateState, type Severity } from './severity';

/* ------------------------------------------------------------------ glyphs */

/** The severity shape as SVG elements centred on (0, 0) with radius r. */
export function SeverityShape({ severity, r }: { severity: Severity; r: number }) {
  const cls = `viz-glyph viz-sev-${severity}`;
  switch (severity) {
    case 'critical':
      return <path className={cls} d={`M0,${-r}L${r},0L0,${r}L${-r},0Z`} />;
    case 'high':
      return (
        <path
          className={cls}
          d={`M0,${-r}L${r * 0.98},${r * 0.78}L${-r * 0.98},${r * 0.78}Z`}
          strokeLinejoin="round"
        />
      );
    case 'medium':
      return <circle className={cls} r={r * 0.82} />;
    case 'low':
      return <circle className={`${cls} viz-glyph-hollow`} r={r * 0.68} />;
    default:
      return <circle className={`${cls} viz-glyph-hollow viz-glyph-dashed`} r={r * 0.68} />;
  }
}

/** A severity glyph for HTML contexts (legends, tooltips, table headers). */
export function SeverityGlyph({ severity, size = 10 }: { severity: Severity; size?: number }) {
  const r = size / 2;
  return (
    <svg
      className="viz-glyph-svg"
      width={size}
      height={size}
      viewBox={`${-r - 1} ${-r - 1} ${size + 2} ${size + 2}`}
      aria-hidden="true"
      focusable="false"
    >
      <SeverityShape severity={severity} r={r} />
    </svg>
  );
}

/** Gate result as a shape centred on (0, 0): check, cross or a dashed ring. */
export function GateShape({ gate, r }: { gate: GateState; r: number }) {
  if (gate === 'pass') {
    return (
      <g className="viz-gate viz-gate-pass">
        <circle r={r} />
        <path
          className="viz-gate-mark"
          d={`M${-r * 0.45},0L${-r * 0.1},${r * 0.35}L${r * 0.5},${-r * 0.35}`}
        />
      </g>
    );
  }
  if (gate === 'fail') {
    const k = r * 0.38;
    return (
      <g className="viz-gate viz-gate-fail">
        <circle r={r} />
        <path className="viz-gate-mark" d={`M${-k},${-k}L${k},${k}M${k},${-k}L${-k},${k}`} />
      </g>
    );
  }
  return (
    <g className="viz-gate viz-gate-none">
      <circle r={r * 0.8} />
    </g>
  );
}

export function GateGlyph({ gate, size = 12 }: { gate: GateState; size?: number }) {
  const r = size / 2;
  return (
    <svg
      className="viz-glyph-svg"
      width={size}
      height={size}
      viewBox={`${-r - 1} ${-r - 1} ${size + 2} ${size + 2}`}
      aria-hidden="true"
      focusable="false"
    >
      <GateShape gate={gate} r={r} />
    </svg>
  );
}

/** Glyph + text, the way gate results are shown outside charts. */
export function GateLabel({ gate }: { gate: GateState }) {
  return (
    <span className="viz-gate-label">
      <GateGlyph gate={gate} />
      <span>{gate === 'pass' ? 'Passed' : gate === 'fail' ? 'Failed' : 'None'}</span>
    </span>
  );
}

/* ------------------------------------------------------------------ legend */

export interface LegendItem {
  key: string;
  label: ReactNode;
  glyph: ReactNode;
}

/** A compact custom legend: glyph then label, one row, wraps when narrow. */
export function Legend({ items, className = '' }: { items: LegendItem[]; className?: string }) {
  return (
    <ul className={`viz-legend ${className}`}>
      {items.map((item) => (
        <li key={item.key}>
          {item.glyph}
          <span>{item.label}</span>
        </li>
      ))}
    </ul>
  );
}

export function severityLegend(severities: readonly Severity[]): LegendItem[] {
  return severities.map((s) => ({
    key: s,
    label: SEVERITY_NAME[s],
    glyph: <SeverityGlyph severity={s} />,
  }));
}

export function gateLegend(): LegendItem[] {
  return (['pass', 'fail', 'none'] as GateState[]).map((g) => ({
    key: g,
    label: GATE_NAME[g],
    glyph: <GateGlyph gate={g} size={10} />,
  }));
}

/* ------------------------------------------------------------- data table */

export interface SrTableProps {
  caption: string;
  columns: string[];
  rows: (string | number)[][];
}

/** The chart's data as a table that only assistive technology sees. */
export function SrTable({ caption, columns, rows }: SrTableProps) {
  return (
    <table className="viz-sr">
      <caption>{caption}</caption>
      <thead>
        <tr>
          {columns.map((c, i) => (
            <th key={i} scope="col">
              {c}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>
        {rows.map((row, r) => (
          <tr key={r}>
            {row.map((cell, i) =>
              i === 0 ? (
                <th key={i} scope="row">
                  {cell}
                </th>
              ) : (
                <td key={i}>{cell}</td>
              ),
            )}
          </tr>
        ))}
      </tbody>
    </table>
  );
}

/* -------------------------------------------------------------- axes, grid */

export interface Tick {
  value: number;
  y: number;
  text: string;
}

/** Horizontal hairlines at the tick positions; never dashed, never a border. */
export function Gridlines({ ticks, x1, x2 }: { ticks: Tick[]; x1: number; x2: number }) {
  return (
    <g className="viz-grid" aria-hidden="true">
      {ticks.map((t) => (
        <line key={t.value} x1={x1} x2={x2} y1={t.y} y2={t.y} />
      ))}
    </g>
  );
}

/** Tick labels beside the plot, left- or right-aligned to `x`. No axis line. */
export function AxisY({ ticks, x, side }: { ticks: Tick[]; x: number; side: 'left' | 'right' }) {
  return (
    <g className="viz-axis" aria-hidden="true">
      {ticks.map((t) => (
        <text
          key={t.value}
          x={x}
          y={t.y}
          dy="0.32em"
          textAnchor={side === 'left' ? 'end' : 'start'}
        >
          {t.text}
        </text>
      ))}
    </g>
  );
}

export interface XLabel {
  key: string | number;
  x: number;
  text: string;
}

/** Date labels under the plot. The first and last stay inside the frame. */
export function AxisX({ labels, y, width }: { labels: XLabel[]; y: number; width: number }) {
  return (
    <g className="viz-axis" aria-hidden="true">
      {labels.map((l) => {
        const anchor = l.x < 28 ? 'start' : l.x > width - 28 ? 'end' : 'middle';
        return (
          <text key={l.key} x={l.x} y={y} textAnchor={anchor}>
            {l.text}
          </text>
        );
      })}
    </g>
  );
}

/* ---------------------------------------------------------------- tooltip */

export interface TooltipProps {
  /** Anchor in container pixels: the card sits beside x, near y. */
  x: number;
  y: number;
  containerWidth: number;
  children: ReactNode;
  /** Width the card is laid out with, used to flip it to the left side. */
  cardWidth?: number;
}

/**
 * The tooltip layer: an HTML card positioned through the CSSOM inside the
 * chart's relatively positioned box. It repeats what the focused mark's label
 * already says, so it is hidden from assistive technology.
 */
export function ChartTooltip({ x, y, containerWidth, children, cardWidth = 232 }: TooltipProps) {
  const ref = useRef<HTMLDivElement>(null);
  const gap = 14;
  const flip = x + gap + cardWidth > containerWidth;
  const left = flip ? Math.max(0, x - gap - cardWidth) : x + gap;
  useStyles(ref, { left: `${Math.round(left)}px`, top: `${Math.round(Math.max(0, y))}px` });
  return (
    <div ref={ref} className="viz-tip" data-testid="viz-tooltip" aria-hidden="true">
      {children}
    </div>
  );
}

/** A tooltip row: key glyph, label, value. Values lead visually. */
export function TipRow({
  glyph,
  label,
  value,
}: {
  glyph?: ReactNode;
  label: ReactNode;
  value: ReactNode;
}) {
  return (
    <div className="viz-tip-row">
      <span className="viz-tip-key">
        {glyph}
        <span>{label}</span>
      </span>
      <span className="viz-tip-value">{value}</span>
    </div>
  );
}

/* ------------------------------------------------------------ mark layer */

export interface MarkRect {
  key: string | number;
  x: number;
  y: number;
  width: number;
  height: number;
  label: string;
}

function MarkButton({
  rect,
  active,
  tabbable,
  onFocus,
  onClick,
  onKeyDown,
  onPointerEnter,
  setRef,
}: {
  rect: MarkRect;
  active: boolean;
  tabbable: boolean;
  onFocus: () => void;
  onClick: () => void;
  onKeyDown: (e: KeyboardEvent<HTMLButtonElement>) => void;
  onPointerEnter: () => void;
  setRef: (el: HTMLButtonElement | null) => void;
}) {
  const ref = useRef<HTMLButtonElement | null>(null);
  useStyles(ref, {
    left: `${rect.x}px`,
    top: `${rect.y}px`,
    width: `${Math.max(1, rect.width)}px`,
    height: `${Math.max(1, rect.height)}px`,
  });
  return (
    <button
      ref={(el) => {
        ref.current = el;
        setRef(el);
      }}
      type="button"
      className="viz-mark"
      data-active={active || undefined}
      tabIndex={tabbable ? 0 : -1}
      aria-label={rect.label}
      onFocus={onFocus}
      onClick={onClick}
      onKeyDown={onKeyDown}
      onPointerEnter={onPointerEnter}
    />
  );
}

export interface MarkLayerProps {
  /** Accessible name of the group, e.g. "Pipelines". */
  label: string;
  rects: MarkRect[];
  /** The highlighted mark (hover or focus), or null. */
  active: number | null;
  onActiveChange: (index: number | null) => void;
  /** Click or Enter on a mark. Without it the marks are still focusable for reading. */
  onActivate?: (index: number) => void;
}

/**
 * Transparent buttons over the drawn marks: big hit targets for the pointer
 * and one tab stop for the keyboard (arrow keys, Home and End move between
 * marks; Enter or Space activates). Each button's label says everything the
 * tooltip shows.
 */
export function MarkLayer({ label, rects, active, onActiveChange, onActivate }: MarkLayerProps) {
  const buttons = useRef<(HTMLButtonElement | null)[]>([]);
  const focusIndex = active ?? rects.length - 1;
  const handleKey = (index: number) => (e: KeyboardEvent<HTMLButtonElement>) => {
    if (e.key === 'Escape') {
      onActiveChange(null);
      return;
    }
    const next = nextIndex(e.key, index, rects.length);
    if (next === null) return;
    e.preventDefault();
    onActiveChange(next);
    buttons.current[next]?.focus();
  };
  return (
    <div
      className="viz-marks"
      role="group"
      aria-label={label}
      onPointerLeave={() => onActiveChange(null)}
    >
      {rects.map((rect, i) => (
        <MarkButton
          key={rect.key}
          rect={rect}
          active={active === i}
          tabbable={i === focusIndex}
          setRef={(el) => {
            buttons.current[i] = el;
          }}
          onFocus={() => onActiveChange(i)}
          onPointerEnter={() => onActiveChange(i)}
          onClick={() => onActivate?.(i)}
          onKeyDown={handleKey(i)}
        />
      ))}
    </div>
  );
}

/* ------------------------------------------------------------ empty state */

/** The chart's footprint with a calm baseline and a sentence on what to do next. */
export function EmptyChart({
  children,
  className = '',
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={`viz-empty ${className}`} role="note">
      <span className="viz-empty-rule" aria-hidden="true" />
      <p>{children}</p>
    </div>
  );
}
