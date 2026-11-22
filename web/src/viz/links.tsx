// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import type { ReactNode, Ref } from 'react';
import { Link } from 'react-router';
import { safeExternalUrl } from '../format';

// Any C0 control character or DEL; browsers strip or reinterpret them in URLs.
// eslint-disable-next-line no-control-regex
const CONTROL = /[\u0000-\u001f\u007f]/;

/**
 * The value when it is an internal app path: it starts with exactly one "/"
 * (never "//" or "/\", which browsers treat as another host) and contains no
 * control characters. Anything else gives undefined.
 */
export function internalPath(value: string | null | undefined): string | undefined {
  if (typeof value !== 'string' || !value.startsWith('/')) return undefined;
  if (value[1] === '/' || value[1] === '\\') return undefined;
  if (CONTROL.test(value)) return undefined;
  return value;
}

export interface SafeLinkProps {
  /** Data-derived target: an internal path or an http(s) URL; anything else renders as text. */
  href: string | null | undefined;
  className?: string;
  children?: ReactNode;
  tabIndex?: number;
  'aria-label'?: string;
  'data-tip'?: string;
  ref?: Ref<HTMLElement>;
}

/**
 * A link that never passes an unchecked value into an anchor. Internal paths
 * become router links, http and https URLs open with rel="noopener
 * noreferrer", and everything else (javascript:, data:, protocol-relative
 * "//host" or "/\host") renders as plain text.
 */
export function SafeLink({ href, ref, children, ...rest }: SafeLinkProps) {
  const path = internalPath(href);
  if (path) {
    return (
      <Link to={path} ref={ref as Ref<HTMLAnchorElement>} {...rest}>
        {children}
      </Link>
    );
  }
  const external = safeExternalUrl(href);
  if (external) {
    return (
      <a href={external} rel="noopener noreferrer" ref={ref as Ref<HTMLAnchorElement>} {...rest}>
        {children}
      </a>
    );
  }
  const { 'aria-label': label, className, 'data-tip': tip } = rest;
  return (
    <span ref={ref as Ref<HTMLSpanElement>} className={className} data-tip={tip} aria-label={label}>
      {children}
    </span>
  );
}

/** True when SafeLink would render an anchor for this value. */
export function isSafeHref(value: string | null | undefined): boolean {
  return Boolean(internalPath(value) ?? safeExternalUrl(value));
}
