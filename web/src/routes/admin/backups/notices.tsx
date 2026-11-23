// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import type { ReactNode } from 'react';
import { Link } from 'react-router';
import { useDismissScheduleAlert, type ScheduleStatus } from '../../../api/backups';
import { describeError } from '../../../api/errors';
import { Spinner } from '../../../components/ConfirmDialog';
import { StatusMark, type StatusKind } from '../../../components/SeverityMark';
import { toast } from '../../../components/Toast';
import { buttonClass, linkClass } from '../../../ui';
import { scheduleAlertShown, scheduleFailure } from './model';

const tones: Record<StatusKind, string> = {
  ok: 'tone-ok',
  warn: 'tone-warn',
  fail: 'tone-fail',
  none: 'tone-neutral',
};

/** A full-width tinted strip that says what went wrong, named for screen readers by `label`. */
export function Notice({
  label,
  status = 'fail',
  title,
  children,
  action,
}: {
  label: string;
  status?: StatusKind;
  title?: ReactNode;
  children: ReactNode;
  action?: ReactNode;
}) {
  return (
    <section
      aria-label={label}
      className={`${tones[status]} relative overflow-hidden rounded-panel border border-[color-mix(in_srgb,var(--tone)_30%,var(--line))] bg-[color-mix(in_srgb,var(--tone)_7%,var(--surface))]`}
    >
      <span aria-hidden="true" className="tone-bg absolute inset-y-0 left-0 w-1" />
      <div className="flex flex-col gap-3 py-4 pr-5 pl-6 sm:flex-row sm:items-center sm:gap-6 sm:pr-6 sm:pl-7">
        <div className="flex min-w-0 flex-1 items-start gap-3">
          <StatusMark status={status} size={18} className="mt-0.5" />
          <div className="min-w-0 text-body-s text-text">
            {title ? <h2 className="text-body font-semibold tracking-tight">{title}</h2> : null}
            <div className={title ? 'mt-1 text-text-muted' : ''}>{children}</div>
          </div>
        </div>
        {action ? <div className="shrink-0 pl-7 sm:pl-0">{action}</div> : null}
      </div>
    </section>
  );
}

/**
 * "The scheduled backup at ... failed. ..." while the schedule is on and the
 * last scheduled backup failed, until an administrator dismisses it; else
 * nothing. Dismissing lasts until the next scheduled run.
 */
export function ScheduleFailedNotice({
  schedule,
  link = false,
}: {
  schedule: ScheduleStatus | undefined;
  /** Offer a link to the Backups page (from other pages). */
  link?: boolean;
}) {
  const dismiss = useDismissScheduleAlert();
  if (!schedule?.last_run_at || !scheduleAlertShown(schedule)) return null;
  return (
    <Notice
      label="Scheduled backup failed"
      action={
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
          {link ? (
            <Link to="/admin/backups" className={`${linkClass} text-body-s whitespace-nowrap`}>
              Open Backups
            </Link>
          ) : null}
          <button
            type="button"
            className={buttonClass('secondary', 'sm')}
            disabled={dismiss.isPending}
            aria-busy={dismiss.isPending || undefined}
            onClick={() =>
              dismiss.mutate(undefined, {
                onError: (err) =>
                  toast.error('Could not dismiss the notice', { description: describeError(err) }),
              })
            }
          >
            {dismiss.isPending ? <Spinner /> : null}
            Dismiss
          </button>
        </div>
      }
    >
      <p className="break-words">{scheduleFailure(schedule.last_run_at, schedule.error)}</p>
      <p className="mt-1 text-small text-text-muted">
        Dismissing hides this until the next scheduled backup.
      </p>
    </Notice>
  );
}
