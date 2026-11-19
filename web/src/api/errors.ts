// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { ApiError } from './client';

const network = 'Scanopsis could not reach the server. Check your connection and try again.';

/** A calm message for a failed request; 429 names the wait when the server gave one. */
export function describeError(err: unknown): string {
  if (!(err instanceof ApiError)) return network;
  if (err.status === 429) {
    return err.retryAfter === null
      ? 'Too many attempts. Try again later.'
      : `Too many attempts. Try again in ${err.retryAfter} ${err.retryAfter === 1 ? 'second' : 'seconds'}.`;
  }
  return err.message;
}
