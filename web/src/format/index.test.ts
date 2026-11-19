// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import {
  countOf,
  formatExactTime,
  formatInt,
  formatPercent,
  formatShortDate,
  plural,
  relativeTime,
  shortSha,
} from '.';

describe('formatInt', () => {
  it('separates thousands and never abbreviates under 10 000', () => {
    expect(formatInt(0)).toBe('0');
    expect(formatInt(999)).toBe('999');
    expect(formatInt(4217)).toBe('4,217');
    expect(formatInt(9999, { compact: true })).toBe('9,999');
    expect(formatInt(1234567)).toBe('1,234,567');
  });

  it('abbreviates from 10 000 only when asked', () => {
    expect(formatInt(12345, { compact: true })).toBe('12.3K');
    expect(formatInt(1_200_000, { compact: true })).toBe('1.2M');
    expect(formatInt(12345)).toBe('12,345');
  });

  it('shows a dash for missing values', () => {
    expect(formatInt(null)).toBe('–');
    expect(formatInt(undefined)).toBe('–');
    expect(formatInt(Number.NaN)).toBe('–');
  });
});

describe('formatPercent', () => {
  it('keeps one decimal at most and drops a trailing zero', () => {
    expect(formatPercent(84.23)).toBe('84.2%');
    expect(formatPercent(90)).toBe('90%');
    expect(formatPercent(0)).toBe('0%');
    expect(formatPercent(100)).toBe('100%');
  });

  it('supports ratios and other precision', () => {
    expect(formatPercent(0.4567, { ratio: true })).toBe('45.7%');
    expect(formatPercent(12.3456, { digits: 2 })).toBe('12.35%');
    expect(formatPercent(null)).toBe('–');
  });
});

describe('plural', () => {
  it('picks the word and builds counts', () => {
    expect(plural(1, 'service')).toBe('service');
    expect(plural(0, 'service')).toBe('services');
    expect(plural(2, 'vulnerability', 'vulnerabilities')).toBe('vulnerabilities');
    expect(countOf(1, 'finding')).toBe('1 finding');
    expect(countOf(1204, 'finding')).toBe('1,204 findings');
  });
});

describe('relativeTime', () => {
  const now = new Date('2026-11-19T12:00:00Z');
  const ago = (ms: number) => new Date(now.getTime() - ms);
  const min = 60_000;
  const hour = 60 * min;
  const day = 24 * hour;

  it.each([
    [10_000, 'just now'],
    [60_000, '1 minute ago'],
    [3 * min, '3 minutes ago'],
    [59 * min, '59 minutes ago'],
    [hour, '1 hour ago'],
    [5 * hour, '5 hours ago'],
    [day, '1 day ago'],
    [2 * day, '2 days ago'],
    [14 * day, '2 weeks ago'],
    [60 * day, '2 months ago'],
    [800 * day, '2 years ago'],
  ])('%d ms is %s', (ms, text) => {
    expect(relativeTime(ago(ms), now)).toBe(text);
  });

  it('handles the future and bad input', () => {
    expect(relativeTime(new Date(now.getTime() + 3 * min), now)).toBe('in 3 minutes');
    expect(relativeTime('nope', now)).toBe('–');
  });

  it('accepts ISO strings', () => {
    expect(relativeTime('2026-11-17T12:00:00Z', now)).toBe('2 days ago');
  });
});

describe('formatExactTime and shortSha', () => {
  it('formats the exact moment in a given zone', () => {
    expect(formatExactTime('2026-11-19T14:03:00Z', 'UTC')).toBe('19 Nov 2026, 14:03 UTC');
    expect(formatExactTime('bad')).toBe('–');
    expect(formatShortDate('2026-11-19T14:03:00Z', 'UTC')).toBe('19 Nov');
  });

  it('shortens commit hashes', () => {
    expect(shortSha('0123456789abcdef0123')).toBe('01234567');
    expect(shortSha('0123456789abcdef', 7)).toBe('0123456');
    expect(shortSha('abc')).toBe('abc');
  });
});
