// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { api, unwrap } from './client';
import type { components } from './schema.gen';

export type Service = components['schemas']['Service'];
export type Overview = components['schemas']['Overview'];
export type Trends = components['schemas']['Trends'];
export type ScanSummary = components['schemas']['ScanSummary'];
export type ServiceSonar = components['schemas']['ServiceSonar'];
export type SonarAnalysis = components['schemas']['SonarAnalysis'];

/** Sonar history entries requested for the coverage chart. */
export const SONAR_HISTORY_LIMIT = 60;

const keys = {
  service: (id: number) => ['service', id] as const,
  overview: (id: number, ref: string | undefined) =>
    ['service', id, 'overview', ref ?? ''] as const,
  trends: (id: number, ref: string | undefined, days: number) =>
    ['service', id, 'trends', ref ?? '', days] as const,
  scans: (id: number, ref: string | undefined) => ['service', id, 'scans', ref ?? ''] as const,
  sonar: (id: number) => ['service', id, 'sonar'] as const,
  scan: (scanId: number) => ['service', 'scan', scanId] as const,
};

export function useService(id: number) {
  return useQuery({
    queryKey: keys.service(id),
    queryFn: async () =>
      unwrap(await api.GET('/api/v1/services/{id}', { params: { path: { id } } })),
    retry: false,
  });
}

/**
 * Trivy posture for a ref. Without a ref the server picks the service's dashboard
 * branch, else the most recently scanned one; `ref_source` says which.
 */
export function useOverview(id: number, ref: string | undefined, enabled = true) {
  return useQuery({
    queryKey: keys.overview(id, ref),
    enabled,
    queryFn: async () =>
      unwrap(
        await api.GET('/api/v1/services/{id}/overview', {
          params: { path: { id }, query: { ref } },
        }),
      ),
    retry: false,
  });
}

export function useTrends(id: number, ref: string | undefined, days: number) {
  return useQuery({
    queryKey: keys.trends(id, ref, days),
    queryFn: async () =>
      unwrap(
        await api.GET('/api/v1/services/{id}/trends', {
          params: { path: { id }, query: { ref, days } },
        }),
      ),
    retry: false,
  });
}

export function useSonar(id: number) {
  return useQuery({
    queryKey: keys.sonar(id),
    queryFn: async () =>
      unwrap(
        await api.GET('/api/v1/services/{id}/sonar', {
          params: { path: { id }, query: { limit: SONAR_HISTORY_LIMIT } },
        }),
      ),
    retry: false,
  });
}

/** Scans newest first; fetchNextPage continues after next_before. */
export function useServiceScans(id: number, ref: string | undefined) {
  return useInfiniteQuery({
    queryKey: keys.scans(id, ref),
    initialPageParam: undefined as number | undefined,
    queryFn: async ({ pageParam }) =>
      unwrap(
        await api.GET('/api/v1/services/{id}/scans', {
          params: { path: { id }, query: { ref, before: pageParam } },
        }),
      ),
    getNextPageParam: (last) => last.next_before ?? undefined,
    retry: false,
  });
}

/** One scan with its GitLab links, used to find where the project lives in GitLab. */
export function useScanLinks(scanId: number | undefined) {
  return useQuery({
    queryKey: keys.scan(scanId ?? 0),
    enabled: scanId !== undefined,
    queryFn: async () =>
      unwrap(await api.GET('/api/v1/scans/{id}', { params: { path: { id: scanId as number } } })),
    retry: false,
  });
}
