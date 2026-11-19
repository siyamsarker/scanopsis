// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { CircleCheck, CircleDashed, CircleX, Minus, TriangleAlert } from 'lucide-react';

/** A SonarQube rating as the API sends it: A to E, or null when not reported. */
export type RatingValue = 'A' | 'B' | 'C' | 'D' | 'E' | null | undefined;

const ratingTone = { A: 'a', B: 'b', C: 'c', D: 'd', E: 'e' } as const;

/**
 * A SonarQube rating on a five-step scale from pass (A) through medium (C) to
 * fail (E). The letter is always printed. `null` reads "Not available" with a
 * dash icon. `label` adds the metric name ("Security") before the letter.
 */
export function RatingBadge({
  rating,
  label,
  className = '',
}: {
  rating: RatingValue;
  /** What is rated: "Reliability", "Security", "Maintainability". */
  label?: string;
  className?: string;
}) {
  if (!rating || !(rating in ratingTone)) {
    return (
      <span className={`chip tone-neutral ${className}`}>
        <Minus
          size={14}
          strokeWidth={1.5}
          aria-hidden="true"
          className="shrink-0 text-text-muted"
        />
        {label ? <span className="text-text-muted">{label}</span> : null}
        Not available
      </span>
    );
  }
  return (
    <span className={`inline-flex items-center gap-2 ${className}`}>
      {label ? <span className="text-sm text-text-muted">{label}</span> : null}
      <span
        className={`tone-${ratingTone[rating]} inline-flex size-7 items-center justify-center rounded-md border border-(--tone) bg-[color-mix(in_srgb,var(--tone)_16%,var(--surface))] text-base font-semibold leading-none text-text`}
      >
        <span className="sr-only">Rating </span>
        {rating}
      </span>
    </span>
  );
}

/** The API's quality gate status; "" and anything unrecognised read as unknown. */
export type GateStatus = 'OK' | 'WARN' | 'ERROR' | 'NONE' | '' | null | undefined;

const gates = {
  OK: { Icon: CircleCheck, tone: 'pass', text: 'Passed' },
  WARN: { Icon: TriangleAlert, tone: 'warn', text: 'Warning' },
  ERROR: { Icon: CircleX, tone: 'fail', text: 'Failed' },
  NONE: { Icon: CircleDashed, tone: 'neutral', text: 'No gate' },
} as const;

/** A quality gate result as icon, colour and word: Passed, Warning, Failed, No gate or Unknown. */
export function GateBadge({ status, className = '' }: { status: GateStatus; className?: string }) {
  const gate = status && status in gates ? gates[status as keyof typeof gates] : null;
  const Icon = gate?.Icon ?? CircleDashed;
  return (
    <span className={`chip tone-${gate?.tone ?? 'neutral'} ${className}`}>
      <Icon
        size={14}
        strokeWidth={1.5}
        aria-hidden="true"
        className={`shrink-0 ${gate ? 'tone-fg' : 'text-text-muted'}`}
      />
      {gate?.text ?? 'Unknown'}
    </span>
  );
}
