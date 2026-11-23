// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import type {
  BackupInfo,
  BackupKind,
  JobStatus,
  RestorePreview,
  RestoreStatus,
} from '../../../api/backups';
import type { SelectOption } from '../../../components/inputs';
import { countOf, formatBytes, formatExactTime, formatInt, NO_VALUE } from '../../../format';

const kindLabels: Record<BackupKind, string> = {
  manual: 'Manual',
  scheduled: 'Scheduled',
  uploaded: 'Uploaded',
  'before-restore': 'Before restore',
};

export function kindLabel(kind: BackupKind): string {
  return kindLabels[kind] ?? kind;
}

/** The moment a backup was made, as row labels and dialog titles name it. */
export function backupDate(backup: BackupInfo): string {
  return formatExactTime(backup.created_at);
}

/** Who made a backup, in words: an account's username, the schedule or the setup screen. */
export function madeBy(createdBy: string): string {
  if (createdBy === 'scheduler') return 'Schedule';
  if (createdBy === 'setup') return 'Setup screen';
  if (!createdBy) return NO_VALUE;
  const colon = createdBy.indexOf(':');
  return colon >= 0 ? createdBy.slice(colon + 1) : createdBy;
}

export function isRunning(job: JobStatus | undefined): boolean {
  return job?.state === 'backing_up' || job?.state === 'restoring';
}

/** The progress of a job in one line: the table being copied, tables done of all, size so far. */
export function jobDetail(job: JobStatus): string {
  const { table, tables_done, tables_total, bytes } = job.progress;
  if (!table && tables_total === 0) return 'Starting';
  const parts = [];
  if (table) parts.push(`Copying ${table}`);
  if (tables_total > 0)
    parts.push(`${formatInt(tables_done)} of ${countOf(tables_total, 'table')}`);
  parts.push(`${formatBytes(bytes)} so far`);
  return parts.join(', ');
}

// ---- The schedule ----

export const MAX_TIMES = 24;
const TIME = /^([01]\d|2[0-3]):[0-5]\d$/;

/** Why a time cannot be added to the list, or undefined when it can. */
export function newTimeProblem(value: string, times: string[]): string | undefined {
  const time = value.trim();
  if (!TIME.test(time)) return 'Use a 24-hour time such as 02:00.';
  if (times.includes(time)) return `${time} is already in the list.`;
  if (times.length >= MAX_TIMES) return `A day can have at most ${MAX_TIMES} backup times.`;
  return undefined;
}

export const KEEP_DAYS_MIN = 1;
export const KEEP_DAYS_MAX = 365;

export function keepDaysProblem(text: string): string | undefined {
  const value = text.trim();
  const n = Number(value);
  if (value === '' || !Number.isInteger(n) || n < KEEP_DAYS_MIN || n > KEEP_DAYS_MAX) {
    return `Use a whole number from ${KEEP_DAYS_MIN} to ${KEEP_DAYS_MAX}.`;
  }
  return undefined;
}

export interface ScheduleProblems {
  times?: string;
  keepDays?: string;
}

/** Problems of a schedule the browser can find before saving it. */
export function scheduleProblems(draft: { times: string[]; keepDays: string }): ScheduleProblems {
  const problems: ScheduleProblems = {};
  if (draft.times.length === 0) problems.times = 'Add at least one time.';
  const keep = keepDaysProblem(draft.keepDays);
  if (keep) problems.keepDays = keep;
  return problems;
}

/** The browser's time zone, or UTC when it does not say. */
export function browserTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
  } catch {
    return 'UTC';
  }
}

function knownZones(): string[] {
  try {
    return Intl.supportedValuesOf('timeZone');
  } catch {
    return [];
  }
}

/**
 * Every time zone the browser knows, UTC first, with the browser's own zone
 * marked. The saved zone is listed even when the browser does not know it.
 */
