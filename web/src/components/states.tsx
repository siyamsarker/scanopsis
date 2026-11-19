// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { CircleAlert, RotateCw } from 'lucide-react';
import type { ReactNode } from 'react';
import { ApertureMark } from './ApertureMark';
import { secondaryButtonClass } from '../ui';

/**
 * The aperture motif behind empty states: the mark inside two quiet rings over
 * a soft brand glow. Decorative, static, drawn in currentColor.
 */
export function ApertureOrnament({ size = 112 }: { size?: number }) {
  return (
    <span
      aria-hidden="true"
      className="aperture-halo relative flex size-40 shrink-0 items-center justify-center text-brand"
    >
      <svg
        width={size + 24}
        height={size + 24}
        viewBox="0 0 136 136"
        fill="none"
        stroke="currentColor"
        strokeWidth={1}
        className="absolute opacity-30"
      >
        <circle cx="68" cy="68" r="66" strokeDasharray="2 6" strokeLinecap="round" />
        <circle cx="68" cy="68" r="50" className="opacity-60" />
      </svg>
      <ApertureMark size={Math.round(size / 2.2)} />
    </span>
  );
}

export interface EmptyStep {
  title: string;
  body?: ReactNode;
}

/**
 * Nothing to show yet. The aperture, a plain title and one sentence of what
 * happens next, with an optional action. Variant "connect" lists numbered next
 * steps for getting data in. Use `compact` inside cards and tables.
 */
export function EmptyState({
  title,
  body,
  action,
  variant = 'default',
  steps,
  compact = false,
  headingLevel = 2,
  className = '',
}: {
  title: string;
  body?: ReactNode;
  /** A button or link, e.g. "Add a service". */
  action?: ReactNode;
  variant?: 'default' | 'connect';
  /** Numbered next steps; shown for variant "connect". */
  steps?: EmptyStep[];
  compact?: boolean;
  headingLevel?: 2 | 3 | 4;
  className?: string;
}) {
  const Heading = `h${headingLevel}` as const;
  return (
    <div
      className={`flex flex-col items-center text-center ${compact ? 'px-4 py-8' : 'px-6 py-12 sm:py-16'} ${className}`}
    >
      {compact ? <ApertureOrnament size={64} /> : <ApertureOrnament />}
      <Heading className="mt-2 text-lg font-semibold tracking-tight text-text">{title}</Heading>
      {body ? <div className="mt-2 max-w-md text-text-muted">{body}</div> : null}
      {variant === 'connect' && steps && steps.length > 0 ? (
        <ol className="mt-6 w-full max-w-md space-y-4 text-left">
          {steps.map((step, index) => (
            <li key={step.title} className="flex gap-3">
              <span
                aria-hidden="true"
                className="flex size-6 shrink-0 items-center justify-center rounded-full border border-border-strong font-mono text-sm tabular-nums text-text"
              >
                {index + 1}
              </span>
              <span className="min-w-0">
                <span className="block font-medium text-text">
                  <span className="sr-only">Step {index + 1}: </span>
                  {step.title}
                </span>
                {step.body ? (
                  <span className="mt-0.5 block text-text-muted">{step.body}</span>
                ) : null}
              </span>
            </li>
          ))}
        </ol>
      ) : null}
      {action ? <div className="mt-6">{action}</div> : null}
    </div>
  );
}

/**
 * Something failed to load: what happened in plain words, and a way to try
 * again. `detail` is for a request id or code and is set in monospace.
 */
