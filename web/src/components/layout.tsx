// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { ChevronRight } from 'lucide-react';
import { Fragment, useId, type ReactNode } from 'react';
import { Link, type To } from 'react-router';
import { Freshness, type FreshnessProps } from './Freshness';

export interface Crumb {
  label: ReactNode;
  /** Omit for the current page (the last crumb). */
  to?: To;
}

/** A breadcrumb trail, only where hierarchy exists (service, findings, scan). */
export function Breadcrumb({ items, className = '' }: { items: Crumb[]; className?: string }) {
  return (
    <nav aria-label="Breadcrumb" className={className}>
      <ol className="flex flex-wrap items-center gap-x-1 text-body-s text-text-muted">
        {items.map((item, index) => (
          <Fragment key={index}>
            {index > 0 ? (
              <li aria-hidden="true" className="text-text-faint">
                <ChevronRight size={14} strokeWidth={1.75} />
              </li>
            ) : null}
            <li aria-current={item.to === undefined ? 'page' : undefined} className="min-w-0">
              {item.to !== undefined ? (
                <Link
                  to={item.to}
                  className="rounded-[4px] transition-colors duration-micro hover:text-text"
                >
                  {item.label}
                </Link>
              ) : (
                <span className="text-text">{item.label}</span>
              )}
            </li>
          </Fragment>
        ))}
      </ol>
    </nav>
  );
}

/**
 * Page title row: the h1 (24/32), one context line, optional freshness of the
 * data, and right-aligned actions that wrap below on phones. `breadcrumb`
 * goes above the title, only where the page sits in a hierarchy.
 */
export function PageHeader({
  title,
  subtitle,
  freshness,
  actions,
  breadcrumb,
  eyebrow,
}: {
  title: ReactNode;
  /** The context line: what this page shows, or the object's key facts. */
  subtitle?: ReactNode;
  freshness?: FreshnessProps;
  actions?: ReactNode;
  breadcrumb?: Crumb[];
  /** Free-form content above the title; prefer `breadcrumb`. */
  eyebrow?: ReactNode;
}) {
  return (
    <header className="flex flex-wrap items-end justify-between gap-x-6 gap-y-4 pb-6 sm:pb-8">
      <div className="min-w-0">
        {breadcrumb ? <Breadcrumb items={breadcrumb} className="mb-2" /> : null}
        {eyebrow ? <div className="mb-2 text-body-s text-text-muted">{eyebrow}</div> : null}
        <h1 className="text-h2 font-semibold tracking-tight text-balance text-text">{title}</h1>
        {subtitle ? (
          <div className="mt-1 max-w-prose text-body-s text-text-muted">{subtitle}</div>
        ) : null}
        {freshness ? <Freshness {...freshness} className="mt-2" /> : null}
      </div>
      {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
    </header>
  );
}

/**
 * A top-level panel: 16 px radius, hairline border, first elevation. `padded`
 * (default) gives 24 px inner space; turn it off for tables and charts that go
 * edge to edge. Not every group needs one: tables and strips can sit on the
 * canvas.
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
    <Tag {...rest} className={`panel ${padded ? 'p-5 sm:p-6' : ''} ${className}`}>
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
        <Heading id={id} className="text-body font-semibold tracking-tight text-text">
          {title}
        </Heading>
        {description ? <p className="mt-0.5 text-body-s text-text-muted">{description}</p> : null}
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
    <section aria-labelledby={id} className={`mt-10 first:mt-0 ${className}`}>
      <div className="flex flex-wrap items-end justify-between gap-x-4 gap-y-2 pb-4">
        <div className="min-w-0">
          <h2 id={id} className="text-h3 font-semibold tracking-tight text-text">
            {title}
          </h2>
          {description ? <p className="mt-0.5 text-body-s text-text-muted">{description}</p> : null}
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
      className={`grid ${single ? 'grid-cols-1' : 'grid-cols-2'} gap-4 lg:gap-6 ${gridCols[cols]} ${className}`}
    >
      {children}
    </div>
  );
}
