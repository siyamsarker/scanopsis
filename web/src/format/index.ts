// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Number, time and identifier formatting shared by every screen. All output is
 * English with a comma as thousands separator, so tables line up and tests can
 * assert exact strings regardless of the machine locale.
 */

/** Shown wherever a value is missing or not a finite number. */
export const NO_VALUE = '–';

const intFormat = new Intl.NumberFormat('en-US', { maximumFractionDigits: 0 });
const compactFormat = new Intl.NumberFormat('en-US', {
  notation: 'compact',
  maximumFractionDigits: 1,
});

/**
 * An integer with thousands separators: 4217 becomes "4,217". Never abbreviated
 * below 10 000. With `compact`, values from 10 000 up read "12.3K" or "1.2M";
 * use that only where space is tight, such as treemap labels.
 */
export function formatInt(value: number | null | undefined, options?: { compact?: boolean }) {
  if (value == null || !Number.isFinite(value)) return NO_VALUE;
  if (options?.compact && Math.abs(value) >= 10_000) return compactFormat.format(value);
  return intFormat.format(Math.round(value));
}

/**
 * A percentage with at most `digits` decimals and no trailing zero: 84.2 becomes
 * "84.2%", 90 becomes "90%". `value` is on a 0 to 100 scale; pass `ratio: true`
 * for 0 to 1.
 */
export function formatPercent(
  value: number | null | undefined,
  options?: { digits?: number; ratio?: boolean },
) {
  if (value == null || !Number.isFinite(value)) return NO_VALUE;
  const scaled = options?.ratio ? value * 100 : value;
  const out = new Intl.NumberFormat('en-US', {
    minimumFractionDigits: 0,
    maximumFractionDigits: options?.digits ?? 1,
  }).format(scaled);
  return `${out}%`;
}

/** The singular or plural word for a count: plural(1, 'service') is "service". */
export function plural(count: number, one: string, other = `${one}s`) {
  return count === 1 ? one : other;
}

/** A count with its noun: countOf(1204, 'finding') is "1,204 findings". */
export function countOf(count: number, one: string, other?: string) {
  return `${formatInt(count)} ${plural(count, one, other)}`;
}

const SECOND = 1000;
const MINUTE = 60 * SECOND;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

function unit(n: number, name: string) {
  return `${n} ${name}${n === 1 ? '' : 's'}`;
}

/**
 * Elapsed time in words: "just now", "3 minutes ago", "2 days ago". Dates in
 * the future read "in 3 minutes". Invalid dates give the no-value dash.
 */
export function relativeTime(from: Date | string | number, now: Date | number = new Date()) {
  const then = new Date(from).getTime();
  if (Number.isNaN(then)) return NO_VALUE;
  const diff = new Date(now).getTime() - then;
  const abs = Math.abs(diff);
  if (abs < 45 * SECOND) return 'just now';

  let text: string;
  if (abs < HOUR) text = unit(Math.max(1, Math.round(abs / MINUTE)), 'minute');
  else if (abs < DAY) text = unit(Math.round(abs / HOUR), 'hour');
  else if (abs < 7 * DAY) text = unit(Math.round(abs / DAY), 'day');
  else if (abs < 30 * DAY) text = unit(Math.round(abs / (7 * DAY)), 'week');
  else if (abs < 365 * DAY) text = unit(Math.round(abs / (30 * DAY)), 'month');
  else text = unit(Math.round(abs / (365 * DAY)), 'year');
  return diff >= 0 ? `${text} ago` : `in ${text}`;
}

/**
 * The exact moment, for tooltips and title attributes: "19 Nov 2026, 14:03 UTC".
 * Uses the browser time zone unless `timeZone` is given.
 */
export function formatExactTime(value: Date | string | number, timeZone?: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return NO_VALUE;
  return new Intl.DateTimeFormat('en-GB', {
    day: 'numeric',
    month: 'short',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
    timeZoneName: 'short',
    timeZone,
  }).format(date);
}

/** A short date without time, for chart axes: "19 Nov". */
export function formatShortDate(value: Date | string | number, timeZone?: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return NO_VALUE;
  return new Intl.DateTimeFormat('en-GB', { day: 'numeric', month: 'short', timeZone }).format(
    date,
  );
}

/**
 * The URL itself when it is a well-formed http or https URL, otherwise undefined.
 * Use it for every link built from data (GitLab URLs, advisory links from uploaded
 * reports) so `javascript:`, `data:` and other schemes never become an anchor.
 */
export function safeExternalUrl(value: string | null | undefined): string | undefined {
  if (!value) return undefined;
  try {
    const url = new URL(value);
    return url.protocol === 'http:' || url.protocol === 'https:' ? value : undefined;
  } catch {
    return undefined;
  }
}

/** The leading characters of a commit hash, eight by default. */
export function shortSha(sha: string, length = 8) {
  return sha.slice(0, length);
}
