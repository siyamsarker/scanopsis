// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { QueryClient } from '@tanstack/react-query';

export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } },
  });
}
