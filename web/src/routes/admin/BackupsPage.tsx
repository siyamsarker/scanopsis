// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { DatabaseBackup } from 'lucide-react';
import { useEffect, useId, useRef } from 'react';
import { useBackups, useCreateBackup, type JobStatus } from '../../api/backups';
import { describeError } from '../../api/errors';
import { useMe } from '../../api/hooks';
import { Spinner } from '../../components/ConfirmDialog';
import { EmptyState, ErrorState, Skeleton, SkeletonTable } from '../../components/states';
import { toast } from '../../components/Toast';
import { formatBytes } from '../../format';
import { buttonClass } from '../../ui';
import { AdminGate } from './AdminGate';
import { BackupList } from './backups/BackupList';
import { BackupProgress } from './backups/BackupProgress';
import { asSentence, folderProblem, isRunning } from './backups/model';
import { Notice, ScheduleFailedNotice } from './backups/notices';
import { ScheduleForm } from './backups/ScheduleForm';
import { UploadBackup } from './backups/UploadBackup';

/** Toasts for a backup this page saw running when it ends. */
function useJobToasts(job: JobStatus | undefined) {
  const previous = useRef<JobStatus | undefined>(undefined);
  useEffect(() => {
    const before = previous.current;
    previous.current = job;
    if (!job || !before || job.type !== 'backup') return;
    if (!isRunning(before) || isRunning(job)) return;
    if (job.state === 'done') {
      toast.success('Backup finished', {
        description: 'The new backup is at the top of the list.',
      });
    } else if (job.state === 'failed') {
      toast.error('The backup did not finish', { description: asSentence(job.message ?? '') });
    }
  }, [job]);
}

function BackupsBody({ onBackUp, canBackUp }: { onBackUp: () => void; canBackUp: boolean }) {
  const backups = useBackups(true);
  const headingId = useId();
  const scheduleId = useId();

  if (backups.isPending) {
    return (
      <div className="grid items-start gap-6 xl:grid-cols-[minmax(0,1fr)_22rem]">
        <div className="panel overflow-hidden">
          <SkeletonTable rows={4} columns={5} />
        </div>
        <Skeleton className="h-96 w-full" />
      </div>
    );
  }
  if (backups.isError) {
    return (
      <ErrorState
        title="Backups could not be loaded"
        message={describeError(backups.error)}
        onRetry={() => void backups.refetch()}
        retrying={backups.isFetching}
      />
    );
  }

  const { backups: items, job, schedule, disk, folder } = backups.data;
  const running = isRunning(job);
  // A failed scheduled backup has its own notice; every other failed job, restores too, gets this one.
  const lastFailed =
    job.state === 'failed' && !(job.type === 'backup' && job.kind === 'scheduled') ? job : null;

  return (
    <div className="flex flex-col gap-6">
      {!folder.ok ? (
        <Notice label="The backup folder cannot be used" title="The backup folder cannot be used">
          <p className="font-mono text-small break-all text-text">{folder.path}</p>
          <p className="mt-1">{folderProblem(folder.error)}</p>
        </Notice>
      ) : null}
      <ScheduleFailedNotice schedule={schedule} />
      {lastFailed ? (
        <Notice
          label="Last job failed"
          title={
            lastFailed.type === 'restore'
              ? 'The last restore did not finish. Nothing was changed.'
              : 'The last backup did not finish'
          }
        >
          <p className="break-words">{asSentence(lastFailed.message ?? '')}</p>
        </Notice>
      ) : null}
      {running ? <BackupProgress job={job} /> : null}

      <div className="grid items-start gap-6 xl:grid-cols-[minmax(0,1fr)_22rem]">
        <section aria-labelledby={headingId} className="min-w-0">
          <div className="flex flex-wrap items-end justify-between gap-x-4 gap-y-1 pb-3">
            <h2 id={headingId} className="text-h3 font-semibold tracking-tight text-text">
              Backup folder
            </h2>
            {folder.ok ? (
              <p className="min-w-0 text-small text-text-muted tabular-nums">
                {formatBytes(disk.free_bytes)} free in{' '}
                <span className="font-mono break-all text-text">{folder.path}</span>
              </p>
            ) : null}
          </div>
          <div className="panel overflow-hidden">
            {items.length === 0 ? (
              <EmptyState
                compact
                art="timeline"
                title="No backups yet"
                body={
                  folder.ok
                    ? 'A backup holds everything in Scanopsis in one file. Make the first one now, or turn on scheduled backups.'
                    : 'Backups can be made once Scanopsis can write to the backup folder.'
                }
                action={
                  <button
                    type="button"
                    className={buttonClass('primary')}
                    onClick={onBackUp}
                    disabled={!canBackUp}
                  >
                    Back up now
                  </button>
                }
              />
            ) : (
              <BackupList backups={items} busy={running} />
            )}
          </div>
          {items.length > 0 ? (
            <p className="mt-3 text-small text-text-muted tabular-nums">
              {items.length === 1 ? '1 backup' : `${items.length} backups`},{' '}
              {formatBytes(disk.used_bytes)} in all. Only scheduled backups are deleted
              automatically.
            </p>
          ) : null}
        </section>

        <section aria-labelledby={scheduleId} className="panel p-5 sm:p-6 xl:sticky xl:top-8">
          <div className="mb-5 flex items-center gap-2.5">
            <DatabaseBackup
              size={18}
              strokeWidth={1.75}
              aria-hidden="true"
              className="text-text-muted"
            />
            <h2 id={scheduleId} className="text-h3 font-semibold tracking-tight text-text">
              Scheduled backups
            </h2>
          </div>
          <ScheduleForm lastRun={schedule} />
        </section>
      </div>
    </div>
  );
}

export function BackupsPage() {
  const me = useMe();
  const isAdmin = me.data?.role === 'admin';
  const backups = useBackups(isAdmin);
  const create = useCreateBackup();
  useJobToasts(backups.data?.job);

  const running = isRunning(backups.data?.job);
  const folderOk = backups.data?.folder.ok ?? false;
  const canBackUp = backups.isSuccess && folderOk && !running && !create.isPending;

  function backUp() {
    create.mutate(undefined, {
      onSuccess: () =>
        toast.success('Backup started', {
          description: 'It runs in the background, so you can leave this page.',
        }),
      onError: (error) =>
        toast.error('The backup could not start', { description: describeError(error) }),
    });
  }

  return (
    <AdminGate
      title="Backups"
      subtitle="Back up everything in Scanopsis, keep backups on a schedule, and restore one when you need to."
      actions={
        <>
          <UploadBackup disabled={!backups.isSuccess || !folderOk || running} />
          <button
            type="button"
            className={buttonClass('primary')}
            onClick={backUp}
            disabled={!canBackUp}
            aria-busy={create.isPending || undefined}
          >
            {create.isPending ? <Spinner /> : null}
            Back up now
          </button>
        </>
      }
    >
      <BackupsBody onBackUp={backUp} canBackUp={canBackUp} />
    </AdminGate>
  );
}
