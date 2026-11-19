// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { describeCondition } from './conditions';

const base = { metric: 'new_violations', threshold: '5', value: '5', status: 'ERROR' };

describe('describeCondition operators', () => {
  it.each([
    ['EQ', '5 · must not equal 5'],
    ['EQUALS', '5 · must not equal 5'],
    ['NE', '5 · must equal 5'],
    ['NOT_EQUALS', '5 · must equal 5'],
    ['GT', '5 · must be at most 5'],
    ['SOMETHING_NEW', '5 · threshold SOMETHING_NEW 5'],
  ])('reads %s', (operator, want) => {
    expect(describeCondition({ ...base, operator })).toBe(want);
  });

  it('reports a missing value as not available', () => {
    expect(describeCondition({ ...base, operator: 'GT', value: null })).toBe('Not available');
    expect(describeCondition({ ...base, operator: 'GT', status: 'NO_VALUE' })).toBe(
      'Not available',
    );
  });

  it('keeps an out-of-range rating as reported', () => {
    expect(
      describeCondition({
        metric: 'new_security_rating',
        operator: 'GT',
        threshold: '1',
        value: '9',
        status: 'ERROR',
      }),
    ).toBe('9 · must be at most A');
  });
});
