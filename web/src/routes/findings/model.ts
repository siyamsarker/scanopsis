// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import type { Vulnerability } from '../../api/findings';
import {
  SEVERITY_ORDER,
  severityKey,
  type SeverityCountValues,
  type SeverityKey,
} from '../../components/severityModel';
import { RISK_WEIGHTS, riskPoints } from '../../viz/risk';

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

/** Zero counts, a fresh object each time. */
export const emptyCounts = (): SeverityCountValues => ({
  critical: 0,
  high: 0,
  medium: 0,
  low: 0,
  unknown: 0,
});

/** Findings per severity. */
export function countSeverities(rows: Vulnerability[]): SeverityCountValues {
  const counts = emptyCounts();
  for (const v of rows) counts[severityKey(v.severity)]++;
  return counts;
}

/** Risk points of one finding: its severity's weight. */
export const riskOf = (v: Vulnerability) => RISK_WEIGHTS[severityKey(v.severity)];

export interface UpgradeGroup {
  key: string;
  pkg: string;
  /** Trivy's package type (debian, npm, pip…), for the upgrade command. */
  pkgType: string;
  installed: string;
  upgradeTo: string;
  cleared: number;
  counts: SeverityCountValues;
  /** Risk points the upgrade clears. */
  risk: number;
  /** Distinct vulnerability IDs the upgrade clears, most severe first. */
  vulnIds: string[];
  artifacts: string[];
}

/**
 * Fixable, unsuppressed findings grouped by package and installed version,
 * ranked by the risk points the upgrade clears (critical×10, high×5…), then
 * by the number of findings and the package name.
 */
export function upgradeGroups(rows: Vulnerability[]): UpgradeGroup[] {
  const groups = new Map<string, Vulnerability[]>();
  for (const v of rows) {
    if (v.suppressed || !isFixable(v)) continue;
    const key = JSON.stringify([v.pkg_name, v.installed_version]);
    const list = groups.get(key);
    if (list) list.push(v);
    else groups.set(key, [v]);
  }
  return [...groups.entries()]
    .map(([key, items]): UpgradeGroup => {
      const counts = countSeverities(items);
      const bySeverity = [...items].sort(
        (a, b) => SEVERITY_RANK[severityKey(b.severity)] - SEVERITY_RANK[severityKey(a.severity)],
      );
      return {
        key,
        pkg: items[0].pkg_name,
        pkgType: items[0].pkg_type,
        installed: items[0].installed_version,
        upgradeTo: highestFixed(items.map((v) => v.fixed_version)),
        cleared: items.length,
        counts,
        risk: riskPoints(counts),
        vulnIds: [...new Set(bySeverity.map((v) => v.vuln_id))],
        artifacts: [...new Set(items.map((v) => v.artifact_name))].sort(),
      };
    })
    .sort(
      (a, b) =>
        b.risk - a.risk ||
        b.cleared - a.cleared ||
        a.pkg.localeCompare(b.pkg, 'en', { numeric: true }),
    );
}

/**
 * The fixed version to suggest for one finding: the lowest listed version
 * above the installed one (Trivy lists one per release line, "1.0.5, 1.1.2"),
 * else the highest listed. Empty when there is no fix.
 */
export function suggestedFix(installed: string, fixed: string): string {
  const all = fixed
    .split(',')
    .map((part) => part.trim())
    .filter(Boolean)
    .sort(compareVersions);
  return all.find((v) => compareVersions(v, installed) > 0) ?? all[all.length - 1] ?? '';
}

// Package names and versions come from uploaded reports. Only names made of
// these characters get a command: nothing in them means anything to a shell,
// and neither starts like an option, so a copied command cannot run anything
// but the package tool.
const SAFE = /^[A-Za-z0-9@._][A-Za-z0-9@._/:+~-]*$/;

const OS_APT = ['debian', 'ubuntu'];
const OS_APK = ['alpine', 'wolfi', 'chainguard'];
const OS_DNF = ['redhat', 'centos', 'rocky', 'alma', 'amazon', 'oracle', 'fedora', 'azurelinux'];

/**
 * A command that installs `version` of a package with the usual tool of its
 * ecosystem, or null when there is no common one. The version comes from the
 * scan; it is a hint to check, not something to run blindly.
 */