export function timeZoneOptions(current: string, browser: string): SelectOption[] {
  const zones = new Set([...knownZones(), current, browser]);
  zones.delete('UTC');
  zones.delete('');
  const sorted = [...zones].sort((a, b) => a.localeCompare(b, 'en'));
  return ['UTC', ...sorted].map((zone) => ({
    value: zone,
    label: zone.replace(/_/g, ' '),
    hint: zone === browser && zone !== 'UTC' ? 'Your time zone' : undefined,
  }));
}

// ---- Restore ----

/**
 * Why the backup cannot be restored, or null when it can. The preview, once
 * loaded, has the final word on the key and the version.
 */
export function restoreBlocker(info: BackupInfo, preview?: RestorePreview): string | null {
  if (!info.readable) {
    return 'This backup cannot be read, so it cannot be restored. Delete it, or download it to look into it.';
  }
  if (!info.key_matches || (preview && !preview.key_matches)) {
    return preview
      ? 'This backup was made with another secret key, so this installation cannot read its encrypted settings and two-factor secrets. A restore needs the same secret key file (secrets/secret_key, or SCANOPSIS_SECRET_KEY) as the installation that made it.'
      : 'Made with another secret key';
  }
  if (preview && !preview.schema_ok) {
    return `This backup was made by a newer version of Scanopsis (${info.app_version}). Upgrade Scanopsis to that version or later, then restore it.`;
  }
  return null;
}

/**
 * The warning about scans that the first daily clean-up after the restore
 * deletes, or null when there are none. The count is an upper bound: the
 * clean-up keeps the latest scans of protected and dashboard branches. A
 * backup made with another key has no such numbers.
 */
export function retentionWarning(preview: RestorePreview): string | null {
  if (!preview.key_matches || preview.scans_removed_by_retention <= 0) return null;
  return `The next daily clean-up removes up to ${countOf(preview.scans_removed_by_retention, 'scan')} older than ${countOf(preview.retention_days, 'day')} from this backup. To keep them, raise the retention period on the Settings page right after the restore.`;
}

export const CONFIRM_PHRASE = 'RESTORE';

/** Digits of a two-factor code as typed, without spaces. */
export function cleanCode(code: string): string {
  return code.replace(/\s+/g, '');
}

export function canRestore(phrase: string, code: string): boolean {
  return phrase === CONFIRM_PHRASE && /^\d{6}$/.test(cleanCode(code));
}

// ---- The restore screen ----

/** The server's sentence for a restore that failed before it changed anything. */
export const RESTORE_FAILED_MESSAGE =
  'The restore failed and nothing was changed. The server log has the reason.';

export type OverlayView =
  | { kind: 'running'; percent: number; message: string }
  | { kind: 'done'; message: string }
  | { kind: 'failed'; message: string }
  | { kind: 'restart'; message: string }
  | { kind: 'idle' };

/**
 * What the restore screen shows for a status. A failed restore with any
 * other message than the plain failure committed the data and waits for a
 * restart: the server stays in restore mode, so there is no way back.
 */
export function overlayView(status: RestoreStatus): OverlayView {
  switch (status.state) {
    case 'backing_up':
    case 'restoring':
      return { kind: 'running', percent: status.percent, message: status.message };
    case 'done':
      return { kind: 'done', message: status.message };
    case 'failed':
      return status.message === RESTORE_FAILED_MESSAGE
        ? { kind: 'failed', message: status.message }
        : { kind: 'restart', message: status.message };
    default:
      return { kind: 'idle' };
  }
}

/**
 * The server's reason the folder cannot be used, as a sentence under a title
 * that already says so: "/srv/backups does not exist; create it ...".
 */
export function folderProblem(error: string | undefined): string {
  const text = (error ?? '').trim().replace(/^the backup folder cannot be used:\s*/i, '');
  if (!text) return 'Scanopsis cannot write to it. Check that it exists and is writable.';
  const sentence = text.charAt(0).toUpperCase() + text.slice(1);
  return /[.!?]$/.test(sentence) ? sentence : `${sentence}.`;
}

/** The banner text for a scheduled backup that failed. */
export function scheduleFailure(lastRunAt: string, error: string): string {
  return `The scheduled backup at ${formatExactTime(lastRunAt)} failed: ${error || 'the reason is in the server log.'}`;
}
