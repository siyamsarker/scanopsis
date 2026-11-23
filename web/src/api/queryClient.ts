// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { MutationCache, QueryCache, QueryClient } from '@tanstack/react-query';
import { ApiError } from './client';

const incompleteSession = new Set([
  'mfa_required',
  'mfa_enrollment_required',
  'password_change_required',
]);

export function createQueryClient(): QueryClient {
  // A 403 with one of these codes means the session is not complete. Reloading
  // the session state makes the protected layout send the user to the right step.
  const reload = (error: unknown) => {
    if (error instanceof ApiError && error.status === 403 && incompleteSession.has(error.code)) {
      void client.invalidateQueries({ queryKey: ['me'] });
    }
  };
  const client: QueryClient = new QueryClient({
    queryCache: new QueryCache({ onError: reload }),
    mutationCache: new MutationCache({ onError: reload }),
    defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } },
  });
  return client;
}
