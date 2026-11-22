// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useQuery } from '@tanstack/react-query';
import { api, unwrap } from './client';
import type { components } from './schema.gen';

export type Portfolio = components['schemas']['Portfolio'];
export type PortfolioItem = components['schemas']['PortfolioItem'];
export type PortfolioHistory = components['schemas']['PortfolioHistory'];
export type ServiceHistory = PortfolioHistory['services'][number];

export const portfolioKey = ['portfolio'] as const;
export const portfolioHistoryKey = ['portfolio', 'history'] as const;

/** The per-service overview shown on the home screen. */
export function usePortfolio() {
  return useQuery({
    queryKey: portfolioKey,
    queryFn: async () => unwrap(await api.GET('/api/v1/portfolio')),
  });
}

/**
 * The recent pipelines of every service and the portfolio a week back, for
 * the sparks and deltas. Loaded after the cards, which do not wait for it.
 */
export function usePortfolioHistory(enabled = true) {
  return useQuery({
    queryKey: portfolioHistoryKey,
    enabled,
    queryFn: async () => unwrap(await api.GET('/api/v1/portfolio/history')),
    retry: false,
  });
}
