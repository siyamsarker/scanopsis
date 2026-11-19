// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { describeCondition, metricName } from './conditions';

const base = {
  metric: 'new_coverage',
  operator: 'LESS_THAN',
  threshold: '80',
  value: '62.0',
  status: 'ERROR',
};

describe('describeCondition', () => {
  it('reads a failing coverage condition in plain language', () => {
    expect(describeCondition(base)).toBe('62.0% · must be at least 80%');
    expect(describeCondition({ ...base, operator: 'LT' })).toBe('62.0% · must be at least 80%');
  });

  it('uses at most for GREATER_THAN and no percent for counts', () => {
    expect(
      describeCondition({
        metric: 'new_violations',
        operator: 'GREATER_THAN',
        threshold: '0',
        value: '3',
        status: 'ERROR',
      }),
    ).toBe('3 · must be at most 0');
  });

  it('shows ratings as letters', () => {
    expect(
      describeCondition({
        metric: 'new_security_rating',
        operator: 'GREATER_THAN',
        threshold: '1',
        value: '3',
        status: 'ERROR',
      }),
    ).toBe('C · must be at most A');
  });

  it('adds a percent sign for density and reviewed metrics only', () => {
    const c = { ...base, operator: 'GREATER_THAN' };
    expect(
      describeCondition({
        ...c,
        metric: 'new_duplicated_lines_density',
        threshold: '3',
        value: '7.5',
      }),
    ).toBe('7.5% · must be at most 3%');
    expect(describeCondition({ ...c, metric: 'new_security_hotspots_reviewed' })).toBe(
      '62.0% · must be at most 80%',
    );
    expect(describeCondition({ ...c, metric: 'new_lines', threshold: '5' })).toBe(
      '62.0 · must be at most 5',
    );
  });

  it('says Not available without a value', () => {
    expect(describeCondition({ ...base, value: null, status: 'NO_VALUE' })).toBe('Not available');
    expect(describeCondition({ ...base, status: 'NO_VALUE' })).toBe('Not available');
  });

  it('names known metrics and leaves unknown keys to the caller', () => {
    expect(metricName('new_maintainability_rating')).toBe('Maintainability rating on new code');
    expect(metricName('coverage')).toBe('Coverage');
    expect(metricName('sqale_index')).toBeUndefined();
  });
});
