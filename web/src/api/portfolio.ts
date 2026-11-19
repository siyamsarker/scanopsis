// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useQuery } from '@tanstack/react-query';
import { api, unwrap } from './client';
import type { components } from './schema.gen';

export type Portfolio = components['schemas']['Portfolio'];
export type PortfolioItem = components['schemas']['PortfolioItem'];

export const portfolioKey = ['portfolio'] as const;

/** The per-service overview shown on the home screen. */
export function usePortfolio() {
  return useQuery({
    queryKey: portfolioKey,
    queryFn: async () => unwrap(await api.GET('/api/v1/portfolio')),
  });
}
