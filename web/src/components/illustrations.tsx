// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import type { ReactNode } from 'react';

/*
 * Small purpose-made illustrations for empty, error and access states, each
 * built from the lens mark: two open arcs whose overlap forms a lens. They are
 * decorative (aria-hidden) and drawn with theme tokens, so both themes work.
 */

export type EmptyArt =
  'lens' | 'services' | 'search' | 'clean' | 'pipeline' | 'timeline' | 'locked' | 'lost' | 'error';

/** An arc of a circle from `from` to `to` degrees, clockwise (0 = 3 o'clock, y down). */
export function arc(cx: number, cy: number, r: number, from: number, to: number): string {
  const rad = (deg: number) => (deg * Math.PI) / 180;
  const sweep = (((to - from) % 360) + 360) % 360;
  const x1 = cx + r * Math.cos(rad(from));
  const y1 = cy + r * Math.sin(rad(from));
  const x2 = cx + r * Math.cos(rad(to));
  const y2 = cy + r * Math.sin(rad(to));
  const f = (n: number) => n.toFixed(2);
  return `M${f(x1)} ${f(y1)}A${r} ${r} 0 ${sweep > 180 ? 1 : 0} 1 ${f(x2)} ${f(y2)}`;
}

/**
 * The lens mark at any size: two circles of radius r, 0.46 r apart, each open
 * by 60 degrees on opposite sides, as in ApertureMark.
 */
export function LensArcs({
  cx,
  cy,
  r,
  spread = 0.4615,
  strokeWidth = 2,
  className = 'text-brand',
}: {
  cx: number;
  cy: number;
  r: number;
  /** Distance of each centre from the middle, as a share of r. */
  spread?: number;
  strokeWidth?: number;
  className?: string;
}) {
  const d = r * spread;
  return (
    <g
      fill="none"
      stroke="currentColor"
      strokeWidth={strokeWidth}
      strokeLinecap="round"
      className={className}
    >
      <path d={arc(cx - d, cy, r, -110, 190)} />
      <path d={arc(cx + d, cy, r, 70, 370)} />
    </g>
  );
}

function Frame({ children, label }: { children: ReactNode; label: string }) {
  return (
    <svg
      viewBox="0 0 160 112"
      width="160"
      height="112"
      fill="none"
      aria-hidden="true"
      focusable="false"
      data-art={label}
      className="h-auto max-w-full"
    >
      {children}
    </svg>
  );
}

const faint = 'text-line-strong';
const quiet = 'text-line';

