// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { keepPreviousData, useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { api, unwrap } from './client';
import type { components } from './schema.gen';

export type Scan = components['schemas']['Scan'];
export type ScanDiff = components['schemas']['ScanDiff'];
export type ScanSummary = components['schemas']['ScanSummary'];
export type Vulnerability = components['schemas']['Vulnerability'];

export function useScan(id: number) {
  return useQuery({
    queryKey: ['scan', id],
    queryFn: async () => unwrap(await api.GET('/api/v1/scans/{id}', { params: { path: { id } } })),
  });
}

/**
 * The diff of a scan with its previous scan, or with `base`: any earlier
 * scan of the same service, artifact and kind. While another base loads the
 * last diff stays on screen (isPlaceholderData).
 */
export function useScanDiff(id: number, base: number | null = null) {
  return useQuery({
    queryKey: ['scan', id, 'diff', base],
    placeholderData: keepPreviousData,
    queryFn: async () =>
      unwrap(
        await api.GET('/api/v1/scans/{id}/diff', {
          params: { path: { id }, query: base === null ? {} : { base } },
        }),
      ),
  });
}

/** Earlier scans of the same artifact and kind on every ref, newest first, page by page. */
export function useEarlierScans(id: number, enabled: boolean) {
  return useInfiniteQuery({
    queryKey: ['scan', id, 'earlier'],
    enabled,
    initialPageParam: undefined as number | undefined,
    queryFn: async ({ pageParam }) =>
      unwrap(
        await api.GET('/api/v1/scans/{id}/earlier', {
          params: { path: { id }, query: { limit: 50, before: pageParam } },
        }),
      ),
    getNextPageParam: (last) => last.next_before ?? undefined,
  });
}

export function useScanService(serviceId: number | undefined) {
  return useQuery({
    queryKey: ['scan', 'service', serviceId],
    enabled: serviceId !== undefined,
    queryFn: async () =>
      unwrap(
        await api.GET('/api/v1/services/{id}', { params: { path: { id: serviceId as number } } }),
      ),
  });
}

/**
 * The id of the newest scan of the same artifact and kind on this scan's ref
 * when it is not this scan, otherwise null. A failed lookup is simply no notice.
 */
export function useNewerScanId(scan: Scan | undefined) {
  return useQuery({
    queryKey: ['scan', scan?.id, 'newer'],
    enabled: scan !== undefined,
    queryFn: async (): Promise<number | null> => {
      const s = scan as Scan;
      const page = unwrap(
        await api.GET('/api/v1/services/{id}/scans', {
          params: { path: { id: s.service_id }, query: { ref: s.ref, limit: 200 } },
        }),
      );
      const latest = page.items.find(
        (i) => i.artifact_name === s.artifact_name && i.kind === s.kind,
      );
      return latest && latest.id !== s.id ? latest.id : null;
    },
  });
}
