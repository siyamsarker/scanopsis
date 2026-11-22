// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

/*
 * Small pieces shared by the administration and account pages: a time that
 * stays on one line, initials avatars, flat table cells and inline problems.
 */

import { CircleAlert } from 'lucide-react';
import type { ReactNode } from 'react';
import { formatExactTime, relativeTime } from '../../format';

/** A relative time on one line ("3 hours ago"), with the exact time as a tooltip. */
export function When({
  at,
  prefix,
  className = '',
}: {
  at: string;
  /** Words before the time: "Signed in". */
  prefix?: string;
  className?: string;
}) {
  return (
    <span className={`whitespace-nowrap ${className}`}>
      {prefix ? `${prefix} ` : null}
      <time dateTime={at} title={formatExactTime(at)}>
        {relativeTime(at)}
      </time>
    </span>
  );
}

/** One or two capital letters for a name, a username or an actor ("local:root" gives "RO"). */
export function initialsOf(name: string): string {
  const bare = name.includes(':') ? name.slice(name.indexOf(':') + 1) : name;
  const words = bare
    .trim()
    .split(/[\s._@/-]+/)
    .filter(Boolean);
  if (words.length === 0) return '?';
  const letters = words.length > 1 ? words[0][0] + words[1][0] : words[0].slice(0, 2);
  return letters.toUpperCase();
}

const avatarSizes = {
  sm: 'size-7 text-[0.6875rem]',
  md: 'size-9 text-caption',
  lg: 'size-16 text-h3',
} as const;

/**
 * Initials in a disc. `ring` marks the people who administer (a brand ring),
 * `muted` the ones who cannot sign in. Decorative: the name is always written
 * next to it.
 */
export function InitialsAvatar({
  name,
  size = 'md',
  ring = false,
  muted = false,
  className = '',
}: {
  name: string;
  size?: keyof typeof avatarSizes;
  ring?: boolean;
  muted?: boolean;
  className?: string;
}) {
  return (
    <span
      aria-hidden="true"
      className={`inline-flex shrink-0 items-center justify-center rounded-full border font-semibold tracking-tight select-none ${avatarSizes[size]} ${
        ring
          ? 'border-brand bg-[color-mix(in_srgb,var(--brand)_14%,var(--surface))] text-text shadow-[0_0_0_3px_color-mix(in_srgb,var(--brand)_14%,transparent)]'
          : 'border-line-strong bg-sunken text-text-muted'
      } ${muted ? 'opacity-55' : ''} ${className}`}
    >
      {initialsOf(name)}
    </span>
  );
}

/** Header cell of a flat table: sentence case, muted, on the sunken band. */
export const th =
  'border-b border-line bg-sunken px-3 py-2.5 text-left sm:px-4 text-small font-medium whitespace-nowrap text-text-muted first:rounded-tl-inner last:rounded-tr-inner';
/** Body cell of a flat table. */
export const td = 'border-b border-line px-3 py-3 align-middle text-body-s text-text sm:px-4';

/** A flat, hairline table that scrolls sideways inside itself on narrow screens. */
export function FlatTable({
  caption,
  head,
  children,
  className = '',
}: {
  caption: string;
  head: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={`overflow-x-auto ${className}`}>
      <table className="w-full border-separate border-spacing-0">
        <caption className="sr-only">{caption}</caption>
        <thead>
          <tr>{head}</tr>
        </thead>
        <tbody className="[&>tr:last-child>td]:border-b-0">{children}</tbody>
      </table>
    </div>
  );
}

/** A problem with a form or an action, next to it: critical icon, plain text. */
export function Problem({ children, id }: { children: ReactNode; id?: string }) {
  return (
    <p id={id} role="alert" className="flex items-start gap-2 text-body-s text-text">
      <CircleAlert
        size={16}
        strokeWidth={1.75}
        aria-hidden="true"
        className="mt-0.5 shrink-0 text-fail"
      />
      <span>{children}</span>
    </p>
  );
}

/**
 * A neutral, monochrome GitLab glyph: a fox head drawn from flat facets. It
 * names the sign-in method; it is not the GitLab logo artwork.
 */
export function GitLabGlyph({ size = 18, className = '' }: { size?: number; className?: string }) {
  return (
    <svg
      viewBox="0 0 24 24"
      width={size}
      height={size}
      aria-hidden="true"
      focusable="false"
      className={`shrink-0 ${className}`}
    >
      <path
        fill="currentColor"
        d="M12 21.2 2.6 14.4a.9.9 0 0 1-.33-1L3.6 9.3l2.3-6.9a.4.4 0 0 1 .76 0l2.3 6.9h6.08l2.3-6.9a.4.4 0 0 1 .76 0l2.3 6.9 1.33 4.1a.9.9 0 0 1-.33 1Z"
        opacity="0.9"
      />
      <path fill="currentColor" d="M12 21.2 8.96 9.3h6.08Z" opacity="0.55" />
    </svg>
  );
}
