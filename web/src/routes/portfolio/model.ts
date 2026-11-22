// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import type { PortfolioHistory, PortfolioItem, ServiceHistory } from '../../api/portfolio';
import { SEVERITY_ORDER, totalOf } from '../../components/severityModel';
import {
  riskPoints,
  SEVERITIES,
  type GateState,
  type PipelinePoint,
  type SeverityCounts,
} from '../../viz';

export type FilterKey = 'all' | 'gate-failed' | 'critical' | 'fixable' | 'stale';
export type SortKey = 'risk' | 'name' | 'freshness';
export type ViewKey = 'cards' | 'list';

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
export const STALE_AFTER_DAYS = 7;
export const STALE_AFTER_MS = STALE_AFTER_DAYS * 24 * 60 * 60 * 1000;

/** Above this many services the compact list is the better first view. */
export const LIST_VIEW_FROM = 25;

export interface PortfolioParams {
  q: string;
  filter: FilterKey;
  sort: SortKey;
  /** Undefined when the address does not say; the remembered or default view applies. */
  view?: ViewKey;
}

function pick<T extends string>(raw: string | null, allowed: { value: T }[], fallback: T): T {
  return allowed.find((option) => option.value === raw)?.value ?? fallback;
}

function viewOf(raw: string | null | undefined): ViewKey | undefined {
  return raw === 'cards' || raw === 'list' ? raw : undefined;
}

/** Reads the view state from the URL; unknown values fall back to the defaults. */
export function readParams(search: URLSearchParams): PortfolioParams {
  return {
    q: search.get('q') ?? '',
    filter: pick(search.get('filter'), FILTERS, 'all'),
    sort: pick(search.get('sort'), SORTS, 'risk'),
    view: viewOf(search.get('view')),
  };
}

/** Writes the state back; defaults are left out so the URL stays short. */
export function writeParams(params: PortfolioParams): URLSearchParams {
  const out = new URLSearchParams();
  if (params.q) out.set('q', params.q);
  if (params.filter !== 'all') out.set('filter', params.filter);
  if (params.sort !== 'risk') out.set('sort', params.sort);
  if (params.view) out.set('view', params.view);
  return out;
}

const VIEW_KEY = 'scanopsis.portfolio.view';

/** The view chosen last time on this device, if storage is available. */
export function rememberedView(): ViewKey | undefined {
  try {
    return viewOf(window.localStorage.getItem(VIEW_KEY));
  } catch {
    return undefined;
  }
}

export function rememberView(view: ViewKey) {
  try {
    window.localStorage.setItem(VIEW_KEY, view);
  } catch {
    // Private windows and blocked storage: the choice lasts for this page only.
  }
}

/** The address wins, then the remembered choice, then cards for a few services and the list for many. */
export function effectiveView(
  fromUrl: ViewKey | undefined,
  remembered: ViewKey | undefined,
  services: number,
): ViewKey {
  return fromUrl ?? remembered ?? (services >= LIST_VIEW_FROM ? 'list' : 'cards');
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
 * Highest risk points first (the number in the iris), then the worse
 * severity mix, then the name. Services never scanned go last.
 */
function byRisk(a: PortfolioItem, b: PortfolioItem) {
  const scannedA = a.last_scan_at !== null;
  const scannedB = b.last_scan_at !== null;
  if (scannedA !== scannedB) return scannedA ? -1 : 1;
  const risk = riskPoints(b.counts) - riskPoints(a.counts);
  if (risk !== 0) return risk;
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

export function sortItems(items: PortfolioItem[], sort: SortKey) {
  return [...items].sort(sorters[sort]);
}

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

/** The quality gate as the iris draws it; undefined without any SonarQube analysis. */
export function gateOf(
  item: Pick<PortfolioItem, 'quality_gate_status' | 'last_analysis_at'>,
): GateState | undefined {
  if (item.quality_gate_status === 'OK') return 'pass';
  if (item.quality_gate_status === 'ERROR') return 'fail';
  return item.last_analysis_at ? 'none' : undefined;
}

/* ---------------------------------------------------------------- history */

export type HistoryIndex = Map<number, ServiceHistory>;

export function indexHistory(history: PortfolioHistory | undefined): HistoryIndex {
  return new Map((history?.services ?? []).map((s) => [s.service_id, s]));
}

function sameCounts(a: SeverityCounts, b: SeverityCounts) {
  return SEVERITIES.every((s) => (a[s] ?? 0) === (b[s] ?? 0));
}

/**
 * The history fits the card only when it describes the ref the card shows
 * and ends in the counts the card shows; a scan that arrived between the two
 * requests makes them disagree until the next refresh.
 */
export function historyFor(
  item: PortfolioItem,
  index: HistoryIndex,
): ServiceHistory['points'] | undefined {
  const h = index.get(item.service.id);
  if (!h || h.ref !== item.ref || h.points.length === 0) return undefined;
  const last = h.points[h.points.length - 1];
  return sameCounts(last.counts, item.counts) ? h.points : undefined;
}

/**
 * Each pipeline as the card's spark draws it: how many open findings of each
 * severity it added (above the line) and how many it removed (below), as the
 * net change of the counts against the pipeline before. The first pipeline
 * has nothing to compare with and is left out.
 */
export function sparkPoints(points: ServiceHistory['points'], ref: string): PipelinePoint[] {
  const out: PipelinePoint[] = [];
  for (let i = 1; i < points.length; i++) {
    const before = points[i - 1].counts;
    const after = points[i].counts;
    const added: SeverityCounts = {};
    let removed = 0;
    for (const s of SEVERITIES) {
      const change = (after[s] ?? 0) - (before[s] ?? 0);
      if (change > 0) added[s] = change;
      else removed -= change;
    }
    out.push({
      pipelineId: points[i].pipeline_id,
      at: points[i].at,
      ref,
      newBySeverity: added,
      fixedCount: removed,
    });
  }
  return out;
}

export interface RiskChange {
  delta: number;
  /** The pipeline compared against. */
  sincePipeline: number;
}

/** Risk now against the pipeline before the latest; undefined with fewer than two pipelines. */
export function riskChange(points: ServiceHistory['points'] | undefined): RiskChange | undefined {
  if (!points || points.length < 2) return undefined;
  const last = points[points.length - 1];
  const before = points[points.length - 2];
  return {
    delta: riskPoints(last.counts) - riskPoints(before.counts),
    sincePipeline: before.pipeline_id,
  };
}

export interface PortfolioRisk {
  /** Risk points of every listed service now. */
  now: number;
  /** One value per day back, oldest first, then now. */
  series: number[];
  /** Change against a week ago; undefined until the history is loaded. */
  weekDelta?: number;
}

export function portfolioRisk(items: PortfolioItem[], history?: PortfolioHistory): PortfolioRisk {
  const now = items.reduce((sum, item) => sum + riskPoints(item.counts), 0);
  if (!history || history.days.length === 0) return { now, series: [] };
  const past = history.days.map((d) => riskPoints(d.counts));
  return { now, series: [...past, now], weekDelta: now - past[0] };
}

export interface Mover {
  item: PortfolioItem;
  change: RiskChange;
}

/** The service whose latest pipeline moved its risk the most, either way. */
export function biggestMover(items: PortfolioItem[], index: HistoryIndex): Mover | undefined {
  let best: Mover | undefined;
  for (const item of items) {
    const change = riskChange(historyFor(item, index));
    if (!change || change.delta === 0) continue;
    if (!best || Math.abs(change.delta) > Math.abs(best.change.delta)) best = { item, change };
  }
  return best;
}
