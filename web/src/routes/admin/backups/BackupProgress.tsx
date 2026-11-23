// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import type { JobStatus } from '../../../api/backups';
import { LensSpinner } from '../../../components/states';
import { When } from '../kit';
import { jobDetail, kindLabel } from './model';

/**
 * The running backup: a band with what is being copied and a progress bar.
 * It comes from the server's job, so it shows again when the page is opened
 * while the backup still runs.
 */
export function BackupProgress({ job }: { job: JobStatus }) {
  const value = Math.max(0, Math.min(100, Math.round(job.percent)));
  const title =
    job.type === 'restore'
      ? job.state === 'backing_up'
        ? 'Backing up the current state before a restore'
        : 'Restoring a backup'
      : job.kind
        ? `${kindLabel(job.kind)} backup running`
        : 'Backup running';
  return (
    <section
      aria-label="Running backup"
      className="tone-brand relative overflow-hidden rounded-panel border border-[color-mix(in_srgb,var(--tone)_30%,var(--line))] bg-[color-mix(in_srgb,var(--tone)_6%,var(--surface))]"
    >
      <span aria-hidden="true" className="tone-bg absolute inset-y-0 left-0 w-1" />
      <div className="flex items-center gap-4 py-4 pr-5 pl-6 sm:pr-6 sm:pl-7">
        <span className="hidden shrink-0 sm:block">
          <LensSpinner size={36} />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
            <h2 className="text-body font-semibold tracking-tight text-text">{title}</h2>
            <p className="text-small text-text-muted tabular-nums">
              {value}%
              {job.started_at ? (
                <>
                  {' '}
                  <span aria-hidden="true">·</span> <When at={job.started_at} prefix="started" />
                </>
              ) : null}
            </p>
          </div>
          <div
            role="progressbar"
            aria-label="Backup progress"
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={value}
            aria-valuetext={`${value}%, ${jobDetail(job)}`}
            className="mt-2.5 h-1.5 w-full overflow-hidden rounded-full bg-sunken shadow-[inset_0_0_0_1px_var(--line)]"
          >
            <div
              className="h-full rounded-full bg-brand transition-[width] duration-layout ease-enter"
              style={{ width: `${value}%` }}
            />
          </div>
          <p className="mt-2 truncate text-small text-text-muted tabular-nums">{jobDetail(job)}</p>
        </div>
      </div>
    </section>
  );
}
