// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { SlidersHorizontal } from 'lucide-react';
import { useId, useMemo, useState, type ReactNode } from 'react';
import { useSearchParams } from 'react-router';
import { ApiError } from '../../api/client';
import type { Vulnerability } from '../../api/findings';
import { FilterChips, type ChipOption } from '../../components/FilterChips';
import { SearchInput, Select } from '../../components/inputs';
import { Segmented } from '../../components/Segmented';
import { SEVERITY_LABEL, SEVERITY_ORDER, severityKey } from '../../components/severityModel';
import { EmptyState, ErrorState } from '../../components/states';
import { formatInt } from '../../format';
import { buttonClass } from '../../ui';
import { FindingsList } from './FindingsList';
import { LensSummary } from './LensSummary';
import { VulnDrawer } from './VulnDrawer';
import {
  DEFAULT_SORT,
  applyFilters,
  countBy,
  isFixable,
  parseFilters,
  parseGroupBy,
  sortVulns,
  vulnClass,
  type GroupBy,
  type Sort,
} from './model';

const CLASS_OPTIONS = [
  { value: 'os-pkgs', label: 'OS packages' },
  { value: 'lang-pkgs', label: 'Language packages' },
  { value: 'other', label: 'Other' },
];
const FILTER_PARAMS = ['severity', 'fixable', 'class', 'artifact', 'q'];
const GROUP_OPTIONS: { value: GroupBy; label: string }[] = [
  { value: 'none', label: 'None' },
  { value: 'package', label: 'Package' },
  { value: 'vulnerability', label: 'Vulnerability' },
  { value: 'image', label: 'Image' },
];

/** One labelled row of facet chips. */
function Facet({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid gap-x-4 gap-y-1.5 sm:grid-cols-[5.5rem_minmax(0,1fr)] sm:items-start">
      <span className="pt-1.5 text-small text-text-muted">{label}</span>
      {children}
    </div>
  );
}

export interface VulnerabilitiesTabProps {
  serviceId: number;
  rows: Vulnerability[];
  loading: boolean;
  loadingMore: boolean;
  /** Loading stopped at the row ceiling although more findings exist. */
  truncated?: boolean;
  error: unknown;
  onRetry: () => void;
}

