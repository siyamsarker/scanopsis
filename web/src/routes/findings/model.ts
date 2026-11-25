// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import type { Vulnerability } from '../../api/findings';
import {
  SEVERITY_ORDER,
  severityKey,
  type SeverityCountValues,
  type SeverityKey,
} from '../../components/severityModel';
import { artifactLabel } from '../../format';
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
  /** Artifacts (see artifactIdOf); a name from an older link until resolveFilters. */
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

/**
 * A finding, with the kind of the scan it is in when that is not an image. A
 * finding names only the artifact key, and a secret scan of a folder may
 * have the key of an image; the kind keeps the two apart.
 */
export type Finding = Vulnerability & { scan_kind?: string };

/**
 * Marks the findings of scans that are not images with their kind, from the
 * current scans of the ref. The same array when every scan is an image.
 */
export function withScanKinds(
  rows: Vulnerability[],
  scans: readonly { id: number; kind: string }[] | undefined,
): Finding[] {
  const kinds = new Map((scans ?? []).filter((s) => s.kind !== 'image').map((s) => [s.id, s.kind]));
  if (kinds.size === 0) return rows;
  return rows.map((v) => {
    const kind = kinds.get(v.scan_id);
    return kind ? { ...v, scan_kind: kind } : v;
  });
}

/** The artifact of a finding: its key for an image, else "<kind>:<key>". */
export function artifactIdOf(v: Finding): string {
  return v.scan_kind ? `${v.scan_kind}:${v.artifact_key}` : v.artifact_key;
}

/** How the artifact of a finding is named: its key, a secret scan marked as one. */
export function artifactLabelOf(v: Finding): string {
  if (!v.scan_kind) return v.artifact_key;
  const label = artifactLabel(v.artifact_key, v.scan_kind);
  return label === v.artifact_key ? `${label} (secret scan)` : label;
}

/**
 * The image an image name belongs to, as the server derives it: the name
 * without its digest, then without the tag of its last part (a registry port
 * is not a tag).
 */
export function imageKey(name: string): string {
  const bare = name.split('@')[0];
  const colon = bare.indexOf(':', bare.lastIndexOf('/') + 1);
  return colon === -1 ? bare : bare.slice(0, colon);
}

/**
 * Turns every artifact the filters name into the artifact it is: an artifact
 * as is, a current name into its artifact, and any other name, such as an
 * older tag from a link made before, into its image.
 */
export function resolveFilters(f: Filters, rows: Finding[]): Filters {
  if (f.artifacts.length === 0) return f;
  const ids = new Set<string>();
  const byName = new Map<string, string>();
  for (const v of rows) {
    const id = artifactIdOf(v);
    ids.add(id);
    if (!byName.has(v.artifact_name)) byName.set(v.artifact_name, id);
  }
  const artifacts = f.artifacts.map((a) => (ids.has(a) ? a : (byName.get(a) ?? imageKey(a))));
  return { ...f, artifacts: [...new Set(artifacts)] };
}

type Facet = 'severity' | 'fixable' | 'classes' | 'artifacts';

/**
 * Applies the filters, whose artifacts resolveFilters has resolved; `skip`
 * leaves one facet out, to count what its chips would show.
 */
