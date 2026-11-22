// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { Check } from 'lucide-react';
import { useEffect, useId, useRef, type ReactNode } from 'react';
import { Wordmark } from './ApertureMark';
import { arc } from './illustrations';
import { ThemeSwitch } from './ThemeSwitch';

export interface AuthSteps {
  /** Names of the steps, in order: "Scan the code", "Enter a code", "Save recovery codes". */
  labels: string[];
  /** Zero-based index of the current step. */
  current: number;
}

/** Where the person is in a multi-step flow; the current step is marked for screen readers. */
export function StepIndicator({ steps, className = '' }: { steps: AuthSteps; className?: string }) {
  const { labels, current } = steps;
  return (
    <nav aria-label="Progress" className={className}>
      <p className="mb-2 text-caption text-text-faint tabular-nums">
        Step {current + 1} of {labels.length}
      </p>
      <ol className="flex gap-1.5">
        {labels.map((label, index) => {
          const state = index < current ? 'done' : index === current ? 'current' : 'next';
          return (
            <li
              key={label}
              aria-current={state === 'current' ? 'step' : undefined}
              className="min-w-0 flex-1"
            >
              <span
                aria-hidden="true"
                className={`block h-1 rounded-full ${
                  state === 'next'
                    ? 'bg-line'
                    : state === 'current'
                      ? 'bg-[image:var(--lens)]'
                      : 'bg-brand'
                }`}
              />
              <span
                className={`mt-2 flex items-center gap-1 text-caption ${state === 'current' ? 'font-medium text-text' : 'text-text-faint'}`}
              >
                {state === 'done' ? (
                  <Check size={12} strokeWidth={2.5} aria-hidden="true" className="text-brand" />
                ) : null}
                <span className="truncate">{label}</span>
                {state === 'done' ? <span className="sr-only"> (done)</span> : null}
              </span>
            </li>
          );
        })}
      </ol>
    </nav>
  );
}

/**
 * The large lens of the sign-in pages: the mark's two arcs drawn in the lens
 * gradient, in focal rings, over a faint pipeline of columns. Decorative.
 */
function LensPlate() {
  const id = useId().replace(/:/g, '');
  const arcs = [arc(121.2, 144, 84, -110, 190), arc(198.8, 144, 84, 70, 370)];
  const columns = [38, 64, 30, 82, 52, 96, 44, 70, 58, 88, 36, 62];
  return (
    <svg
      viewBox="0 0 320 300"
      fill="none"
      aria-hidden="true"
      className="h-auto w-full max-w-[420px]"
    >
      <defs>
        <linearGradient
          id={`${id}-lens`}
          x1="40"
          y1="40"
          x2="280"
          y2="260"
          gradientUnits="userSpaceOnUse"
        >
          <stop offset="0" className="[stop-color:var(--lens-a)]" />
          <stop offset="1" className="[stop-color:var(--lens-b)]" />
        </linearGradient>
      </defs>
      <g className="text-line">
        <circle
          cx="160"
          cy="144"
          r="138"
          stroke="currentColor"
          strokeDasharray="2 7"
          strokeLinecap="round"
        />
        <circle cx="160" cy="144" r="112" stroke="currentColor" />
      </g>
      <g className="text-line-strong" opacity="0.5">
        {columns.map((h, i) => (
          <rect
            key={i}
            x={56 + i * 18}
            y={262 - h * 0.5}
            width="8"
            height={h * 0.5}
            rx="2"
            fill="currentColor"
          />
        ))}
      </g>
      <g stroke={`url(#${id}-lens)`} strokeWidth="7" strokeLinecap="round">
        {arcs.map((d) => (
          <path key={d} d={d} />
        ))}
      </g>
    </svg>
  );
}

/**
 * The frame of sign-in, setup and the session steps: a brand plane with the
 * lens and one line on what Scanopsis does, beside the form on the canvas.
 * On phones the plane folds into a header. The heading takes focus when the
 * title changes, so keyboard and screen reader users land on the new step.
 */
export function AuthShell({
  title,
  lead,
  children,
  steps,
  wide = false,
  focusHeading = true,
}: {
  title: string;
  lead?: ReactNode;
  children: ReactNode;
  /** A step indicator above the title, for multi-step flows. */
  steps?: AuthSteps;
  wide?: boolean;
  focusHeading?: boolean;
}) {
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    if (focusHeading) heading.current?.focus();
  }, [title, focusHeading]);

  return (
    <div className="grid min-h-dvh lg:grid-cols-[minmax(0,5fr)_minmax(0,7fr)]">
      <aside className="relative hidden overflow-hidden border-r border-line bg-surface lg:flex lg:flex-col lg:justify-between lg:p-12">
        <Wordmark size={28} className="text-xl" />
        <div className="flex flex-col items-center gap-10">
          <LensPlate />
        </div>
        <div className="max-w-md">
          <p className="text-h3 font-semibold tracking-tight text-text">
            See what every pipeline changed.
          </p>
          <p className="mt-2 text-body-s text-text-muted">
            Scanopsis brings SonarQube code quality and Trivy vulnerability and secret scans from
            your GitLab pipelines together, per service and across all of them.
          </p>
        </div>
      </aside>
      <main className="flex min-w-0 flex-col px-5 py-6 sm:px-10 sm:py-10">
        <div className="flex items-center justify-between gap-4">
          <span className="lg:invisible">
            <Wordmark size={24} className="text-lg" />
          </span>
          <ThemeSwitch />
        </div>
        <div className="flex flex-1 items-center py-10 sm:py-14">
          <section
            className={`anim-rise mx-auto flex w-full ${wide ? 'max-w-xl' : 'max-w-md'} flex-col gap-6`}
          >
            {steps ? <StepIndicator steps={steps} /> : null}
            <div>
              <h1
                ref={heading}
                tabIndex={-1}
                className="text-h2 font-semibold tracking-tight text-text sm:text-display-s"
              >
                {title}
              </h1>
              {lead && <p className="mt-2 text-body text-text-muted">{lead}</p>}
            </div>
            {children}
          </section>
        </div>
        <p className="text-caption text-text-faint lg:hidden">
          SonarQube and Trivy results from your GitLab pipelines, per service and across all of
          them.
        </p>
      </main>
    </div>
  );
}
