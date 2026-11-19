// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, unwrap, type ApiError } from './client';
import { portfolioKey } from './portfolio';
import type { components } from './schema.gen';

export type Service = components['schemas']['Service'];
export type ServiceCreate = components['schemas']['ServiceCreate'];
export type ServiceUpdate = components['schemas']['ServiceUpdate'];
export type AuditEvent = components['schemas']['AuditEvent'];
export type TrivyIntegration = components['schemas']['TrivyIntegration'];
export type SonarIntegration = components['schemas']['SonarIntegration'];

/** How often the integration pages reload while the tab is visible. */
export const INTEGRATION_REFRESH_MS = 30_000;

const servicesKey = ['admin', 'services'] as const;

export interface ServiceFilters {
  q: string;
  archived: boolean;
}

/** Services in cursor pages; `enabled` is false for people who are not admins. */
export function useAdminServices(filters: ServiceFilters, enabled: boolean) {
  return useInfiniteQuery({
    queryKey: [...servicesKey, filters],
    enabled,
    initialPageParam: 0,
    queryFn: async ({ pageParam }) =>
      unwrap(
        await api.GET('/api/v1/services', {
          params: {
            query: {
              ...(filters.q ? { q: filters.q } : {}),
              archived: filters.archived,
              after: pageParam,
            },
          },
        }),
      ),
    getNextPageParam: (last) => last.next_after ?? undefined,
  });
}

export function useCreateService() {
  const queryClient = useQueryClient();
  return useMutation<Service, ApiError, ServiceCreate>({
    mutationFn: async (body) => unwrap(await api.POST('/api/v1/services', { body })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: servicesKey }),
  });
}

export function useUpdateService() {
  const queryClient = useQueryClient();
  return useMutation<Service, ApiError, { id: number; body: ServiceUpdate }>({
    mutationFn: async ({ id, body }) =>
      unwrap(await api.PATCH('/api/v1/services/{id}', { params: { path: { id } }, body })),
    // A changed dashboard branch changes what every dashboard of the service shows.
    onSuccess: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: servicesKey }),
        queryClient.invalidateQueries({ queryKey: portfolioKey }),
        queryClient.invalidateQueries({ queryKey: ['service'] }),
        queryClient.invalidateQueries({ queryKey: ['findings'] }),
      ]),
  });
}

export function useDeleteService() {
  const queryClient = useQueryClient();
  return useMutation<unknown, ApiError, number>({
    mutationFn: async (id) =>
      unwrap(await api.DELETE('/api/v1/services/{id}', { params: { path: { id } } })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: servicesKey }),
  });
}

const refresh = {
  refetchInterval: INTEGRATION_REFRESH_MS,
  refetchIntervalInBackground: false,
} as const;

export function useTrivyIntegration(enabled: boolean) {
  return useQuery({
    queryKey: ['admin', 'integrations', 'trivy'],
    enabled,
    ...refresh,
    queryFn: async () => unwrap(await api.GET('/api/v1/admin/integrations/trivy')),
  });
}

export function useSonarIntegration(enabled: boolean) {
  return useQuery({
    queryKey: ['admin', 'integrations', 'sonarqube'],
    enabled,
    ...refresh,
    queryFn: async () => unwrap(await api.GET('/api/v1/admin/integrations/sonarqube')),
  });
}

export interface AuditFilters {
  action: string;
  actor: string;
}

export function useAuditLog(filters: AuditFilters, enabled: boolean) {
  return useInfiniteQuery({
    queryKey: ['admin', 'audit', filters],
    enabled,
    initialPageParam: undefined as number | undefined,
    queryFn: async ({ pageParam }) =>
      unwrap(
        await api.GET('/api/v1/admin/audit', {
          params: {
            query: {
              ...(filters.action ? { action: filters.action } : {}),
              ...(filters.actor ? { actor: filters.actor } : {}),
              ...(pageParam !== undefined ? { before: pageParam } : {}),
            },
          },
        }),
      ),
    getNextPageParam: (last) => last.next_before ?? undefined,
  });
}
