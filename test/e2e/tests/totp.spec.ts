// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { expect, test } from '@playwright/test';
import { base32Decode, hotp, totpFromKey } from '../fixtures/totp';

// Plain checks of the helper against the published test vectors; no browser.

test.describe('TOTP helper', () => {
  test('matches the RFC 6238 SHA1 vectors', () => {
    const key = Buffer.from('12345678901234567890', 'ascii');
    const vectors: [number, string][] = [
      [59, '94287082'],
      [1111111109, '07081804'],
      [1111111111, '14050471'],
      [1234567890, '89005924'],
      [2000000000, '69279037'],
      [20000000000, '65353130'],
    ];
    for (const [time, expected] of vectors) {
      expect(totpFromKey(key, time, 8), `T=${time}`).toBe(expected);
    }
  });

  test('matches the RFC 4226 HOTP vectors', () => {
    const key = Buffer.from('12345678901234567890', 'ascii');
    const expected = ['755224', '287082', '359152', '969429', '338314'];
    expected.forEach((code, counter) => expect(hotp(key, counter)).toBe(code));
  });

  test('decodes base32 as in RFC 4648', () => {
    expect(base32Decode('').toString()).toBe('');
    expect(base32Decode('MY======').toString()).toBe('f');
    expect(base32Decode('MZXW6===').toString()).toBe('foo');
    expect(base32Decode('mzxw 6ytb oi').toString()).toBe('foobar');
    expect(() => base32Decode('1')).toThrow();
  });
});
