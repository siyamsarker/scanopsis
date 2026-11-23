// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import type { ReactNode } from 'react';
import { StatusMark, type StatusKind } from './SeverityMark';

export type ChipTone =
  'neutral' | 'brand' | 'ok' | 'fail' | 'warn' | 'critical' | 'high' | 'medium' | 'low' | 'unknown';

/**
 * A status pill: a 14% tint of the tone, tone-coloured text that keeps 4.5:1,
 * fully rounded. Put a shape or icon in `icon` so colour is never the only
 * carrier; StatusMark and SeverityMark are made for it.
 */
export function Chip({
  tone = 'neutral',
  icon,
  children,
  className = '',
  title,
}: {
  tone?: ChipTone;
  icon?: ReactNode;
  children: ReactNode;
  className?: string;
  title?: string;
}) {
  return (
    <span title={title} className={`chip tone-${tone} ${className}`}>
      {icon}
      {children}
    </span>
  );
}

/** A status as a pill with its mark: "Passed", "Disabled", "Archived". */
export function StatusPill({
  status,
  children,
  className = '',
  title,
}: {
  status: StatusKind;
  children: ReactNode;
  className?: string;
  title?: string;
}) {
  const tone = status === 'none' ? 'neutral' : status;
  return (
    <Chip
      tone={tone}
      icon={<StatusMark status={status} size={13} />}
      className={className}
      title={title}
    >
      {children}
    </Chip>
  );
}

/** A small label that is not a status (a branch kind, a source): 6px radius, sunken. */
export function Tag({
  children,
  icon,
  className = '',
  title,
}: {
  children: ReactNode;
  icon?: ReactNode;
  className?: string;
  title?: string;
}) {
  return (
    <span title={title} className={`tag ${className}`}>
      {icon}
      {children}
    </span>
  );
}

/** A SonarQube rating as the API sends it: A to E, or null when not reported. */
export type RatingValue = 'A' | 'B' | 'C' | 'D' | 'E' | null | undefined;

const ratingTone = { A: 'a', B: 'b', C: 'c', D: 'd', E: 'e' } as const;

/**
 * A SonarQube rating on a five-step scale from ok (A) through medium (C) to
 * fail (E). The letter is always printed. `null` reads "Not available".
 * `label` adds the metric name ("Security") before the letter.
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
      <span className={`inline-flex items-center gap-2 ${className}`}>
        {label ? <span className="text-body-s text-text-muted">{label}</span> : null}
        <span className="tag border-dashed text-text-faint">Not available</span>
      </span>
    );
  }
  return (
    <span className={`inline-flex items-center gap-2 ${className}`}>
      {label ? <span className="text-body-s text-text-muted">{label}</span> : null}
      <span
        className={`tone-${ratingTone[rating]} tone-ink inline-flex size-6 items-center justify-center rounded-[6px] border border-[color-mix(in_srgb,var(--tone)_45%,transparent)] bg-[color-mix(in_srgb,var(--tone)_16%,var(--surface))] text-body-s font-semibold leading-none`}
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
  OK: { status: 'ok', text: 'Passed' },
  WARN: { status: 'warn', text: 'Warning' },
  ERROR: { status: 'fail', text: 'Failed' },
  NONE: { status: 'none', text: 'No gate' },
} as const;

/** A quality gate result as mark, colour and word: Passed, Warning, Failed, No gate or Unknown. */
export function GateBadge({ status, className = '' }: { status: GateStatus; className?: string }) {
  const gate = status && status in gates ? gates[status as keyof typeof gates] : null;
  return (
    <StatusPill status={gate?.status ?? 'none'} className={className}>
      {gate?.text ?? 'Unknown'}
    </StatusPill>
  );
}
