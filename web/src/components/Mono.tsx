// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { Fragment, type ReactNode } from 'react';
import { Link, type To } from 'react-router';
import { shortSha } from '../format';
import { CopyIconButton } from './CopyIconButton';

export interface MonoProps {
  /** The identifier, version, path or commit hash. */
  value: string;
  /** "sha" shows the first 8 characters; the full value stays in the tooltip and the copy. */
  kind?: 'sha';
  /** Adds a copy button that copies the full value. */
  copy?: boolean;
  /** What the value is, for the copy button's name: "Copy commit". Defaults to "Copy". */
  copyLabel?: string;
  /** Makes the value a link inside the app. */
  to?: To;
  /** Cuts long values with an ellipsis instead of wrapping; the full value is in the tooltip. */
  truncate?: boolean;
  /** A tooltip in place of the full value. */
  title?: string;
  className?: string;
}

/**
 * Offers a line break after every slash, so a long image name or path wraps
 * between its parts ("registry.example.com/platform/" then "payments-api:4.12.0")
 * and breaks inside a part only when that part alone is too wide.
 */
function withBreaks(value: string): ReactNode {
  const parts = value.split(/(?<=\/)/);
  if (parts.length < 2) return value;
  return parts.map((part, i) => (
    <Fragment key={i}>
      {i > 0 ? <wbr /> : null}
      {part}
    </Fragment>
  ));
}

/**
 * Identifiers, versions, package names, paths and commit hashes in JetBrains
 * Mono. Digits are tabular so columns of versions line up.
 */
export function Mono({
  value,
  kind,
  copy = false,
  copyLabel = 'Copy',
  to,
  truncate = false,
  title,
  className = '',
}: MonoProps) {
  const shown = kind === 'sha' ? shortSha(value) : value;
  const tip = title ?? (shown !== value || truncate ? value : undefined);
  const text = (
    <span
      title={tip}
      className={`font-mono tabular-nums ${truncate ? 'block max-w-full truncate' : 'wrap-anywhere'}`}
    >
      {truncate ? shown : withBreaks(shown)}
    </span>
  );
  return (
    <span
      className={`inline-flex min-w-0 max-w-full items-center gap-1 text-[0.93em] ${className}`}
    >
      {to !== undefined ? (
        <Link
          to={to}
          className="min-w-0 underline decoration-border-strong underline-offset-2 hover:decoration-text"
        >
          {text}
        </Link>
      ) : (
        text
      )}
      {copy ? <CopyIconButton text={value} label={copyLabel} /> : null}
    </span>
  );
}