export function applyFilters<T extends Finding>(rows: T[], f: Filters, skip?: Facet): T[] {
  const q = f.q.toLowerCase();
  return rows.filter((v) => {
    if (skip !== 'severity' && f.severity.length && !f.severity.includes(severityKey(v.severity)))
      return false;
    if (skip !== 'fixable' && f.fixable !== null && isFixable(v) !== (f.fixable === 'true'))
      return false;
    if (skip !== 'classes' && f.classes.length && !f.classes.includes(vulnClass(v))) return false;
    if (skip !== 'artifacts' && f.artifacts.length && !f.artifacts.includes(artifactIdOf(v)))
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

const isDigit = (c: string) => c >= '0' && c <= '9';
const isLetter = (c: string) => (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z');

/** An optional leading "N:" (digits) is a Debian or RPM epoch. */
function splitEpoch(v: string): [string, string] {
  const s = v.trim();
  const i = s.indexOf(':');
  if (i > 0 && /^[0-9]+$/.test(s.slice(0, i))) return [s.slice(0, i), s.slice(i + 1)];
  return ['0', s];
}

/** Runs of digits and runs of letters; a leading "v" before a digit is dropped. */
function tokens(v: string): string[] {
  const s = v.length > 1 && (v[0] === 'v' || v[0] === 'V') && isDigit(v[1]) ? v.slice(1) : v;
  return s.match(/[0-9]+|[A-Za-z]+/g) ?? [];
}

/** Compares two digit strings by value, without overflow. */
function compareNumeric(a: string, b: string): number {
  const x = a.replace(/^0+/, '');
  const y = b.replace(/^0+/, '');
  if (x.length !== y.length) return x.length < y.length ? -1 : 1;
  return x === y ? 0 : x < y ? -1 : 1;
}

function compareToken(a: string, b: string): number {
  const da = isDigit(a[0]);
  const db = isDigit(b[0]);
  if (da && db) return compareNumeric(a, b);
  if (da !== db) return da ? 1 : -1;
  const x = a.toLowerCase();
  const y = b.toLowerCase();
  return x === y ? 0 : x < y ? -1 : 1;
}

/** How a version with token t left compares with one that has ended. */
function remainder(t: string): number {
  if (isLetter(t[0])) return -1; // a pre-release such as "rc1"
  return /^0+$/.test(t) ? 0 : 1;
}

/**
 * Natural version order, the same as the server's (CompareVersions in
 * internal/domain/version.go), so this page and the service page suggest the
 * same upgrade: a leading "N:" epoch dominates; a leading "v" before a digit
 * is ignored; the rest is split into runs of digits, compared by value, and
 * runs of letters, compared case-insensitively, a digit run being greater;
 * when one side runs out, the other is greater unless its remainder starts
 * with letters (a pre-release, 1.0.0-rc1 < 1.0.0) or is only zeros
 * (1.0 = 1.0.0). Ecosystem rules beyond that are not modelled: Debian tilde
 * ordering ("1.0~rc1"), RPM carets and semver build metadata are compared as
 * plain tokens.
 */
export function compareVersions(a: string, b: string): number {
  const [ea, ra] = splitEpoch(a);
  const [eb, rb] = splitEpoch(b);
  const epoch = compareNumeric(ea, eb);
  if (epoch !== 0) return epoch;
  const x = tokens(ra);
  const y = tokens(rb);
  for (let i = 0; i < Math.max(x.length, y.length); i++) {
    const c =
      i >= y.length ? remainder(x[i]) : i >= x.length ? -remainder(y[i]) : compareToken(x[i], y[i]);
    if (c !== 0) return c;
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

/** 0 for critical down to 4 for unknown or none: the most severe finding counted. */
function topRank(c: SeverityCountValues): number {
  if (c.critical > 0) return 0;
  if (c.high > 0) return 1;
  if (c.medium > 0) return 2;
  if (c.low > 0) return 3;
  return 4;
}

/** Plain code-unit order, as the server orders names (byte order for ASCII). */
const byCodeUnits = (a: string, b: string) => (a === b ? 0 : a < b ? -1 : 1);

/**
 * Fixable, unsuppressed findings grouped by package and installed version,
 * in the order of Fix first on the service page (upgradeFirst in
 * internal/domain/packages.go): the most risk points the upgrade clears
 * (critical×10, high×5…) first, then the most findings it fixes, then the
 * most severe one, then by package name and installed version.
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
        artifacts: [...new Set(items.map(artifactLabelOf))].sort(),
      };
    })
    .sort(
      (a, b) =>
        b.risk - a.risk ||
        b.cleared - a.cleared ||
        topRank(a.counts) - topRank(b.counts) ||
        byCodeUnits(a.pkg, b.pkg) ||
        byCodeUnits(a.installed, b.installed),
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
  /** Image groups: the name of the latest scan, with its tag, when it differs from the key. */
  latest?: string;
}

function groupKey(v: Finding, by: Exclude<GroupBy, 'none'>): [string, string] {
  if (by === 'package') return [JSON.stringify([v.pkg_name, v.installed_version]), v.pkg_name];
  if (by === 'vulnerability') return [v.vuln_id, v.vuln_id];
  return [artifactIdOf(v), artifactLabelOf(v)];
}

/**
 * Groups rows that are already sorted; rows keep their order inside a group.
 * Groups come by risk points, then size, then name.
 */
export function groupRows(rows: Finding[], by: Exclude<GroupBy, 'none'>): FindingGroup[] {
  const groups = new Map<string, FindingGroup>();
  for (const v of rows) {
    const [key, label] = groupKey(v, by);
    let group = groups.get(key);
    if (!group) {
      group = { key, label, rows: [], counts: emptyCounts(), risk: 0, fixable: 0 };
      if (by === 'package') group.version = v.installed_version;
      const image = !v.scan_kind;
      if (by === 'image' && image && v.artifact_name !== v.artifact_key)
        group.latest = v.artifact_name;
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
