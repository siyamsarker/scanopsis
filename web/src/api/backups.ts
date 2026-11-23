// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, unwrap, type ApiError } from './client';
import { isRestoreOverlayShown } from './restoreMode';
import type { components } from './schema.gen';
import { restUploadEndpoints, type UploadEndpoints } from './upload';

export type BackupInfo = components['schemas']['BackupInfo'];
export type BackupKind = components['schemas']['BackupKind'];
export type BackupList = components['schemas']['BackupList'];
export type JobStatus = components['schemas']['JobStatus'];
export type RestorePreview = components['schemas']['RestorePreview'];
export type RestoreStatus = components['schemas']['RestoreStatus'];

export const backupsKey = ['admin', 'backups'] as const;

/** How often the list reloads while a backup runs, and otherwise. */
export const JOB_POLL_MS = 1000;
export const IDLE_POLL_MS = 30_000;

const running = (job: JobStatus | undefined) =>
  job?.state === 'backing_up' || job?.state === 'restoring';

/**
 * The backups, the current or last job, the schedule's last run, free space
 * and the folder. While a job runs the list reloads every second, so its
 * progress shows wherever the page is opened again; otherwise every half
 * minute, which picks up scheduled backups. Nothing reloads while the
 * restore screen is up, or with `poll` false (a glance from another page).
 */
export function useBackups(enabled: boolean, poll = true) {
  return useQuery({
    queryKey: backupsKey,
    enabled,
    refetchIntervalInBackground: false,
    refetchInterval: (query) => {
      if (!poll || isRestoreOverlayShown()) return false;
      return running(query.state.data?.job) ? JOB_POLL_MS : IDLE_POLL_MS;
    },
    queryFn: async () => unwrap(await api.GET('/api/v1/admin/backups')),
  });
}

export function useCreateBackup() {
  const queryClient = useQueryClient();
  return useMutation<JobStatus, ApiError, void>({
    mutationFn: async () => unwrap(await api.POST('/api/v1/admin/backups')),
    onSuccess: (job) => {
      // Show the job at once; the next reload brings its progress.
      queryClient.setQueryData<BackupList>(backupsKey, (old) => (old ? { ...old, job } : old));
      return queryClient.invalidateQueries({ queryKey: backupsKey });
    },
  });
}

export function useDeleteBackup() {
  const queryClient = useQueryClient();
  return useMutation<void, ApiError, string>({
    mutationFn: async (name) => {
      unwrap(await api.DELETE('/api/v1/admin/backups/{name}', { params: { path: { name } } }));
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: backupsKey }),
  });
}

/** What restoring the backup would do; the server reads the whole file for it. */
export function useRestorePreview(name: string) {
  return useQuery({
    queryKey: [...backupsKey, 'preview', name],
    staleTime: 0,
    gcTime: 0,
    queryFn: async () =>
      unwrap(await api.GET('/api/v1/admin/backups/{name}/preview', { params: { path: { name } } })),
  });
}

export interface RestoreRequest {
  name: string;
  code: string;
  skipSafetyBackup: boolean;
}

export function useRestoreBackup() {
  return useMutation<JobStatus, ApiError, RestoreRequest>({
    mutationFn: async ({ name, code, skipSafetyBackup }) =>
      unwrap(
        await api.POST('/api/v1/admin/backups/{name}/restore', {
          params: { path: { name } },
          body: { confirm: 'RESTORE', code, skip_safety_backup: skipSafetyBackup },
        }),
      ),
  });
}

/** The download route; a link to it downloads with the session cookie, straight to disk. */
export function backupDownloadUrl(name: string) {
  return `/api/v1/admin/backups/${encodeURIComponent(name)}/download`;
}

/** The admin upload routes: an upload completes as a backup of kind uploaded. */
export const adminUploadEndpoints: UploadEndpoints<BackupInfo> = restUploadEndpoints<BackupInfo>({
  base: '/api/v1/admin/backup-uploads',
});

/** The public restore status; answered while every other request is turned away. */
export async function fetchRestoreStatus(signal?: AbortSignal): Promise<RestoreStatus> {
  return unwrap(await api.GET('/api/v1/restore/status', { signal }));
}
