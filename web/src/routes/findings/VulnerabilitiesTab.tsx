// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useMemo, useState } from 'react';
import { useSearchParams } from 'react-router';
import type { Vulnerability } from '../../api/findings';
import { FilterChips, type ChipOption } from '../../components/FilterChips';
import { Mono } from '../../components/Mono';
import { SearchInput } from '../../components/inputs';
import { SeverityBadge } from '../../components/Severity';
import { SEVERITY_LABEL, SEVERITY_ORDER, severityKey } from '../../components/severityModel';
import { EmptyState, ErrorState } from '../../components/states';
import type { Column, SortState } from '../../components/tableCore';
import { VirtualTable } from '../../components/VirtualTable';
import { countOf, formatExactTime, formatInt, relativeTime } from '../../format';
import { secondaryButtonClass } from '../../ui';
import { ApiError } from '../../api/client';
import { AdvisoryLink, VulnDrawer } from './VulnDrawer';
import {
  DEFAULT_SORT,
  applyFilters,
  countBy,
  isFixable,
  parseFilters,
  sortVulns,
  vulnClass,
  type Sort,
  type SortKey,
} from './model';

const CLASS_OPTIONS = [
  { value: 'os-pkgs', label: 'os-pkgs' },
  { value: 'lang-pkgs', label: 'lang-pkgs' },
  { value: 'other', label: 'Other' },
];
const FILTER_PARAMS = ['severity', 'fixable', 'class', 'artifact', 'q'];

function StatusCell({ vuln }: { vuln: Vulnerability }) {
  if (vuln.suppressed) {
    const reason = vuln.suppression || 'no reason recorded';
    return (
      <span className="flex min-w-0 items-center gap-2" title={`Suppressed: ${reason}`}>
        <span className="chip tone-neutral shrink-0">Suppressed</span>
        <span className="truncate text-text-muted">{reason}</span>
      </span>
    );
  }
  return <span className="text-text-muted">{vuln.status || '–'}</span>;
}

export interface VulnerabilitiesTabProps {
  rows: Vulnerability[];
  loading: boolean;
  loadingMore: boolean;
  /** Loading stopped at the row ceiling although more findings exist. */
  truncated?: boolean;
  error: unknown;
  onRetry: () => void;
}