export function VulnerabilitiesTab({
  serviceId,
  rows,
  loading,
  loadingMore,
  truncated = false,
  error,
  onRetry,
}: VulnerabilitiesTabProps) {
  const [params, setParams] = useSearchParams();
  const [sort, setSort] = useState<Sort>(DEFAULT_SORT);
  const [open, setOpen] = useState<Vulnerability | null>(null);
  const [filtersOpen, setFiltersOpen] = useState(false);
  const facetsId = useId();
  const filters = useMemo(() => parseFilters(params), [params]);
  const groupBy = parseGroupBy(params.get('group'));
  const includeSuppressed = params.get('suppressed') === 'true';

  function setParam(name: string, value: string | null) {
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (value) next.set(name, value);
        else next.delete(name);
        return next;
      },
      { replace: true },
    );
  }

  const filtered = useMemo(
    () => sortVulns(applyFilters(rows, filters), sort),
    [rows, filters, sort],
  );

  const chips = useMemo(() => {
    const sev = countBy(applyFilters(rows, filters, 'severity'), (v) => severityKey(v.severity));
    const fix = applyFilters(rows, filters, 'fixable');
    const cls = countBy(applyFilters(rows, filters, 'classes'), vulnClass);
    const art = countBy(applyFilters(rows, filters, 'artifacts'), (v) => v.artifact_key);
    const fixable = fix.filter(isFixable).length;
    const severity: ChipOption[] = SEVERITY_ORDER.map((key) => ({
      value: key,
      label: SEVERITY_LABEL[key],
      severity: key,
      count: sev.get(key) ?? 0,
    }));
    const fixOptions: ChipOption[] = [
      { value: 'true', label: 'Fixable', count: fixable },
      { value: 'false', label: 'No fix yet', count: fix.length - fixable },
    ];
    const classes = CLASS_OPTIONS.map((o) => ({ ...o, count: cls.get(o.value) ?? 0 })).filter(
      (o) => o.count > 0 || filters.classes.includes(o.value as never),
    );
    // One chip per artifact key, so every tag of an image is one image.
    const keys = [...new Set(rows.map((v) => v.artifact_key))].sort();
    const artifacts = keys.map((key) => ({ value: key, label: key, count: art.get(key) ?? 0 }));
    // A link from before keys may name an image with its tag: mark its key.
    const keyOf = new Map(rows.map((v) => [v.artifact_name, v.artifact_key]));
    const selectedArtifacts = [...new Set(filters.artifacts.map((a) => keyOf.get(a) ?? a))];
    return { severity, fixOptions, classes, artifacts, selectedArtifacts };
  }, [rows, filters]);

  if (error && rows.length === 0) {
    return (
      <ErrorState
        title="Vulnerabilities could not be loaded"
        message={error instanceof ApiError ? error.message : 'Try again in a moment.'}
        onRetry={onRetry}
      />
    );
  }

  const focused =
    filters.severity.length + filters.classes.length + filters.artifacts.length > 0 ||
    filters.fixable !== null ||
    filters.q !== '';
  const focusKey = JSON.stringify([filters, groupBy]);
  const activeCount =
    filters.severity.length +
    filters.classes.length +
    filters.artifacts.length +
    (filters.fixable !== null ? 1 : 0) +
    (groupBy !== 'none' ? 1 : 0) +
    (includeSuppressed ? 1 : 0);

  function clearFilters() {
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        for (const name of FILTER_PARAMS) next.delete(name);
        return next;
      },
      { replace: true },
    );
  }

  return (
    <div className="space-y-6">
      <div data-focused={focused || undefined} className="lens-panel panel overflow-hidden">
        <div className="space-y-3 px-5 pt-5 pb-4 sm:px-6">
          <div className="flex flex-wrap items-center gap-x-4 gap-y-3">
            <SearchInput
              label="Search package, vulnerability ID or title"
              placeholder="Search package, ID or title"
              value={filters.q}
              primary
              onChange={(value) => setParam('q', value.trim() || null)}
              className="min-w-0 flex-1 lg:max-w-md"
            />
            <button
              type="button"
              aria-expanded={filtersOpen}
              aria-controls={facetsId}
              onClick={() => setFiltersOpen((open) => !open)}
              className={`${buttonClass('secondary')} md:hidden`}
            >
              <SlidersHorizontal size={16} strokeWidth={1.75} aria-hidden="true" />
              Filters
              {activeCount > 0 ? (
                <span className="rounded-full bg-brand px-1.5 text-caption font-semibold text-brand-ink tabular-nums">
                  {activeCount}
                </span>
              ) : null}
            </button>
            <div className="hidden items-center gap-2 md:flex">
              <span className="text-small text-text-muted">Group by</span>
              <Segmented
                label="Group by"
                options={GROUP_OPTIONS}
                value={groupBy}
                onChange={(next) => setParam('group', next === 'none' ? null : next)}
              />
            </div>
          </div>
          <div id={facetsId} className={`space-y-3 ${filtersOpen ? '' : 'max-md:hidden'}`}>
            <div className="space-y-3 md:hidden">
              <Select
                label="Group by"
                value={groupBy}
                onChange={(next) => setParam('group', next === 'none' ? null : next)}
                options={GROUP_OPTIONS}
              />
            </div>
            <Facet label="Severity">
              <FilterChips
                label="Filter by severity"
                options={chips.severity}
                selected={filters.severity}
                onChange={(s) => setParam('severity', s.join(',') || null)}
              />
            </Facet>
            <Facet label="Fix">
              <div className="flex flex-wrap items-center gap-x-6 gap-y-2">
                <FilterChips
                  label="Filter by fix availability"
                  single
                  options={chips.fixOptions}
                  selected={filters.fixable ? [filters.fixable] : []}
                  onChange={(s) => setParam('fixable', s[0] ?? null)}
                />
                <label className="inline-flex min-h-8 items-center gap-2 text-body-s text-text-muted">
                  <input
                    type="checkbox"
                    checked={includeSuppressed}
                    onChange={(event) =>
                      setParam('suppressed', event.target.checked ? 'true' : null)
                    }
                    className="size-4 accent-[var(--brand)]"
                  />
                  Include suppressed
                </label>
              </div>
            </Facet>
            {chips.classes.length > 1 ? (
              <Facet label="Packages">
                <FilterChips
                  label="Filter by package class"
                  options={chips.classes}
                  selected={filters.classes}
                  onChange={(s) => setParam('class', s.join(',') || null)}
                />
              </Facet>
            ) : null}
            {chips.artifacts.length > 1 ? (
              <Facet label="Image">
                <FilterChips
                  label="Filter by image"
                  options={chips.artifacts}
                  selected={chips.selectedArtifacts}
                  onChange={(s) => setParam('artifact', s.join(',') || null)}
                />
              </Facet>
            ) : null}
          </div>
        </div>
        <div className="border-t border-line bg-sunken/50">
          <LensSummary
            all={rows}
            selected={filtered}
            focused={focused}
            onClear={clearFilters}
            incomplete={loadingMore || truncated || Boolean(error)}
          />
        </div>
      </div>

      {loadingMore || truncated || (error && rows.length > 0) ? (
        <div className="-mt-2 flex flex-wrap gap-x-4 text-small text-text-muted">
          {loadingMore ? (
            <p role="status" className="tabular-nums">
              Loaded {formatInt(rows.length)} so far, counts update as more arrive
            </p>
          ) : null}
          {truncated ? (
            <p role="status">
              Showing the first 50 000 findings. Narrow the filters to see the rest.
            </p>
          ) : null}
          {error && rows.length > 0 ? (
            <p role="alert">
              Loading stopped after {formatInt(rows.length)} vulnerabilities.{' '}
              <button type="button" onClick={onRetry} className="underline underline-offset-2">
                Try again
              </button>
            </p>
          ) : null}
        </div>
      ) : null}

      <FindingsList
        rows={filtered}
        groupBy={groupBy}
        sort={sort}
        onSortChange={setSort}
        onOpen={setOpen}
        openId={open?.id ?? null}
        loading={loading}
        focusKey={focusKey}
        empty={
          <div className="panel">
            {rows.length === 0 ? (
              <EmptyState
                compact
                art="clean"
                title="No vulnerabilities in the current scans"
                body="Trivy reported nothing for the latest scan of this ref."
              />
            ) : (
              <EmptyState
                compact
                art="lens"
                title="No vulnerabilities match these filters"
                body="Loosen a filter to see more."
                action={
                  focused ? (
                    <button
                      type="button"
                      className={buttonClass('secondary')}
                      onClick={clearFilters}
                    >
                      Clear filters
                    </button>
                  ) : undefined
                }
              />
            )}
          </div>
        }
      />
      {open ? <VulnDrawer serviceId={serviceId} vuln={open} onClose={() => setOpen(null)} /> : null}
    </div>
  );
}
