// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import type { PortfolioItem } from '../../api/portfolio';
import { SEVERITY_ORDER, totalOf } from '../../components/severityModel';

export type FilterKey = 'all' | 'gate-failed' | 'critical' | 'fixable' | 'stale';
export type SortKey = 'risk' | 'name' | 'freshness';
export type ViewKey = 'grid' | 'treemap';

export const FILTERS: { value: FilterKey; label: string }[] = [
  { value: 'all', label: 'All' },
  { value: 'gate-failed', label: 'Failing gate' },
  { value: 'critical', label: 'Has critical' },
  { value: 'fixable', label: 'Fixable' },
  { value: 'stale', label: 'Stale' },
];

export const SORTS: { value: SortKey; label: string }[] = [
  { value: 'risk', label: 'Risk' },
  { value: 'name', label: 'Name' },
  { value: 'freshness', label: 'Freshness' },
];

/** No scan for longer than this makes a service stale. */
export const STALE_AFTER_MS = 7 * 24 * 60 * 60 * 1000;

export interface PortfolioParams {
  q: string;
  filter: FilterKey;
  sort: SortKey;
  view: ViewKey;
}

function pick<T extends string>(raw: string | null, allowed: { value: T }[], fallback: T): T {
  return allowed.find((option) => option.value === raw)?.value ?? fallback;
}

/** Reads the view state from the URL; unknown values fall back to the defaults. */
export function readParams(search: URLSearchParams): PortfolioParams {
  return {
    q: search.get('q') ?? '',
    filter: pick(search.get('filter'), FILTERS, 'all'),
    sort: pick(search.get('sort'), SORTS, 'risk'),
    view: search.get('view') === 'treemap' ? 'treemap' : 'grid',
  };
}

/** Writes the state back; defaults are left out so the URL stays short. */
export function writeParams(params: PortfolioParams): URLSearchParams {
  const out = new URLSearchParams();
  if (params.q) out.set('q', params.q);
  if (params.filter !== 'all') out.set('filter', params.filter);
  if (params.sort !== 'risk') out.set('sort', params.sort);
  if (params.view !== 'grid') out.set('view', params.view);
  return out;
}

/** A service that has never been scanned counts as stale: nothing recent exists. */
export function isStale(item: PortfolioItem, now: Date): boolean {
  if (!item.last_scan_at) return true;
  return now.getTime() - new Date(item.last_scan_at).getTime() > STALE_AFTER_MS;
}

const matchers: Record<FilterKey, (item: PortfolioItem, now: Date) => boolean> = {
  all: () => true,
  'gate-failed': (item) => item.quality_gate_status === 'ERROR',
  critical: (item) => item.counts.critical > 0,
  fixable: (item) => item.fixable > 0,
  stale: isStale,
};

export function matchesFilter(item: PortfolioItem, filter: FilterKey, now: Date): boolean {
  return matchers[filter](item, now);
}

export function matchesQuery(item: PortfolioItem, q: string): boolean {
  const needle = q.trim().toLowerCase();
  if (!needle) return true;
  return (
    item.service.name.toLowerCase().includes(needle) ||
    item.service.gitlab_project_path.toLowerCase().includes(needle)
  );
}

function byName(a: PortfolioItem, b: PortfolioItem) {
  return a.service.name.localeCompare(b.service.name) || a.service.id - b.service.id;
}

/**
 * Worst severity first, then the higher critical count, then high, medium, low
 * and unknown. Comparing the counts level by level in severity order does both.
 */
function byRisk(a: PortfolioItem, b: PortfolioItem) {
  for (const key of SEVERITY_ORDER) {
    if (a.counts[key] !== b.counts[key]) return b.counts[key] - a.counts[key];
  }
  return byName(a, b);
}

/** Newest scan first; services without a scan last. */
function byFreshness(a: PortfolioItem, b: PortfolioItem) {
  const at = a.last_scan_at ? new Date(a.last_scan_at).getTime() : -Infinity;
  const bt = b.last_scan_at ? new Date(b.last_scan_at).getTime() : -Infinity;
  if (at !== bt) return at < bt ? 1 : -1;
  return byName(a, b);
}

const sorters = { risk: byRisk, name: byName, freshness: byFreshness };

export function visibleItems(items: PortfolioItem[], params: PortfolioParams, now: Date) {
  return items
    .filter((item) => matchesQuery(item, params.q) && matchesFilter(item, params.filter, now))
    .sort(sorters[params.sort]);
}

/** Per-filter counts for the chips, ignoring the search text. */
export function filterCounts(items: PortfolioItem[], now: Date): Record<FilterKey, number> {
  const result = {} as Record<FilterKey, number>;
  for (const { value } of FILTERS) {
    result[value] = items.filter((item) => matchesFilter(item, value, now)).length;
  }
  return result;
}

/** Open vulnerabilities of an item: the sum of its severity counts. */
export function openVulnerabilities(item: PortfolioItem): number {
  return totalOf(item.counts);
}
