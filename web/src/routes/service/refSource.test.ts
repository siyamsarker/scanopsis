// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { describeRefSource, refChoices } from './refSource';

describe('refChoices', () => {
  it('adds the shown ref when it has no scans', () => {
    expect(refChoices(['main'], 'release')).toEqual(['main', 'release']);
  });
  it('does not repeat a scanned ref or invent one', () => {
    expect(refChoices(['main', 'dev'], 'dev')).toEqual(['main', 'dev']);
    expect(refChoices(['main'], undefined)).toEqual(['main']);
  });
});

describe('describeRefSource', () => {
  it('names the dashboard branch and the latest scan', () => {
    expect(describeRefSource('default', 'main', 'main')).toBe('dashboard branch');
    expect(describeRefSource('latest', 'dev', null)).toBe(
      'latest scan (no dashboard branch chosen)',
    );
  });
  it('treats a selection of the dashboard branch as the dashboard branch', () => {
    expect(describeRefSource('requested', 'main', 'main')).toBe('dashboard branch');
  });
  it('explains another selected ref', () => {
    expect(describeRefSource('requested', 'dev', 'main')).toBe(
      'selected (the dashboard branch is main)',
    );
    expect(describeRefSource('requested', 'dev', null)).toBe('selected');
  });
});