export function upgradeCommand(pkgType: string, pkg: string, version: string): string | null {
  if (!SAFE.test(pkg) || !SAFE.test(version)) return null;
  const type = pkgType.toLowerCase();
  if (OS_APT.includes(type)) return `apt-get install --only-upgrade ${pkg}=${version}`;
  if (OS_APK.includes(type)) return `apk add --upgrade ${pkg}=${version}`;
  if (OS_DNF.includes(type)) return `dnf upgrade ${pkg}-${version}`;
  switch (type) {
    case 'npm':
    case 'node-pkg':
      return `npm install ${pkg}@${version}`;
    case 'yarn':
      return `yarn add ${pkg}@${version}`;
    case 'pnpm':
      return `pnpm add ${pkg}@${version}`;
    case 'pip':
    case 'pipenv':
    case 'python-pkg':
    case 'uv':
      return `pip install ${pkg}==${version}`;
    case 'poetry':
      return `poetry add ${pkg}@${version}`;
    case 'gomod':
    case 'gobinary':
      return pkg === 'stdlib'
        ? null
        : `go get ${pkg}@${version.startsWith('v') ? version : `v${version}`}`;
    case 'cargo':
    case 'rust-binary':
      return `cargo update -p ${pkg} --precise ${version}`;
    case 'composer':
      return `composer require ${pkg}:${version}`;
    case 'nuget':
    case 'dotnet-core':
    case 'packages-props':
      return `dotnet add package ${pkg} --version ${version}`;
    case 'gemspec':
    case 'bundler':
      return `gem install ${pkg} -v ${version}`;
    case 'jar':
    case 'pom':
    case 'gradle':
    case 'sbt':
      return `${pkg}:${version}`;
  }
  return null;
}

export type GroupBy = 'none' | 'package' | 'vulnerability' | 'image';
export const GROUP_BY: readonly GroupBy[] = ['none', 'package', 'vulnerability', 'image'];

export function parseGroupBy(value: string | null): GroupBy {
  return (GROUP_BY as readonly string[]).includes(value ?? '') ? (value as GroupBy) : 'none';
}

export interface FindingGroup {
  key: string;
  /** What the rows share: a package, an ID or an image. */
  label: string;
  /** The installed version, for package groups. */
  version?: string;
  rows: Vulnerability[];
  counts: SeverityCountValues;
  risk: number;
  fixable: number;
  /** Package groups: the version that fixes every fixable finding of the group. */
  upgradeTo?: string;
  /** Vulnerability groups: the title. */
  title?: string;
}

function groupKey(v: Vulnerability, by: Exclude<GroupBy, 'none'>): [string, string] {
  if (by === 'package') return [JSON.stringify([v.pkg_name, v.installed_version]), v.pkg_name];
  if (by === 'vulnerability') return [v.vuln_id, v.vuln_id];
  return [v.artifact_name, v.artifact_name];
}

/**
 * Groups rows that are already sorted; rows keep their order inside a group.
 * Groups come by risk points, then size, then name.
 */
export function groupRows(rows: Vulnerability[], by: Exclude<GroupBy, 'none'>): FindingGroup[] {
  const groups = new Map<string, FindingGroup>();
  for (const v of rows) {
    const [key, label] = groupKey(v, by);
    let group = groups.get(key);
    if (!group) {
      group = { key, label, rows: [], counts: emptyCounts(), risk: 0, fixable: 0 };
      if (by === 'package') group.version = v.installed_version;
      groups.set(key, group);
    }
    group.rows.push(v);
    group.counts[severityKey(v.severity)]++;
    group.risk += riskOf(v);
    if (isFixable(v)) group.fixable++;
    if (by === 'vulnerability' && !group.title && v.title) group.title = v.title;
  }
  const list = [...groups.values()];
  if (by === 'package') {
    for (const g of list) {
      const fixes = g.rows.filter(isFixable).map((v) => v.fixed_version);
      if (fixes.length) g.upgradeTo = highestFixed(fixes);
    }
  }
  return list.sort(
    (a, b) =>
      b.risk - a.risk ||
      b.rows.length - a.rows.length ||
      a.label.localeCompare(b.label, 'en', { numeric: true }),
  );
}
