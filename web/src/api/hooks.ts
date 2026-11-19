// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, unwrap, type ApiError, type Me, type Role, type User } from './client';

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
