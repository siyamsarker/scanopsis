// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

/** The longest return path accepted; the server uses the same limit. */
const MAX_RETURN_TO = 512;

/** A placeholder origin to resolve against; it is never contacted. */
const BASE = 'http://scanopsis.invalid';

// eslint-disable-next-line no-control-regex
const unsafe = /[\\\u0000-\u001f\u007f]/;

function decoded(value: string): string | null {
  try {
    return decodeURIComponent(value);
  } catch {
    return null;
  }
}

/**
 * Accepts only local paths and returns their normalised path and query.
 * The value is resolved first, as the browser would resolve it, and checked
 * afterwards, so dot segments cannot turn an accepted value into another one
 * ("/./\host" would become "/\host", which browsers read as "//host"). A
 * backslash or a control character anywhere, raw or percent-encoded, and a
 * path that does not start with exactly one "/" give "/". The fragment is
 * dropped.
 */
export function safeReturnTo(value: string | null | undefined): string {
  if (typeof value !== 'string' || value.length > MAX_RETURN_TO) return '/';
  if (!value.startsWith('/') || value.startsWith('//') || unsafe.test(value)) return '/';
  let url: URL;
  try {
    url = new URL(value, BASE);
  } catch {
    return '/';
  }
  if (url.origin !== BASE || url.username !== '' || url.password !== '') return '/';
  const path = decoded(url.pathname);
  if (path === null || unsafe.test(path) || path.startsWith('//')) return '/';
  const result = url.pathname + url.search;
  if (result.startsWith('//') || unsafe.test(result)) return '/';
  return result;
}
