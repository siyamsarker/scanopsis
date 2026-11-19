// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { safeReturnTo } from './returnTo';

describe('safeReturnTo', () => {
  it.each(['/', '/admin/users', '/services?page=2#top'])('keeps the local path %s', (value) => {
    expect(safeReturnTo(value)).toBe(value);
  });

  it.each([
    null,
    undefined,
    '',
    'admin',
    '//evil.com',
    'https://evil.com',
    '/\\evil.com',
    '/\t/evil.com',
    '/a\nb',
  ])('falls back to / for %j', (value) => {
    expect(safeReturnTo(value)).toBe('/');
  });
});
