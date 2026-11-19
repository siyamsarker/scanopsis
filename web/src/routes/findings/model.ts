// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import type { Vulnerability } from '../../api/findings';
import {
  SEVERITY_ORDER,
  severityKey,
  type SeverityCountValues,
  type SeverityKey,
} from '../../components/severityModel';

export const SEVERITY_RANK: Record<SeverityKey, number> = {
  critical: 4,
  high: 3,
  medium: 2,
  low: 1,
  unknown: 0,
};

export const isFixable = (v: Vulnerability) => v.fixed_version.trim() !== '';

export type VulnClass = 'os-pkgs' | 'lang-pkgs' | 'other';
export const vulnClass = (v: Vulnerability): VulnClass =>
  v.class === 'os-pkgs' || v.class === 'lang-pkgs' ? v.class : 'other';

export interface Filters {
  severity: SeverityKey[];
  /** 'true' keeps fixable, 'false' keeps findings without a fix, null keeps both. */
  fixable: 'true' | 'false' | null;
  classes: VulnClass[];
  artifacts: string[];
  q: string;
}

const CLASSES: VulnClass[] = ['os-pkgs', 'lang-pkgs', 'other'];

const list = (value: string | null) =>
  (value ?? '')
    .split(',')
    .map((part) => part.trim())
    .filter(Boolean);

/** Reads the filters from URL params; unknown values are ignored. */
export function parseFilters(params: URLSearchParams): Filters {
  const fixable = params.get('fixable');
  return {
    severity: list(params.get('severity'))
      .map((s) => s.toLowerCase())
      .filter((s): s is SeverityKey => (SEVERITY_ORDER as readonly string[]).includes(s)),
    fixable: fixable === 'true' || fixable === 'false' ? fixable : null,
    classes: list(params.get('class')).filter((c): c is VulnClass =>
      (CLASSES as string[]).includes(c),
    ),
    artifacts: list(params.get('artifact')),
    q: params.get('q')?.trim() ?? '',
  };
}

type Facet = 'severity' | 'fixable' | 'classes' | 'artifacts';

/** Applies the filters; `skip` leaves one facet out, to count what its chips would show. */
export function applyFilters(rows: Vulnerability[], f: Filters, skip?: Facet): Vulnerability[] {
  const q = f.q.toLowerCase();
  return rows.filter((v) => {
    if (skip !== 'severity' && f.severity.length && !f.severity.includes(severityKey(v.severity)))
      return false;
    if (skip !== 'fixable' && f.fixable !== null && isFixable(v) !== (f.fixable === 'true'))
      return false;
    if (skip !== 'classes' && f.classes.length && !f.classes.includes(vulnClass(v))) return false;
    if (skip !== 'artifacts' && f.artifacts.length && !f.artifacts.includes(v.artifact_name))
      return false;
    if (
      q &&
      !(
        v.pkg_name.toLowerCase().includes(q) ||
        v.vuln_id.toLowerCase().includes(q) ||
        v.title.toLowerCase().includes(q)
      )
    )
      return false;
    return true;
  });
}

export type SortKey = 'severity' | 'package' | 'seen';
export interface Sort {
  key: SortKey;
  direction: 'asc' | 'desc';
}
/** Critical first, then fixable first. */
export const DEFAULT_SORT: Sort = { key: 'severity', direction: 'desc' };

