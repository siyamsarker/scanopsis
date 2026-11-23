// Copyright 2026 Siyam Sarker
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

/** The most rows the explorer loads before it stops and asks for narrower filters. */
export const MAX_ROWS = 50_000;

/** Filters the server applies itself; the rest (class, artifact) are client-side only. */
export interface ServerFilters {
  /** Comma-separated, upper case: "CRITICAL,HIGH". */
  severity?: string;
  fixable?: boolean;
  q?: string;
}

function hasServerFilters(f: ServerFilters | undefined): f is ServerFilters {
  return f !== undefined && (Boolean(f.severity) || f.fixable !== undefined || Boolean(f.q));
}

/** Pages of the current state, followed by `next_after` until done or `ceiling` rows. */
function usePagedVulnerabilities(
  id: number,
  ref: string | undefined,
  suppressed: boolean,
  filters: ServerFilters | undefined,
  enabled: boolean,
  ceiling: number,
) {
  const query = useInfiniteQuery({
    queryKey: [
      'findings',
      'vulnerabilities',
      id,
      ref ?? null,
      suppressed,
      filters?.severity ?? null,
      filters?.fixable ?? null,
      filters?.q ?? null,
    ],
    enabled: enabled && ref !== undefined,
    initialPageParam: undefined as number | undefined,
    queryFn: async ({ pageParam }) =>
      unwrap(
        await api.GET('/api/v1/services/{id}/vulnerabilities', {
          params: {
            path: { id },
            query: {
              ref,
              suppressed,
              severity: filters?.severity || undefined,
              fixable: filters?.fixable,
              q: filters?.q || undefined,
              limit: PAGE_SIZE,
              after: pageParam,
            },
          },
        }),
      ),
    getNextPageParam: (last) => last.next_after ?? undefined,
  });
  const { hasNextPage, isFetching, isError, fetchNextPage } = query;
  const loadedPages = query.data?.pages.length ?? 0;
  const loadedRows = useMemo(
    () => query.data?.pages.reduce((sum, page) => sum + page.items.length, 0) ?? 0,
    [query.data],
  );
  const truncated = hasNextPage && loadedRows >= ceiling;

  // A fast response can start and finish a fetch within one render, leaving
  // isFetching unchanged, so the number of loaded pages also triggers the next request.
  useEffect(() => {
    if (enabled && hasNextPage && !truncated && !isFetching && !isError) void fetchNextPage();
  }, [enabled, hasNextPage, truncated, isFetching, isError, fetchNextPage, loadedPages]);

  const rows = useMemo(() => {
    const all = query.data?.pages.flatMap((page) => page.items) ?? [];
    return all.length > ceiling ? all.slice(0, ceiling) : all;
  }, [query.data, ceiling]);
  return {
    rows,
    /** True until the first page arrived. */
    loading: query.isPending && query.fetchStatus !== 'idle',
    /** True while later pages are still being fetched. */
    loadingMore: !isError && !truncated && (hasNextPage || isFetching) && rows.length > 0,
    complete: query.data !== undefined && !hasNextPage && !isError,
    /** True when loading stopped at the ceiling although more rows exist. */
    truncated,
    error: query.error,
    retry: () => (query.data && hasNextPage ? void fetchNextPage() : void query.refetch()),
  };
}

/**
 * Every vulnerability of the current state, loaded by following `next_after`
 * page by page. Filtering, sorting and grouping happen on the complete list.
 * Loading stops after `ceiling` rows (50 000). Only then are the server-side
 * `filters` sent, so narrowing the filters really narrows what is loaded; below
 * the ceiling every filter stays exact on the client.
 */
export function useAllVulnerabilities(
  id: number,
  ref: string | undefined,
  suppressed: boolean,
  enabled: boolean,
  filters?: ServerFilters,
  ceiling = MAX_ROWS,
) {
  const all = usePagedVulnerabilities(id, ref, suppressed, undefined, enabled, ceiling);
  const narrow = all.truncated && hasServerFilters(filters);
  const narrowed = usePagedVulnerabilities(
    id,
    ref,
    suppressed,
    narrow ? filters : undefined,
    enabled && narrow,
    ceiling,
  );
  return narrow ? { ...narrowed, narrowed: true } : { ...all, narrowed: false };
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

/** Which refs a search covers: the dashboard branches and protected refs, or every ref. */
export type SearchScope = 'dashboard' | 'all';

export function useVulnerabilitySearch(vulnId: string | null, scope: SearchScope = 'dashboard') {
  return useQuery({
    queryKey: ['findings', 'search', vulnId, scope],
    enabled: vulnId !== null,
    queryFn: async () =>
      unwrap(
        await api.GET('/api/v1/search/vulnerabilities', {
          // The default scope is left out of the URL, as the API's default.
          params: { query: scope === 'all' ? { id: vulnId!, scope } : { id: vulnId! } },
        }),
      ),
  });
}
