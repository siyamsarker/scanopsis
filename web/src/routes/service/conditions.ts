// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

/** Plain-language names for the quality gate metrics SonarQube commonly reports. */
const METRIC_NAMES: Record<string, string> = {
  coverage: 'Coverage',
  new_coverage: 'Coverage on new code',
  new_duplicated_lines_density: 'Duplicated lines on new code',
  new_violations: 'Issues on new code',
  new_security_hotspots_reviewed: 'Security hotspots reviewed on new code',
  new_reliability_rating: 'Reliability rating on new code',
  new_security_rating: 'Security rating on new code',
  new_maintainability_rating: 'Maintainability rating on new code',
};

/** The sentence-case name of a metric, or undefined when the key is not known. */
export function metricName(metric: string): string | undefined {
  return METRIC_NAMES[metric];
}

function isPercent(metric: string) {
  return /(coverage|density|_reviewed)$/.test(metric);
}

function isRating(metric: string) {
  return /_rating$/.test(metric);
}

/** A value as reported, with a percent sign or a letter grade where the metric has one. */
function display(metric: string, raw: string): string {
  if (isRating(metric)) {
    const n = Number.parseInt(raw, 10);
    if (n >= 1 && n <= 5) return 'ABCDE'[n - 1]!;
    return raw;
  }
  return isPercent(metric) ? `${raw}%` : raw;
}

/**
 * One sentence for a quality gate condition, such as "62.0% · must be at least 80%".
 * SonarQube reports the failing side of the comparison, so LESS_THAN means the
 * value has to be at least the threshold. Both the short (LT) and long
 * (LESS_THAN) operator names are understood.
 */
export function describeCondition(c: {
  metric: string;
  operator: string;
  threshold: string;
  value: string | null;
  status: string;
}): string {
  if (c.status === 'NO_VALUE' || c.value === null) return 'Not available';
  const value = display(c.metric, c.value);
  const threshold = display(c.metric, c.threshold);
  switch (c.operator) {
    case 'LT':
    case 'LESS_THAN':
      return `${value} · must be at least ${threshold}`;
    case 'GT':
    case 'GREATER_THAN':
      return `${value} · must be at most ${threshold}`;
    case 'EQ':
    case 'EQUALS':
      return `${value} · must not equal ${threshold}`;
    case 'NE':
    case 'NOT_EQUALS':
      return `${value} · must equal ${threshold}`;
    default:
      return `${value} · threshold ${c.operator} ${threshold}`;
  }
}

type Condition = Parameters<typeof describeCondition>[0];

/** The measured value as SonarQube reported it, or "Not available". */
export function conditionValue(c: Condition): string {
  if (c.status === 'NO_VALUE' || c.value === null) return 'Not available';
  return display(c.metric, c.value);
}

/** What the gate asks for: "at least 80%", "at most A". */
export function conditionRequirement(c: Condition): string {
  const threshold = display(c.metric, c.threshold);
  switch (c.operator) {
    case 'LT':
    case 'LESS_THAN':
      return `at least ${threshold}`;
    case 'GT':
    case 'GREATER_THAN':
      return `at most ${threshold}`;
    case 'EQ':
    case 'EQUALS':
      return `not ${threshold}`;
    case 'NE':
    case 'NOT_EQUALS':
      return `exactly ${threshold}`;
    default:
      return `${c.operator} ${threshold}`;
  }
}