const arts: Record<EmptyArt, () => ReactNode> = {
  // The mark in its rings: nothing here yet.
  lens: () => (
    <Frame label="lens">
      <circle
        cx="80"
        cy="56"
        r="46"
        stroke="currentColor"
        strokeDasharray="2 6"
        strokeLinecap="round"
        className={faint}
      />
      <circle cx="80" cy="56" r="34" stroke="currentColor" className={quiet} />
      <LensArcs cx={80} cy={56} r={18} strokeWidth={2.5} />
    </Frame>
  ),
  // Three service cards, the lens looking at the empty front one.
  services: () => (
    <Frame label="services">
      <rect x="30" y="16" width="76" height="52" rx="10" stroke="currentColor" className={quiet} />
      <rect
        x="40"
        y="28"
        width="76"
        height="52"
        rx="10"
        fill="var(--surface)"
        stroke="currentColor"
        className={faint}
      />
      <rect
        x="50"
        y="40"
        width="76"
        height="52"
        rx="10"
        fill="var(--surface-raised)"
        stroke="currentColor"
        strokeDasharray="4 4"
        className={faint}
      />
      <path
        d="M62 56h28M62 66h18"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        className={quiet}
      />
      <circle cx="116" cy="78" r="17" fill="var(--surface)" />
      <LensArcs cx={116} cy={78} r={12} strokeWidth={2.25} />
    </Frame>
  ),
  // Rows passing under the lens; inside the lens there is no match.
  search: () => (
    <Frame label="search">
      {[30, 46, 62, 78].map((y, i) => (
        <path
          key={y}
          d={`M18 ${y}h${92 - i * 14}`}
          stroke="currentColor"
          strokeWidth="6"
          strokeLinecap="round"
          className={quiet}
        />
      ))}
      <circle
        cx="104"
        cy="54"
        r="27"
        fill="var(--surface)"
        stroke="currentColor"
        className={faint}
      />
      <LensArcs cx={104} cy={54} r={17} strokeWidth={2.5} />
      <path
        d="m98 48 12 12m0-12-12 12"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        className="text-text-faint"
      />
    </Frame>
  ),
  // A calm, closed ring around the lens with a check: nothing to fix.
  clean: () => (
    <Frame label="clean">
      <circle
        cx="80"
        cy="56"
        r="40"
        stroke="currentColor"
        strokeWidth="6"
        className="text-[color-mix(in_srgb,var(--ok)_22%,transparent)]"
      />
      <circle cx="80" cy="56" r="40" stroke="currentColor" strokeWidth="1.5" className="text-ok" />
      <LensArcs cx={80} cy={56} r={18} strokeWidth={2.5} className="text-ok" />
      <path
        d="m73 57 5 5 10-11"
        stroke="currentColor"
        strokeWidth="2.5"
        strokeLinecap="round"
        strokeLinejoin="round"
        className="text-ok"
      />
    </Frame>
  ),
  // A pipeline flowing into the lens: connect a pipeline to see results.
  pipeline: () => (
    <Frame label="pipeline">
      <path
        d="M22 56h86"
        stroke="currentColor"
        strokeWidth="2"
        strokeDasharray="3 5"
        strokeLinecap="round"
        className={faint}
      />
      {[22, 50, 78].map((x) => (
        <g key={x}>
          <circle
            cx={x}
            cy="56"
            r="9"
            fill="var(--surface)"
            stroke="currentColor"
            strokeWidth="2"
            className={faint}
          />
          <circle cx={x} cy="56" r="3" fill="currentColor" className={faint} />
        </g>
      ))}
      <circle
        cx="122"
        cy="56"
        r="24"
        fill="var(--surface)"
        stroke="currentColor"
        className={quiet}
      />
      <LensArcs cx={122} cy={56} r={15} strokeWidth={2.5} />
    </Frame>
  ),
  // An empty time axis with placeholder columns: no scans over time yet.
  timeline: () => (
    <Frame label="timeline">
      <path d="M12 84h136" stroke="currentColor" strokeWidth="1.5" className={faint} />
      {[18, 38, 58, 78, 98].map((x, i) => {
        const h = [22, 34, 18, 40, 28][i];
        return (
          <rect
            key={x}
            x={x}
            y={84 - h}
            width="12"
            height={h}
            rx="3"
            fill="var(--wash)"
            stroke="currentColor"
            strokeDasharray="3 3"
            className={faint}
          />
        );
      })}
      <circle
        cx="132"
        cy="40"
        r="19"
        fill="var(--surface)"
        stroke="currentColor"
        className={faint}
      />
      <LensArcs cx={132} cy={40} r={12} strokeWidth={2.5} />
      <path
        d="M132 59v25"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeDasharray="2 3"
        className={faint}
      />
    </Frame>
  ),
  // The lens behind a closed shutter: you need another role to see this.
  locked: () => (
    <Frame label="locked">
      <circle cx="80" cy="56" r="40" stroke="currentColor" className={quiet} />
      <LensArcs cx={80} cy={56} r={22} strokeWidth={2.5} className="text-line-strong" />
      <rect
        x="62"
        y="50"
        width="36"
        height="28"
        rx="7"
        fill="var(--surface-raised)"
        stroke="currentColor"
        strokeWidth="2"
        className="text-brand"
      />
      <path
        d="M70 50v-6a10 10 0 0 1 20 0v6"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        className="text-brand"
      />
      <circle cx="80" cy="64" r="3" fill="currentColor" className="text-brand" />
    </Frame>
  ),
  // The two arcs drifted apart: the lens has nothing to focus on.
  lost: () => (
    <Frame label="lost">
      <path
        d="M30 86c26-8 74-8 100 0"
        stroke="currentColor"
        strokeDasharray="2 6"
        strokeLinecap="round"
        className={faint}
      />
      <g
        fill="none"
        stroke="currentColor"
        strokeWidth="2.5"
        strokeLinecap="round"
        className="text-brand"
      >
        <path d={arc(52, 50, 20, -110, 190)} />
        <path d={arc(108, 58, 20, 70, 370)} />
      </g>
      <circle cx="80" cy="54" r="2.5" fill="currentColor" className="text-text-faint" />
    </Frame>
  ),
  // A lens with a broken arc and a warning dot: something failed.
  error: () => (
    <Frame label="error">
      <circle
        cx="80"
        cy="56"
        r="40"
        stroke="currentColor"
        strokeDasharray="2 6"
        strokeLinecap="round"
        className={faint}
      />
      <g fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round">
        <path d={arc(72, 56, 20, -110, 60)} className="text-line-strong" />
        <path d={arc(72, 56, 20, 100, 190)} className="text-line-strong" />
        <path d={arc(88, 56, 20, 70, 370)} className="text-line-strong" />
      </g>
      <circle cx="80" cy="56" r="11" fill="currentColor" className="text-fail" />
      <path d="M80 50v6m0 5v.1" stroke="var(--surface)" strokeWidth="2.5" strokeLinecap="round" />
    </Frame>
  ),
};

/** One of the state illustrations. */
export function StateArt({ art }: { art: EmptyArt }) {
  return <>{arts[art]()}</>;
}
