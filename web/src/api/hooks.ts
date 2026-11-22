// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import type { components } from './schema.gen';
import { api, ApiError, toApiError, unwrap, type Me, type Role, type User } from './client';

export const meKey = ['me'] as const;
const usersKey = ['users'] as const;

export const setupKey = ['setup'] as const;

/** Whether the first administrator still has to be created. */
export function useSetupStatus() {
  return useQuery({
    queryKey: setupKey,
    queryFn: async () => unwrap(await api.GET('/api/v1/setup')),
  });
}

/** The signed-in user and session state, or null when there is no session (401 is not an error). */
export function useMe() {
  return useQuery({
    queryKey: meKey,
    queryFn: async (): Promise<Me | null> => {
      const result = await api.GET('/api/v1/me');
      if (result.response.status === 401) return null;
      return unwrap(result);
    },
  });
}

export function useAuthMethods() {
  return useQuery({
    queryKey: ['auth-methods'],
    queryFn: async () => unwrap(await api.GET('/api/v1/auth/methods')),
  });
}

/** Users in pages of the server's default size; call fetchNextPage for more. */
export function useUsers() {
  return useInfiniteQuery({
    queryKey: usersKey,
    initialPageParam: 0,
    queryFn: async ({ pageParam }) =>
      unwrap(await api.GET('/api/v1/users', { params: { query: { after: pageParam } } })),
    getNextPageParam: (last) => last.next_after ?? undefined,
  });
}

export function useUpdateUserRole() {
  const queryClient = useQueryClient();
  return useMutation<User, ApiError, { id: number; role: Role }>({
    mutationFn: async ({ id, role }) =>
      unwrap(await api.PATCH('/api/v1/users/{id}', { params: { path: { id } }, body: { role } })),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: usersKey }),
        queryClient.invalidateQueries({ queryKey: meKey }),
      ]);
    },
  });
}

export function useResetUserMfa() {
  return useMutation<void, ApiError, number>({
    mutationFn: async (id) => {
      unwrap(await api.DELETE('/api/v1/users/{id}/mfa', { params: { path: { id } } }));
    },
  });
}

export type Setting = components['schemas']['Setting'];
export type SettingValue = components['schemas']['SettingUpdate'][string];

const settingsKey = ['settings'] as const;

export function useSettings(enabled = true) {
  return useQuery({
    enabled,
    queryKey: settingsKey,
    queryFn: async () => unwrap(await api.GET('/api/v1/settings')).items,
  });
}

/** A rejected settings change, with the problem found for each key. */
export class SettingsValidationError extends ApiError {
  readonly fields: Record<string, string>;

  constructor(base: ApiError, fields: Record<string, string>) {
    super(base.status, base.code, base.message, base.retryAfter);
    this.name = 'SettingsValidationError';
    this.fields = fields;
  }
}

export function useUpdateSettings() {
  const queryClient = useQueryClient();
  return useMutation<Setting[], ApiError, Record<string, SettingValue>>({
    mutationFn: async (changes) => {
      const result = await api.PATCH('/api/v1/settings', { body: changes });
      if (result.response.ok) return result.data!.items;
      const error = toApiError(result.response, result.error);
      const fields = (result.error as { error?: { fields?: unknown } } | undefined)?.error?.fields;
      if (fields && typeof fields === 'object') {
        throw new SettingsValidationError(error, fields as Record<string, string>);
      }
      throw error;
    },
    onSuccess: (items) => {
      queryClient.setQueryData(settingsKey, items);
    },
  });
}

export type MySession = components['schemas']['Session'];

const mySessionsKey = ['me', 'sessions'] as const;

/** The signed-in user's sessions that still work, most recently used first. */
export function useMySessions() {
  return useQuery({
    queryKey: mySessionsKey,
    queryFn: async () => unwrap(await api.GET('/api/v1/me/sessions')).items,
  });
}

/** Ends one other session of the signed-in user. */
export function useEndSession() {
  const queryClient = useQueryClient();
  return useMutation<void, ApiError, number>({
    mutationFn: async (id) => {
      unwrap(await api.DELETE('/api/v1/me/sessions/{id}', { params: { path: { id } } }));
    },
    onSettled: () => queryClient.invalidateQueries({ queryKey: mySessionsKey }),
  });
}

/** Ends every session of the signed-in user except this one; resolves to how many ended. */
export function useEndOtherSessions() {
  const queryClient = useQueryClient();
  return useMutation<number, ApiError, void>({
    mutationFn: async () => unwrap(await api.DELETE('/api/v1/me/sessions')).ended,
    onSettled: () => queryClient.invalidateQueries({ queryKey: mySessionsKey }),
  });
}
