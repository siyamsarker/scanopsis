// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { safeReturnTo } from './returnTo';

/** Values that try to leave the site; the server tests the same list. */
const bypasses = [
  '/\\evil.example',
  '/./\\evil.example',
  '/../\\evil.example',
  '/a/../\\evil.example',
  '/.\\evil.example',
  '/\\/evil.example',
  '/%5Cevil.example',
  '/%5cevil.example',
  '/./%5Cevil.example',
  '/../%5Cevil.example',
  '/%2E/%5Cevil.example',
  '/%2e%2e/%5cevil.example',
  '/%255Cevil.example',
  '/%252F%252Fevil.example',
  '/%2F%2Fevil.example',
  '/%2F/evil.example',
  '/%2f%2fevil.example',
  '/\t/evil.example',
  '/\n/evil.example',
  '/%09/evil.example',
  '/%0A/evil.example',
  '/%00/evil.example',
  '///evil.example',
  '////evil.example',
  '/.//evil.example',
  '/./../..//evil.example',
  '/..//evil.example',
  '//evil.example',
  '/\u007f/evil.example',
  'https:/evil.example',
  '/https://evil.example',
];

/** How a browser resolves a path against the page; a backslash counts as a slash. */
function resolvedHost(path: string): string {
  return new URL(path.replaceAll('\\', '/'), 'https://scanopsis.example.com').host;
}

describe('safeReturnTo', () => {
  it.each(['/', '/admin/users', '/services?page=2', '/admin/users/'])(
    'keeps the local path %s',
    (value) => {
      expect(safeReturnTo(value)).toBe(value);
    },
  );

  it.each([
    ['/services?page=2#top', '/services?page=2'],
    ['/a/./b/../c', '/a/c'],
    ['/services?q=%5C', '/services?q=%5C'],
    // Encoded twice: decodes once to "%5C", which a browser keeps as text.
    ['/%255Cevil.example', '/%255Cevil.example'],
  ])('normalises %j to %j', (value, want) => {
    expect(safeReturnTo(value)).toBe(want);
  });

  it.each([
    null,
    undefined,
    '',
    'admin',
    '//evil.com',
    'https://evil.com',
    '/\\evil.com',
    '/./\\evil.com',
    '/%5Cevil.com',
    '/\t/evil.com',
    '/a\nb',
    '/.//evil.com',
    `/${'a'.repeat(600)}`,
  ])('falls back to / for %j', (value) => {
    expect(safeReturnTo(value)).toBe('/');
  });

  it.each(bypasses)('keeps %j on the site', (value) => {
    const result = safeReturnTo(value);
    expect(result.startsWith('/')).toBe(true);
    expect(result.startsWith('//')).toBe(false);
    expect(result).not.toMatch(/[\\\t\r\n]/);
    expect(resolvedHost(result)).toBe('scanopsis.example.com');
  });
});
