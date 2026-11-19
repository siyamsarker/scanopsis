// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { compareVersions, highestFixed } from './model';

describe('compareVersions', () => {
  it('compares numbers numerically', () => {
    expect(compareVersions('1.2.10', '1.2.9')).toBeGreaterThan(0);
    expect(compareVersions('1.2.9', '1.2.10')).toBeLessThan(0);
    expect(compareVersions('2.0', '2.0.0')).toBeLessThan(0);
    expect(compareVersions('3.0.1', '3.0.1')).toBe(0);
  });

  it('compares text lexically and ranks pre-releases lower', () => {
    expect(compareVersions('1.0.0-beta', '1.0.0-alpha')).toBeGreaterThan(0);
    expect(compareVersions('1.2.3', '1.2.3-rc1')).toBeGreaterThan(0);
    expect(compareVersions('1.2.3-rc1', '1.2.3')).toBeLessThan(0);
  });
});

describe('highestFixed', () => {
  it('splits comma-separated lists and picks the highest', () => {
    expect(highestFixed(['3.0.9', '3.0.10, 3.1.2'])).toBe('3.1.2');
    expect(highestFixed(['1.2.9', '1.2.10'])).toBe('1.2.10');
    expect(highestFixed([])).toBe('');
  });
});
