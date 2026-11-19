// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { ApiError, toApiError, unwrap } from './client';

function response(status: number, headers: Record<string, string> = {}) {
  return new Response(null, { status, headers });
}

describe('toApiError', () => {
  it('reads code and message from the Error schema', () => {
    const err = toApiError(response(409), {
      error: { code: 'last_admin', message: 'At least one admin must remain.' },
    });
    expect(err).toBeInstanceOf(ApiError);
    expect(err.status).toBe(409);
    expect(err.code).toBe('last_admin');
    expect(err.message).toBe('At least one admin must remain.');
    expect(err.retryAfter).toBeNull();
  });

  it('reads Retry-After as seconds', () => {
    expect(toApiError(response(429, { 'Retry-After': '42' }), {}).retryAfter).toBe(42);
  });

  it('ignores a Retry-After value that is not a number of seconds', () => {
    expect(
      toApiError(response(429, { 'Retry-After': 'Wed, 21 Oct 2026 07:28:00 GMT' }), {}).retryAfter,
    ).toBeNull();
  });

  it.each([undefined, '', 'text', { error: 'flat' }, { error: { code: 5 } }])(
    'falls back to a readable message for body %j',
    (body) => {
      const err = toApiError(response(502), body);
      expect(err.status).toBe(502);
      expect(err.code).toBe('unknown');
      expect(err.message).toBe('The server answered with status 502. Try again.');
    },
  );
});

describe('unwrap', () => {
  it('returns data for a successful response', () => {
    expect(unwrap({ data: { ok: 1 }, response: response(200) })).toEqual({ ok: 1 });
  });

  it('throws an ApiError for an error response', () => {
    const error = { error: { code: 'forbidden', message: 'Admins only.' } };
    expect(() => unwrap({ error, response: response(403) })).toThrow(
      expect.objectContaining({ status: 403, code: 'forbidden', message: 'Admins only.' }),
    );
  });
});