export function VulnerabilitiesTab({
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
  const filters = useMemo(() => parseFilters(params), [params]);
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
  const fixableCount = filtered.filter(isFixable).length;

  const chips = useMemo(() => {
    const sev = countBy(applyFilters(rows, filters, 'severity'), (v) => severityKey(v.severity));
    const fix = applyFilters(rows, filters, 'fixable');
    const cls = countBy(applyFilters(rows, filters, 'classes'), vulnClass);
    const art = countBy(applyFilters(rows, filters, 'artifacts'), (v) => v.artifact_name);
    const fixable = fix.filter(isFixable).length;
    const severity: ChipOption[] = SEVERITY_ORDER.map((key) => ({
      value: key,
      label: SEVERITY_LABEL[key],
      severity: key,
      count: sev.get(key) ?? 0,
    }));
    const fixOptions: ChipOption[] = [
      { value: 'true', label: 'Fixable', count: fixable },
      { value: 'false', label: 'No fix', count: fix.length - fixable },
    ];
    const classes = CLASS_OPTIONS.map((o) => ({ ...o, count: cls.get(o.value) ?? 0 }));
    const artifactNames = [...new Set(rows.map((v) => v.artifact_name))].sort();
    const artifacts = artifactNames.map((name) => ({
      value: name,
      label: name,
      count: art.get(name) ?? 0,
    }));
    return { severity, fixOptions, classes, artifacts };
  }, [rows, filters]);

  const columns: Column<Vulnerability>[] = [
    {
      key: 'severity',
      header: 'Severity',
      width: 'w-28 shrink-0',
      sortKey: 'severity',
      cell: (v) => <SeverityBadge severity={v.severity} />,
    },
    {
      key: 'vuln',
      header: 'Vulnerability',
      width: 'w-44 shrink-0',
      cell: (v) => <AdvisoryLink vuln={v} />,
    },
    {
      key: 'package',
      header: 'Package',
      width: 'w-52 shrink-0',
      sortKey: 'package',
      cell: (v) => (
        <button
          type="button"
          aria-haspopup="dialog"
          title={v.pkg_name}
          onClick={() => setOpen(v)}
          className="block max-w-full truncate rounded text-left underline-offset-2 hover:underline"
        >
          <Mono value={v.pkg_name} />
        </button>
      ),
    },
    {
      key: 'versions',
      header: 'Installed → fixed',
      width: 'w-[28rem] shrink-0',
      cell: (v) => (
        <span
          className="flex min-w-0 items-center gap-1.5"
          title={`${v.installed_version} → ${isFixable(v) ? v.fixed_version : 'No fix'}`}
        >
          <Mono value={v.installed_version} truncate />
          <span aria-hidden="true" className="text-text-muted">
            →
          </span>
          <span className="sr-only">to</span>
          {isFixable(v) ? (
            <Mono value={v.fixed_version} truncate />
          ) : (
            <span className="text-text-muted">No fix</span>
          )}
        </span>
      ),
    },
    {
      key: 'target',
      header: 'Target',
      width: 'w-48 shrink-0',
      cell: (v) => <span title={v.target}>{v.target}</span>,
    },
    {
      key: 'artifact',
      header: 'Artifact',
      width: 'w-56 shrink-0',
      cell: (v) => <span title={v.artifact_name}>{v.artifact_name}</span>,
    },
    {
      key: 'seen',
      header: 'First seen',
      width: 'w-32 shrink-0',
      sortKey: 'seen',
      cell: (v) => (
        <time dateTime={v.first_seen_at} title={formatExactTime(v.first_seen_at)}>
          {relativeTime(v.first_seen_at)}
        </time>
      ),
    },
    {
      key: 'status',
      header: 'Status',
      width: 'w-56 shrink-0',
      cell: (v) => <StatusCell vuln={v} />,
    },
  ];

  if (error && rows.length === 0) {
    return (
      <ErrorState
        title="Vulnerabilities could not be loaded"
        message={error instanceof ApiError ? error.message : 'Try again in a moment.'}
        onRetry={onRetry}
      />
    );
  }

  const filtering =
    filters.severity.length + filters.classes.length + filters.artifacts.length > 0 ||
    filters.fixable !== null ||
    filters.q !== '';

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
    <div className="space-y-4">
      <div className="space-y-3 rounded-card border border-border bg-surface p-4">
        <div className="flex flex-wrap items-center gap-x-6 gap-y-3">
          <SearchInput
            label="Search package, vulnerability ID or title"
            placeholder="Search package, ID or title"
            value={filters.q}
            onChange={(value) => setParam('q', value.trim() || null)}
            className="w-full sm:w-80"
          />
          <label className="inline-flex min-h-10 items-center gap-2 text-text">
            <input
              type="checkbox"
              checked={includeSuppressed}
              onChange={(event) => setParam('suppressed', event.target.checked ? 'true' : null)}
              className="size-4 accent-[var(--brand)]"
            />
            Include suppressed
          </label>
        </div>
        <FilterChips
          label="Filter by severity"
          options={chips.severity}
          selected={filters.severity}
          onChange={(s) => setParam('severity', s.join(',') || null)}
        />
        <div className="flex flex-wrap gap-x-6 gap-y-3">
          <FilterChips
            label="Filter by fix availability"
            single
            options={chips.fixOptions}
            selected={filters.fixable ? [filters.fixable] : []}
            onChange={(s) => setParam('fixable', s[0] ?? null)}
          />
          <FilterChips
            label="Filter by class"
            options={chips.classes}
            selected={filters.classes}
            onChange={(s) => setParam('class', s.join(',') || null)}
          />
        </div>
        {chips.artifacts.length > 1 ? (
          <FilterChips
            label="Filter by artifact"
            options={chips.artifacts}
            selected={filters.artifacts}
            onChange={(s) => setParam('artifact', s.join(',') || null)}
          />
        ) : null}
      </div>

      <div className="flex min-h-6 flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
        <p className="font-medium text-text tabular-nums">
          {countOf(filtered.length, 'vulnerability', 'vulnerabilities')}, {formatInt(fixableCount)}{' '}
          fixable
        </p>
        {loadingMore ? (
          <p role="status" className="text-sm text-text-muted tabular-nums">
            Loaded {formatInt(rows.length)} so far, counts update as more arrive
          </p>
        ) : null}
        {truncated ? (
          <p role="status" className="text-sm text-text-muted">
            Showing the first 50 000 findings. Narrow the filters to see the rest.
          </p>
        ) : null}
        {error && rows.length > 0 ? (
          <p role="alert" className="text-sm text-text-muted">
            Loading stopped after {formatInt(rows.length)} vulnerabilities.{' '}
            <button type="button" onClick={onRetry} className="underline underline-offset-2">
              Try again
            </button>
          </p>
        ) : null}
      </div>

      <VirtualTable
        caption="Vulnerabilities"
        columns={columns}
        rows={filtered}
        rowKey={(v) => v.id}
        manualSort
        sort={sort}
        onSortChange={(s: SortState) => setSort({ key: s.key as SortKey, direction: s.direction })}
        loading={loading}
        onRowActivate={setOpen}
        heightClass="h-[70vh]"
        minWidthClass="min-w-[107rem]"
        empty={
          rows.length === 0 ? (
            <EmptyState
              compact
              title="No vulnerabilities in the current scans"
              body="Trivy reported nothing for the latest scan of this ref."
            />
          ) : (
            <EmptyState
              compact
              title="No vulnerabilities match these filters"
              body="Loosen a filter to see more."
              action={
                filtering ? (
                  <button type="button" className={secondaryButtonClass} onClick={clearFilters}>
                    Clear filters
                  </button>
                ) : undefined
              }
            />
          )
        }
      />
      {open ? <VulnDrawer vuln={open} onClose={() => setOpen(null)} /> : null}
    </div>
  );
}
