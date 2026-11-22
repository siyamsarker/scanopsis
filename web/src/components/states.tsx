// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { RotateCw } from 'lucide-react';
import type { ReactNode } from 'react';
import { Link } from 'react-router';
import { buttonClass } from '../ui';
import { LensArcs, StateArt, type EmptyArt } from './illustrations';

export type { EmptyArt } from './illustrations';

export interface EmptyStep {
  title: string;
  body?: ReactNode;
}

/**
 * Nothing to show yet. A small illustration, a plain title, one sentence of
 * what happens next and, where possible, the action that fixes it.
 *
 * `art` picks the illustration: "lens" (default), "services" (nothing
 * registered), "search" (no match for a filter), "clean" (nothing to fix),
 * "pipeline" (connect a pipeline), "timeline" (no history yet), "locked",
 * "lost" and "error". Variant "connect" lists numbered next steps and defaults
 * to the pipeline art. Use `compact` inside cards and tables.
 */
export function EmptyState({
  title,
  body,
  action,
  variant = 'default',
  art,
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
  art?: EmptyArt;
  /** Numbered next steps; shown for variant "connect". */
  steps?: EmptyStep[];
  compact?: boolean;
  headingLevel?: 2 | 3 | 4;
  className?: string;
}) {
  const Heading = `h${headingLevel}` as const;
  const shown = art ?? (variant === 'connect' ? 'pipeline' : 'lens');
  return (
    <div
      className={`flex flex-col items-center text-center ${compact ? 'px-4 py-8' : 'px-6 py-12 sm:py-16'} ${className}`}
    >
      <div className={compact ? 'w-32' : 'w-44'}>
        <StateArt art={shown} />
      </div>
      <Heading
        className={`mt-4 font-semibold tracking-tight text-text ${compact ? 'text-body' : 'text-h3'}`}
      >
        {title}
      </Heading>
      {body ? <div className="mt-1.5 max-w-md text-body-s text-text-muted">{body}</div> : null}
      {variant === 'connect' && steps && steps.length > 0 ? (
        <ol className="mt-6 w-full max-w-md space-y-0 text-left">
          {steps.map((step, index) => (
            <li key={step.title} className="relative flex gap-3 pb-4 last:pb-0">
              {index < steps.length - 1 ? (
                <span aria-hidden="true" className="absolute top-7 bottom-0 left-3 w-px bg-line" />
              ) : null}
              <span
                aria-hidden="true"
                className="relative flex size-6 shrink-0 items-center justify-center rounded-full border border-line-strong bg-surface text-caption font-semibold text-text tabular-nums"
              >
                {index + 1}
              </span>
              <span className="min-w-0 pt-0.5">
                <span className="block text-body-s font-medium text-text">
                  <span className="sr-only">Step {index + 1}: </span>
                  {step.title}
                </span>
                {step.body ? (
                  <span className="mt-0.5 block text-body-s text-text-muted">{step.body}</span>
                ) : null}
              </span>
            </li>
          ))}
        </ol>
      ) : null}
      {action ? (
        <div className="mt-6 flex flex-wrap items-center justify-center gap-2">{action}</div>
      ) : null}
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
  action,
  compact = false,
  className = '',
}: {
  title?: string;
  /** What happened and what it means for the person, in one or two sentences. */
  message: string;
  detail?: string;
  onRetry?: () => void;
  retrying?: boolean;
  /** Another way out, next to Try again (a link back). */
  action?: ReactNode;
  compact?: boolean;
  className?: string;
}) {
  return (
    <div
      role="alert"
      className={`flex flex-col items-center text-center ${compact ? 'px-4 py-8' : 'px-6 py-12'} ${className}`}
    >
      <div className={compact ? 'w-24' : 'w-32'}>
        <StateArt art="error" />
      </div>
      <h2
        className={`mt-3 font-semibold tracking-tight text-text ${compact ? 'text-body' : 'text-h3'}`}
      >
        {title}
      </h2>
      <p className="mt-1.5 max-w-md text-body-s text-text-muted">{message}</p>
      {detail ? <p className="mt-2 font-mono text-caption text-text-faint">{detail}</p> : null}
      {onRetry || action ? (
        <div className="mt-6 flex flex-wrap items-center justify-center gap-2">
          {onRetry ? (
            <button
              type="button"
              className={buttonClass('secondary')}
              onClick={onRetry}
              disabled={retrying}
            >
              <RotateCw
                size={16}
                strokeWidth={1.75}
                aria-hidden="true"
                className={retrying ? 'animate-spin' : undefined}
              />
              {retrying ? 'Trying again' : 'Try again'}
            </button>
          ) : null}
          {action}
        </div>
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
      className="flex min-h-30 flex-col rounded-panel border border-line bg-surface p-5"
    >
      <Skeleton className="h-4 w-24" />
      <Skeleton className="mt-auto h-9 w-20" />
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
    <div role="status" aria-label="Loading" className="divide-y divide-line">
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

/**
 * The lens mark turning: a loading indicator for a whole page or a large
 * region. Its two arcs rotate in opposite directions; with reduced motion it
 * stands still and the text says what is happening.
 */
export function LensSpinner({ size = 48 }: { size?: number }) {
  return (
    <svg viewBox="0 0 48 48" width={size} height={size} fill="none" aria-hidden="true">
      <circle cx="24" cy="24" r="21" stroke="var(--line)" strokeWidth="1.5" />
      <g className="lens-spin-a">
        <LensArcs cx={24} cy={24} r={11} strokeWidth={2.5} className="text-lens-a" />
      </g>
    </svg>
  );
}

/** A full-page loading state, centred in the space it is given. */
export function PageLoader({
  label = 'Loading',
  fullScreen = false,
}: {
  /** What is loading, read by screen readers and shown under the mark. */
  label?: string;
  /** Fill the viewport (before the app shell exists) instead of the content area. */
  fullScreen?: boolean;
}) {
  return (
    <div
      role="status"
      aria-live="polite"
      className={`flex flex-col items-center justify-center gap-4 ${fullScreen ? 'min-h-dvh' : 'min-h-[50vh]'}`}
    >
      <LensSpinner />
      <p className="text-body-s text-text-muted">{label}…</p>
    </div>
  );
}

/** Not found: a page or an object that does not exist (or no longer does). */
export function NotFoundState({
  title = 'Page not found',
  body,
  homeLabel = 'Go to the portfolio',
  homeTo = '/',
}: {
  title?: string;
  body?: ReactNode;
  homeLabel?: string;
  homeTo?: string;
}) {
  return (
    <div className="flex flex-col items-center px-6 py-16 text-center sm:py-24">
      <div className="w-40">
        <StateArt art="lost" />
      </div>
      <p className="mt-4 font-mono text-caption text-text-faint">404</p>
      <h1 className="mt-1 text-h2 font-semibold tracking-tight text-text">{title}</h1>
      <div className="mt-2 max-w-md text-body-s text-text-muted">
        {body ??
          'The page you asked for does not exist. Check the address, or start again from the portfolio.'}
      </div>
      <Link to={homeTo} className={`${buttonClass('secondary')} mt-6`}>
        {homeLabel}
      </Link>
    </div>
  );
}

/** Forbidden: the person is signed in but their role does not allow this page. */
export function ForbiddenState({
  title = 'You need the admin role for this page',
  body = 'Ask an administrator of this Scanopsis to change your role if you need access.',
}: {
  title?: string;
  body?: ReactNode;
}) {
  return (
    <div className="flex flex-col items-center px-6 py-16 text-center">
      <div className="w-36">
        <StateArt art="locked" />
      </div>
      <p className="mt-4 text-h3 font-semibold tracking-tight text-text">{title}</p>
      <div className="mt-1.5 max-w-md text-body-s text-text-muted">{body}</div>
      <Link to="/" className={`${buttonClass('secondary')} mt-6`}>
        Go to the portfolio
      </Link>
    </div>
  );
}
