// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { ApiError } from '../../api/client';
import { describeError } from '../../api/errors';

/** What a failed setup request says: the wrong token and a finished setup have their own words. */
export function setupMessage(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.code === 'invalid_setup_token') {
      return 'The setup token is not correct. Copy it again from the server log.';
    }
    if (err.code === 'setup_complete') {
      return 'Setup is already complete. Sign in with the administrator account.';
    }
  }
  return describeError(err);
}
