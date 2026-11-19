// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Accepts only local paths: a single leading "/" that is not followed by "/"
 * or "\", and no control characters (browsers strip tabs and newlines, which
 * could turn "/\t/host" into "//host"). Anything else becomes "/".
 */
export function safeReturnTo(value: string | null | undefined): string {
  if (typeof value !== 'string') return '/';
  if (!value.startsWith('/') || value.startsWith('//') || value.startsWith('/\\')) return '/';
  // eslint-disable-next-line no-control-regex
  if (/[\u0000-\u001f\u007f]/.test(value)) return '/';
  return value;
}
