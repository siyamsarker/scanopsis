// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import createClient from 'openapi-fetch';
import { showRestoreOverlay } from './restoreMode';
import type { components, paths } from './schema.gen';

export type User = components['schemas']['User'];
export type Me = components['schemas']['Me'];
export type Role = components['schemas']['Role'];

// openapi-fetch builds a Request object, and Request rejects relative URLs
// outside a browser page context (jsdom, Node). The base URL is therefore the
// page's own origin, which is the same as a relative URL in the browser.
// The fetch wrapper looks up globalThis.fetch on every call, so the client
// does not capture the function that existed when this module was loaded.
export const api = createClient<paths>({
  baseUrl: globalThis.location?.origin ?? '',
  credentials: 'same-origin',
  fetch: (request) => globalThis.fetch(request),
});

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  /** Seconds from the Retry-After header, or null when absent or invalid. */
  readonly retryAfter: number | null;
  /** Bytes the server holds of an upload, sent with offset_mismatch and upload_incomplete. */
  received?: number;

  constructor(status: number, code: string, message: string, retryAfter: number | null = null) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.retryAfter = retryAfter;
  }
}

function parseRetryAfter(value: string | null): number | null {
  if (value === null || !/^\d+$/.test(value.trim())) return null;
  return Number(value.trim());
}

/**
 * Builds an ApiError from an error response and its parsed body. Bodies that
 * do not match the Error schema still produce a readable message.
 *
 * A 503 "restoring" means a backup is being restored and the server turns
 * every request away: it raises the full-screen restore screen.
 */
export function toApiError(response: Response, body: unknown): ApiError {
  let code = 'unknown';
  let message = `The server answered with status ${response.status}. Try again.`;
  let received: number | undefined;
  if (typeof body === 'object' && body !== null && 'error' in body) {
    const detail = (body as { error?: { code?: unknown; message?: unknown; received?: unknown } })
      .error;
    if (typeof detail?.code === 'string') code = detail.code;
    if (typeof detail?.message === 'string' && detail.message !== '') message = detail.message;
    if (typeof detail?.received === 'number') received = detail.received;
  }
  const error = new ApiError(
    response.status,
    code,
    message,
    parseRetryAfter(response.headers.get('Retry-After')),
  );
  if (received !== undefined) error.received = received;
  if (response.status === 503 && code === 'restoring') showRestoreOverlay();
  return error;
}

interface Result<T> {
  data?: T;
  error?: unknown;
  response: Response;
}

/** Returns the response data, or throws an ApiError for a non-2xx response. */
export function unwrap<T>(result: Result<T>): T {
  if (!result.response.ok) throw toApiError(result.response, result.error);
  return result.data as T;
}
