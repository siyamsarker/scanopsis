// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useId, type ReactNode } from 'react';
import { Freshness, type FreshnessProps } from './Freshness';

/**
 * Page title row: the h1, an optional one-line subtitle, optional freshness of
 * the data, and an actions slot (buttons, filters) that wraps below on phones.
 */
export function PageHeader({
  title,
  subtitle,
  freshness,
  actions,
  eyebrow,
}: {
  title: ReactNode;
  subtitle?: ReactNode;
  freshness?: FreshnessProps;
  actions?: ReactNode;
  /** A small line above the title, such as a breadcrumb or the parent name. */
  eyebrow?: ReactNode;
}) {
  return (
    <header className="flex flex-wrap items-end justify-between gap-x-6 gap-y-4 pb-6">
      <div className="min-w-0">
        {eyebrow ? <div className="mb-1 text-sm text-text-muted">{eyebrow}</div> : null}
        <h1 className="text-2xl font-semibold tracking-tight text-text">{title}</h1>
        {subtitle ? <p className="mt-1 max-w-prose text-text-muted">{subtitle}</p> : null}
        {freshness ? <Freshness {...freshness} className="mt-2" /> : null}
      </div>
      {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
    </header>
  );
}

/**
 * The basic container: 12 px radius, 1 px border, flat. `padded` (default) gives
 * 24 px inner space; turn it off for tables and charts that go edge to edge.
 */
export function Card({
  children,
  padded = true,
  className = '',
  as: Tag = 'div',
  ...rest
}: {
  children: ReactNode;
  padded?: boolean;
  className?: string;
  as?: 'div' | 'section' | 'article' | 'li';
  'aria-labelledby'?: string;
  'aria-label'?: string;
}) {
  return (
    <Tag
      {...rest}
      className={`rounded-card border border-border bg-surface shadow-[inset_0_1px_0_color-mix(in_srgb,var(--text)_5%,transparent)] ${padded ? 'p-6' : ''} ${className}`}
    >
      {children}
    </Tag>
  );
}

/** A card's title row. Pair with `padded={false}` cards, or place inside a padded one. */
export function CardHeader({
  title,
  description,
  actions,
  level = 2,
  id,
  padded = false,
}: {
  /** One plain question or noun phrase: "Findings over time". */
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  level?: 2 | 3 | 4;
  /** Id of the heading, for aria-labelledby on the card. */
  id?: string;
  /** Adds the card's 24 px inset; use when the card itself is not padded. */
  padded?: boolean;
}) {
  const Heading = `h${level}` as const;
  return (
    <div
      className={`flex flex-wrap items-start justify-between gap-x-4 gap-y-2 ${padded ? 'px-6 pt-6' : ''} pb-4`}
    >
      <div className="min-w-0">
        <Heading id={id} className="text-base font-semibold tracking-tight text-text">
          {title}
        </Heading>
        {description ? <p className="mt-0.5 text-sm text-text-muted">{description}</p> : null}
      </div>
      {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
    </div>
  );
}

/** A titled group of content on a page with 32 px of space above. */
export function Section({
  title,
  description,
  actions,
  children,
  className = '',
}: {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  const id = useId();
  return (
    <section aria-labelledby={id} className={`mt-8 first:mt-0 ${className}`}>
      <div className="flex flex-wrap items-end justify-between gap-x-4 gap-y-2 pb-4">
        <div className="min-w-0">
          <h2 id={id} className="text-lg font-semibold tracking-tight text-text">
            {title}
          </h2>
          {description ? <p className="mt-0.5 text-text-muted">{description}</p> : null}
        </div>
        {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
      </div>
      {children}
    </section>
  );
}

const gridCols = {
  2: 'sm:grid-cols-2',
  3: 'sm:grid-cols-2 lg:grid-cols-3',
  4: 'sm:grid-cols-2 xl:grid-cols-4',
  5: 'sm:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-5',
} as const;

/**
 * A responsive grid on a 16 px gap for stat tiles: two columns on phones (one with
 * `single`, for tiles whose content cannot fit half a phone), `cols` on wider screens.
 */
export function Grid({
  cols = 3,
  single = false,
  children,
  className = '',
}: {
  cols?: keyof typeof gridCols;
  /** One column below the sm breakpoint instead of two. */
  single?: boolean;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div
      className={`grid ${single ? 'grid-cols-1' : 'grid-cols-2'} gap-4 ${gridCols[cols]} ${className}`}
    >
      {children}
    </div>
  );
}
