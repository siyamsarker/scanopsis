// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { useEffect, useMemo } from 'react';
import { api, unwrap } from './client';
import type { components } from './schema.gen';

export type Vulnerability = components['schemas']['Vulnerability'];
export type SecretFinding = components['schemas']['SecretFinding'];
export type VulnerabilitySearch = components['schemas']['VulnerabilitySearch'];

/** Rows per request when loading the whole current state. */
export const PAGE_SIZE = 500;

export function useFindingsService(id: number) {
  return useQuery({
    queryKey: ['findings', 'service', id],
    queryFn: async () =>
      unwrap(await api.GET('/api/v1/services/{id}', { params: { path: { id } } })),
  });
}

/** The overview names the refs, the default ref and when the data was produced. */
export function useFindingsOverview(id: number, ref: string | undefined) {
  return useQuery({
    queryKey: ['findings', 'overview', id, ref ?? null],
    queryFn: async () =>
      unwrap(
        await api.GET('/api/v1/services/{id}/overview', {
          params: { path: { id }, query: { ref } },
        }),
      ),
  });
}

/**
 * Every vulnerability of the current state, loaded by following `next_after`
 * page by page. Filtering, sorting and grouping happen on the complete list.
 */
export function useAllVulnerabilities(
  id: number,
  ref: string | undefined,
  suppressed: boolean,
  enabled: boolean,
) {
  const query = useInfiniteQuery({
    queryKey: ['findings', 'vulnerabilities', id, ref ?? null, suppressed],
    enabled: enabled && ref !== undefined,
    initialPageParam: undefined as number | undefined,
    queryFn: async ({ pageParam }) =>
      unwrap(
        await api.GET('/api/v1/services/{id}/vulnerabilities', {
          params: {
            path: { id },
            query: { ref, suppressed, limit: PAGE_SIZE, after: pageParam },
          },
        }),
      ),
    getNextPageParam: (last) => last.next_after ?? undefined,
  });
  const { hasNextPage, isFetching, isError, fetchNextPage } = query;
  const loadedPages = query.data?.pages.length ?? 0;

  // A fast response can start and finish a fetch within one render, leaving
  // isFetching unchanged, so the number of loaded pages also triggers the next request.
  useEffect(() => {
    if (enabled && hasNextPage && !isFetching && !isError) void fetchNextPage();
  }, [enabled, hasNextPage, isFetching, isError, fetchNextPage, loadedPages]);

  const rows = useMemo(() => query.data?.pages.flatMap((page) => page.items) ?? [], [query.data]);
  return {
    rows,
    /** True until the first page arrived. */
    loading: query.isPending && query.fetchStatus !== 'idle',
    /** True while later pages are still being fetched. */
    loadingMore: !isError && (hasNextPage || isFetching) && rows.length > 0,
    complete: query.data !== undefined && !hasNextPage && !isError,
    error: query.error,
    retry: () => (query.data && hasNextPage ? void fetchNextPage() : void query.refetch()),
  };
}

export function useSecrets(id: number, ref: string | undefined, enabled: boolean) {
  return useQuery({
    queryKey: ['findings', 'secrets', id, ref ?? null],
    enabled: enabled && ref !== undefined,
    queryFn: async () =>
      unwrap(
        await api.GET('/api/v1/services/{id}/secrets', {
          params: { path: { id }, query: { ref } },
        }),
      ),
  });
}

export function useVulnerabilitySearch(vulnId: string | null) {
  return useQuery({
    queryKey: ['findings', 'search', vulnId],
    enabled: vulnId !== null,
    queryFn: async () =>
      unwrap(
        await api.GET('/api/v1/search/vulnerabilities', { params: { query: { id: vulnId! } } }),
      ),
  });
}
