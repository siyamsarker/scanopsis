// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useQuery } from '@tanstack/react-query';
import { api, unwrap } from './client';
import type { components } from './schema.gen';

export type Scan = components['schemas']['Scan'];
export type ScanDiff = components['schemas']['ScanDiff'];
export type Vulnerability = components['schemas']['Vulnerability'];

export function useScan(id: number) {
  return useQuery({
    queryKey: ['scan', id],
    queryFn: async () => unwrap(await api.GET('/api/v1/scans/{id}', { params: { path: { id } } })),
  });
}

export function useScanDiff(id: number) {
  return useQuery({
    queryKey: ['scan', id, 'diff'],
    queryFn: async () =>
      unwrap(await api.GET('/api/v1/scans/{id}/diff', { params: { path: { id } } })),
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
