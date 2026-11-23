// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import type {
  BackupInfo,
  BackupKind,
  JobStatus,
  RestorePreview,
  RestoreStatus,
  ScheduleStatus,
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

/**
 * The progress of a job in one line, under the percentage the server counts
 * in bytes: the table being copied or loaded, tables done of all, size so
 * far. Before the first table, what the job does.
 */
export function jobDetail(job: JobStatus): string {
  const { table, tables_done, tables_total, bytes } = job.progress;
  if (!table && tables_total === 0) return job.message || 'Starting';
  const parts = [];
  if (table) parts.push(`${job.type === 'restore' ? 'Loading' : 'Copying'} ${table}`);
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

/**
 * The current IANA names of zones that browsers still list by a former name
 * (Chromium and Node give "Asia/Calcutta" for Asia/Kolkata). The server
 * accepts both names; the page shows and saves the current one.
 */
const CURRENT_ZONE_NAMES: Record<string, string> = {
  'Africa/Asmera': 'Africa/Asmara',
  'America/Buenos_Aires': 'America/Argentina/Buenos_Aires',
  'America/Catamarca': 'America/Argentina/Catamarca',
  'America/Cordoba': 'America/Argentina/Cordoba',
  'America/Godthab': 'America/Nuuk',
  'America/Indianapolis': 'America/Indiana/Indianapolis',
  'America/Jujuy': 'America/Argentina/Jujuy',
  'America/Louisville': 'America/Kentucky/Louisville',
  'America/Mendoza': 'America/Argentina/Mendoza',
  'Asia/Calcutta': 'Asia/Kolkata',
  'Asia/Katmandu': 'Asia/Kathmandu',
  'Asia/Rangoon': 'Asia/Yangon',
  'Asia/Saigon': 'Asia/Ho_Chi_Minh',
  'Atlantic/Faeroe': 'Atlantic/Faroe',
  'Europe/Kiev': 'Europe/Kyiv',
  'Pacific/Enderbury': 'Pacific/Kanton',
  'Pacific/Ponape': 'Pacific/Pohnpei',
  'Pacific/Truk': 'Pacific/Chuuk',
};

/** Other names a zone is found by: its former name, and GMT for UTC. */
const OTHER_ZONE_NAMES: Record<string, string[]> = { UTC: ['GMT', 'Etc/UTC'] };
for (const [former, current] of Object.entries(CURRENT_ZONE_NAMES)) {
  (OTHER_ZONE_NAMES[current] ??= []).push(former);
}

/** The current name of a zone the browser may know by a former one ("Asia/Calcutta" gives "Asia/Kolkata"). */
export function currentZoneName(zone: string): string {
  return CURRENT_ZONE_NAMES[zone] ?? zone;
}

/** The browser's time zone by its current name, or UTC when it does not say. */
export function browserTimeZone(): string {
  try {
    return currentZoneName(Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC');
  } catch {
    return 'UTC';
  }
}

function knownZones(): string[] {
  try {
    return Intl.supportedValuesOf('timeZone').map(currentZoneName);
  } catch {
    return [];
  }
}

/** The words typed into a time zone filter, as lower-case parts to find. */
function filterWords(filter: string): string[] {
  return filter.toLowerCase().replace(/_/g, ' ').split(/\s+/).filter(Boolean);
}

/**
 * Whether `zone`, or one of its other names, holds every word of the filter
 * ("new york" finds America/New_York, "calcutta" finds Asia/Kolkata).
 */
export function zoneMatches(zone: string, filter: string): boolean {
  const words = filterWords(filter);
  return [zone, ...(OTHER_ZONE_NAMES[zone] ?? [])].some((other) => {
    const name = other.toLowerCase().replace(/_/g, ' ');
    return words.every((word) => name.includes(word));
  });
}

/**
 * The time zones the browser knows that match `filter` (all of them when it
 * is empty): the browser's own zone first and marked, then UTC, then the rest
 * in order. The saved zone is listed even when the browser does not know it
 * or the filter does not match it, so the field can still show it.
 */
export function timeZoneOptions(current: string, browser: string, filter = ''): SelectOption[] {
  current = currentZoneName(current);
  browser = currentZoneName(browser);
  const zones = new Set([...knownZones(), current, browser]);
  zones.delete('');
  const rest = [...zones]
    .filter((zone) => zone !== browser && zone !== 'UTC')
    .sort((a, b) => a.localeCompare(b, 'en'));
  const ordered = [...new Set([browser, 'UTC', ...rest])].filter(Boolean);
  return ordered
    .filter((zone) => zone === current || zoneMatches(zone, filter))
    .map((zone) => ({
      value: zone,
      label: zone.replace(/_/g, ' '),
      hint: zone === browser && zone !== 'UTC' ? 'Your time zone' : undefined,
    }));
}

// ---- Restore ----

/** What the list says about a backup that cannot be restored here. */
export type BlockedReason = 'newer' | 'unreadable' | 'key';

/** Why the backup cannot be restored, from the list alone, or null when nothing stops it. */
export function blockedReason(info: BackupInfo): BlockedReason | null {
  if (info.newer_version) return 'newer';
  if (!info.readable) return 'unreadable';
  if (!info.key_matches) return 'key';
  return null;
}

/** The short label of a blocked backup in the list. */
export const blockedLabels: Record<BlockedReason, string> = {
  newer: 'Made by a newer version of Scanopsis',
  unreadable: 'Cannot be read',
  key: 'Made with another secret key',
};

/** The words of the menu of a blocked backup, under its disabled Restore item. */
export const blockedHints: Record<BlockedReason, string> = {
  newer: 'Only that version of Scanopsis or a later one can restore it.',
  unreadable: 'Only a readable backup can be restored.',
  key: 'Only an installation with the same secret key can restore it.',
};

/** The sentence for a backup made with another secret key. */
export const KEY_MISMATCH =
  'This backup was made with another secret key and cannot be restored into this installation. To use it, set up a new installation with that secret key and restore the backup on its setup screen. Never change the secret key of an installation in use.';

/**
 * Why the backup cannot be restored, or null when it can. The preview, once
 * loaded, has the final word on the key and the version.
 */
export function restoreBlocker(info: BackupInfo, preview?: RestorePreview): string | null {
  if (info.newer_version || (preview && !preview.schema_ok)) {
    const version = info.readable && info.app_version ? ` (${info.app_version})` : '';
    return `This backup was made by a newer version of Scanopsis${version}. Upgrade Scanopsis to that version or later, then restore it.`;
  }
  if (!info.readable) {
    return 'This backup cannot be read, so it cannot be restored. Delete it, or download it to look into it.';
  }
  if (!info.key_matches || (preview && !preview.key_matches)) {
    return preview ? KEY_MISMATCH : blockedLabels.key;
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

export type OverlayView =
  | { kind: 'running'; percent: number; message: string }
  | { kind: 'done'; message: string }
  | { kind: 'failed'; message: string }
  | { kind: 'restart'; message: string }
  | { kind: 'idle' };

/**
 * What the restore screen shows for a status. A failed restore that committed
 * the data and waits for a restart (restart_required) has no way back: the
 * server turns every request away until it restarts.
 */
export function overlayView(status: RestoreStatus): OverlayView {
  switch (status.state) {
    case 'backing_up':
    case 'restoring':
      return { kind: 'running', percent: status.percent, message: status.message };
    case 'done':
      return { kind: 'done', message: status.message };
    case 'failed':
      return status.restart_required
        ? { kind: 'restart', message: status.message }
        : { kind: 'failed', message: status.message };
    default:
      return { kind: 'idle' };
  }
}

/**
 * The server's reason the folder cannot be used, as a sentence under a title
 * that already says so: "/srv/backups does not exist; create it ...".
 */
export function folderProblem(error: string | undefined): string {
  const text = asSentence((error ?? '').replace(/^\s*the backup folder cannot be used:\s*/i, ''));
  return text || 'Scanopsis cannot write to it. Check that it exists and is writable.';
}

/** A reason from the server as a sentence: capital first letter, full stop at the end. */
export function asSentence(text: string): string {
  const trimmed = text.trim();
  if (!trimmed) return '';
  const sentence = trimmed.charAt(0).toUpperCase() + trimmed.slice(1);
  return /[.!?]$/.test(sentence) ? sentence : `${sentence}.`;
}

/**
 * Whether the banner about a failed scheduled backup shows: only while the
 * schedule is on, and until an administrator dismisses it or a later
 * scheduled backup succeeds.
 */
export function scheduleAlertShown(schedule: ScheduleStatus | undefined): boolean {
  return Boolean(schedule?.enabled && schedule.last_run_at && !schedule.ok && !schedule.dismissed);
}

/** The banner text for a scheduled backup that failed. */
export function scheduleFailure(lastRunAt: string, error: string): string {
  return `The scheduled backup at ${formatExactTime(lastRunAt)} failed. ${asSentence(error) || 'The reason is in the server log.'}`;
}