/** Sorts a copy. Ties fall back to fixable first, package name and id, so the order is stable. */
export function sortVulns(rows: Vulnerability[], sort: Sort): Vulnerability[] {
  const sign = sort.direction === 'asc' ? 1 : -1;
  const byPackage = (a: Vulnerability, b: Vulnerability) =>
    a.pkg_name.localeCompare(b.pkg_name, 'en', { numeric: true }) ||
    a.vuln_id.localeCompare(b.vuln_id, 'en', { numeric: true }) ||
    a.id - b.id;
  const primary = (a: Vulnerability, b: Vulnerability) => {
    if (sort.key === 'severity')
      return SEVERITY_RANK[severityKey(a.severity)] - SEVERITY_RANK[severityKey(b.severity)];
    if (sort.key === 'package')
      return a.pkg_name.localeCompare(b.pkg_name, 'en', { numeric: true });
    return Date.parse(a.first_seen_at) - Date.parse(b.first_seen_at);
  };
  return [...rows].sort((a, b) => {
    const p = primary(a, b);
    if (p !== 0) return sign * p;
    // The secondary order does not flip with the direction of the primary key.
    return Number(isFixable(b)) - Number(isFixable(a)) || byPackage(a, b);
  });
}

export function countBy<T>(rows: T[], key: (row: T) => string): Map<string, number> {
  const counts = new Map<string, number>();
  for (const row of rows) counts.set(key(row), (counts.get(key(row)) ?? 0) + 1);
  return counts;
}

const TOKEN = /\d+|[A-Za-z]+/g;
const isNumber = (token: string) => /^\d/.test(token);

/**
 * Numeric-aware version comparison: versions are split into runs of digits and
 * runs of letters (punctuation separates), numbers compare numerically
 * (1.2.10 > 1.2.9) and text lexically. A trailing text part marks a
 * pre-release (1.2.3 > 1.2.3rc1). Ecosystem rules differ and are not modelled:
 * Debian epochs ("1:2.0") and tilde ordering ("1.0~rc1"), RPM release
 * numbers and semver build metadata are all compared as plain tokens.
 */
export function compareVersions(a: string, b: string): number {
  const x = a.match(TOKEN) ?? [];
  const y = b.match(TOKEN) ?? [];
  for (let i = 0; i < Math.max(x.length, y.length); i++) {
    const p = x[i];
    const q = y[i];
    if (p === undefined) return isNumber(q) ? -1 : 1;
    if (q === undefined) return isNumber(p) ? 1 : -1;
    if (isNumber(p) && isNumber(q)) {
      const d = Number(p) - Number(q);
      if (d !== 0) return d < 0 ? -1 : 1;
    } else if (isNumber(p) !== isNumber(q)) {
      return isNumber(p) ? 1 : -1;
    } else if (p !== q) {
      return p < q ? -1 : 1;
    }
  }
  return 0;
}

/** The highest version in FixedVersion values, each of which may be a comma-separated list. */
export function highestFixed(values: string[]): string {
  const all = values.flatMap((v) => v.split(',').map((part) => part.trim())).filter(Boolean);
  return all.reduce((best, v) => (compareVersions(v, best) > 0 ? v : best), all[0] ?? '');
}

export interface UpgradeGroup {
  key: string;
  pkg: string;
  installed: string;
  upgradeTo: string;
  cleared: number;
  counts: SeverityCountValues;
}

/** Fixable, unsuppressed findings grouped by package and installed version, biggest win first. */
export function upgradeGroups(rows: Vulnerability[]): UpgradeGroup[] {
  const groups = new Map<string, Vulnerability[]>();
  for (const v of rows) {
    if (v.suppressed || !isFixable(v)) continue;
    const key = JSON.stringify([v.pkg_name, v.installed_version]);
    groups.set(key, [...(groups.get(key) ?? []), v]);
  }
  return [...groups.entries()]
    .map(([key, items]): UpgradeGroup => {
      const counts = { critical: 0, high: 0, medium: 0, low: 0, unknown: 0 };
      for (const v of items) counts[severityKey(v.severity)]++;
      return {
        key,
        pkg: items[0].pkg_name,
        installed: items[0].installed_version,
        upgradeTo: highestFixed(items.map((v) => v.fixed_version)),
        cleared: items.length,
        counts,
      };
    })
    .sort(
      (a, b) =>
        b.counts.critical - a.counts.critical ||
        b.counts.high - a.counts.high ||
        b.cleared - a.cleared ||
        a.pkg.localeCompare(b.pkg, 'en', { numeric: true }),
    );
}