export function ErrorState({
  title = 'This could not be loaded',
  message,
  detail,
  onRetry,
  retrying = false,
  compact = false,
  className = '',
}: {
  title?: string;
  /** What happened and what it means for the person, in one or two sentences. */
  message: string;
  detail?: string;
  onRetry?: () => void;
  retrying?: boolean;
  compact?: boolean;
  className?: string;
}) {
  return (
    <div
      role="alert"
      className={`flex flex-col items-center text-center ${compact ? 'px-4 py-8' : 'px-6 py-12'} ${className}`}
    >
      <span className="flex size-12 items-center justify-center rounded-full border border-border bg-bg">
        <CircleAlert size={24} strokeWidth={1.5} aria-hidden="true" className="text-fail" />
      </span>
      <h2 className="mt-4 text-lg font-semibold tracking-tight text-text">{title}</h2>
      <p className="mt-2 max-w-md text-text-muted">{message}</p>
      {detail ? <p className="mt-2 font-mono text-sm text-text-muted">{detail}</p> : null}
      {onRetry ? (
        <button
          type="button"
          className={`${secondaryButtonClass} mt-6`}
          onClick={onRetry}
          disabled={retrying}
        >
          <RotateCw
            size={16}
            strokeWidth={1.5}
            aria-hidden="true"
            className={retrying ? 'animate-spin' : undefined}
          />
          {retrying ? 'Trying again' : 'Try again'}
        </button>
      ) : null}
    </div>
  );
}

/** A grey block; size it with Tailwind classes so it matches the content that replaces it. */
export function Skeleton({ className = '' }: { className?: string }) {
  return <div aria-hidden="true" className={`skeleton ${className}`} />;
}

/** Lines of text; the last line is shorter, like a paragraph. */
export function SkeletonText({
  lines = 3,
  className = '',
}: {
  lines?: number;
  className?: string;
}) {
  return (
    <div aria-hidden="true" className={`space-y-2 ${className}`}>
      {Array.from({ length: lines }, (_, index) => (
        <Skeleton
          key={index}
          className={`h-4 ${index === lines - 1 && lines > 1 ? 'w-2/3' : 'w-full'}`}
        />
      ))}
    </div>
  );
}

/** The exact footprint of a StatTile. */
export function SkeletonStat() {
  return (
    <div
      role="status"
      aria-label="Loading"
      className="min-h-28 rounded-card border border-border bg-surface p-4"
    >
      <Skeleton className="h-4 w-24" />
      <Skeleton className="mt-2 h-9 w-20" />
      <Skeleton className="mt-2 h-4 w-32" />
    </div>
  );
}

/** Table rows with the same row height as DataTable's density. */
export function SkeletonTable({
  rows = 6,
  columns = 4,
  density = 'comfortable',
}: {
  rows?: number;
  columns?: number;
  density?: 'comfortable' | 'compact';
}) {
  const row = density === 'compact' ? 'h-8' : 'h-11';
  return (
    <div role="status" aria-label="Loading" className="divide-y divide-border">
      {Array.from({ length: rows }, (_, r) => (
        <div key={r} className={`flex items-center gap-6 px-4 ${row}`}>
          {Array.from({ length: columns }, (_, c) => (
            <Skeleton key={c} className={`h-4 ${c === 0 ? 'w-1/4' : 'w-1/6'} flex-none`} />
          ))}
        </div>
      ))}
    </div>
  );
}

/** A chart's footprint. Give the same height class the chart will have. */
export function SkeletonChart({ className = 'h-72' }: { className?: string }) {
  return (
    <div
      role="status"
      aria-label="Loading chart"
      className={`flex items-end gap-2 px-2 ${className}`}
    >
      {[40, 65, 50, 80, 60, 90, 70, 55, 75, 45, 85, 60].map((height, index) => (
        <Skeleton key={index} className={`flex-1 ${barHeights[height] ?? 'h-1/2'}`} />
      ))}
    </div>
  );
}

const barHeights: Record<number, string> = {
  40: 'h-[40%]',
  45: 'h-[45%]',
  50: 'h-[50%]',
  55: 'h-[55%]',
  60: 'h-[60%]',
  65: 'h-[65%]',
  70: 'h-[70%]',
  75: 'h-[75%]',
  80: 'h-[80%]',
  85: 'h-[85%]',
  90: 'h-[90%]',
};
