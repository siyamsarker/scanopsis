// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import type { QueryClient } from '@tanstack/react-query';

/**
 * Every key Scanopsis keeps in the tab's sessionStorage starts with this:
 * unsaved form drafts and uploads to follow after a reload. None of them
 * holds a secret; the setup token is never stored.
 */
const PREFIX = 'scanopsis.';

/** The key of the unsaved backup schedule of the user `userId`. */
export function scheduleDraftKey(userId: number): string {
  return `${PREFIX}backup-schedule-draft.${userId}`;
}

/**
 * Forgets what this tab holds of a session that ended (sign-out, a 401, a
 * restore that signed everyone out): the query cache and every Scanopsis
 * entry of sessionStorage, so the next person on a shared browser sees
 * nothing of it.
 */
export function forgetSession(queryClient: QueryClient) {
  queryClient.clear();
  try {
    const keys: string[] = [];
    for (let i = 0; i < sessionStorage.length; i++) {
      const key = sessionStorage.key(i);
      if (key?.startsWith(PREFIX)) keys.push(key);
    }
    for (const key of keys) sessionStorage.removeItem(key);
  } catch {
    // Storage that cannot be used holds nothing to forget.
  }
}
