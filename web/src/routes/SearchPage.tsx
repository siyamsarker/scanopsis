// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useState } from 'react';
import { Link, useSearchParams } from 'react-router';
import { ApiError } from '../api/client';
import {
  useVulnerabilitySearch,
  type SearchScope,
  type VulnerabilitySearch,
} from '../api/findings';
import { CopyIconButton } from '../components/CopyIconButton';
import { SearchInput } from '../components/inputs';
import { PageHeader } from '../components/layout';
import { Segmented } from '../components/Segmented';
import { SeverityMark } from '../components/SeverityMark';
import {
  SEVERITY_LABEL,
  SEVERITY_ORDER,
  severityKey,
  worstSeverity,
  type SeverityKey,
} from '../components/severityModel';
import { EmptyState, ErrorState, Skeleton, SkeletonTable } from '../components/states';
import { countOf, formatExactTime, formatInt, relativeTime } from '../format';
import { buttonClass, linkClass } from '../ui';
import { SeverityBar } from '../viz';
import { compareVersions } from './findings/model';
import { SafeExternal } from './findings/VulnDrawer';
import './findings/findings.css';

type Hit = VulnerabilitySearch['items'][number];

/** 3 to 100 characters: letters, digits and - _ : . */
export const SEARCH_ID_PATTERN = /^[A-Za-z0-9_:.-]{3,100}$/;
const HINT = 'Use 3 to 100 characters: letters, digits and - _ : .';
const SCOPE_OPTIONS: { value: SearchScope; label: string }[] = [
  { value: 'dashboard', label: 'Dashboard branches and protected refs' },
  { value: 'all', label: 'All branches' },
];
const SCOPE_TEXT: Record<SearchScope, string> = {
  dashboard: 'dashboard branches and protected refs',
  all: 'all branches',
};
const EXAMPLES = ['CVE-2024-3094', 'CVE-2021-44228', 'GHSA-jfh8-c2jp-5v3q'];

/** The query string for an ID and a scope; the default scope is left out. */
function query(id: string, scope: SearchScope): Record<string, string> {
  return scope === 'all' ? { id, scope } : { id };
}

function Stat({ value, label }: { value: string; label: string }) {
  return (
    <div className="min-w-0">
      <dd className="text-h2 font-semibold tracking-tight text-text tabular-nums">{value}</dd>
      <dt className="text-small text-text-muted">{label}</dt>
    </div>
  );
}

/** What the vulnerability is and how far it reaches, over every match. */
function Summary({ result }: { result: VulnerabilitySearch }) {
  const worst: SeverityKey = worstSeverity(result.counts) ?? 'unknown';
  const mixed = SEVERITY_ORDER.filter((s) => result.counts[s] > 0).length > 1;
  const fixes = [...result.fixed_versions].sort(compareVersions);
  const newest = result.items.reduce(
    (a, h) => (Date.parse(h.scanned_at) > Date.parse(a) ? h.scanned_at : a),
    result.items[0]?.scanned_at ?? '',
  );
  return (
    <section aria-label="Vulnerability summary" className="panel overflow-hidden">
      <p role="status" className="sr-only">
        {result.vuln_id} affects {countOf(result.total_services, 'service')} in{' '}
        {countOf(result.total_artifacts, 'image')}.
      </p>
      <div
        data-severity={worst}
        className={`tone-${worst} sev-band flex flex-wrap items-center gap-x-4 gap-y-1 px-5 py-3 sm:px-6`}
      >
        <span className="inline-flex items-center gap-2">
          <SeverityMark severity={worst} size={16} />
          <span className="tone-ink font-semibold">
            {SEVERITY_LABEL[worst]}
            {mixed ? ' at worst' : ''}
          </span>
        </span>
        <span className="inline-flex items-center gap-1 font-mono text-body text-text">
          {result.vuln_id}
          <CopyIconButton text={result.vuln_id} label="Copy vulnerability ID" />
        </span>
        {result.primary_url ? (
          <span className="ml-auto text-small">
            <SafeExternal href={result.primary_url}>Advisory</SafeExternal>
          </span>
        ) : null}
      </div>
      <div className="grid gap-x-10 gap-y-5 px-5 py-5 sm:px-6 lg:grid-cols-[minmax(0,1fr)_auto]">
        <div className="min-w-0 space-y-3">
          <h2 className="text-h3 font-semibold tracking-tight text-balance text-text">
            {result.title || 'No title reported'}
          </h2>
          <p className="text-body-s text-text-muted">
            {fixes.length > 0 ? (
              <>
                Fixed in{' '}
                {fixes.map((v, i) => (
                  <span key={v}>
                    {i > 0 ? ', ' : ''}
                    <span className="tone-ok tone-ink font-mono">{v}</span>
                  </span>
                ))}
                .
              </>
            ) : (
              'No fixed version reported yet.'
            )}{' '}
            Searched {SCOPE_TEXT[result.scope]}
            {newest ? (
              <>
                ; newest scan{' '}
                <time dateTime={newest} title={formatExactTime(newest)} className="text-text">
                  {relativeTime(newest)}
                </time>
              </>
            ) : null}
            .
          </p>
          {mixed ? (
            <div className="max-w-md">
              <SeverityBar counts={result.counts} size="sm" legend label="Severity as reported" />
            </div>
          ) : null}
        </div>
        <dl className="grid grid-cols-3 gap-x-8 self-start">
          <Stat
            value={formatInt(result.total_services)}
            label={result.total_services === 1 ? 'service' : 'services'}
          />
          <Stat
            value={formatInt(result.total_artifacts)}
            label={result.total_artifacts === 1 ? 'image' : 'images'}
          />
          <Stat
            value={formatInt(result.total_findings)}
            label={result.total_findings === 1 ? 'finding' : 'findings'}
          />
        </dl>
      </div>
    </section>
  );
}

const GRID =
  'grid grid-cols-[1.25rem_minmax(0,1fr)] gap-x-3 md:grid-cols-[minmax(0,0.8fr)_minmax(0,1.3fr)_minmax(0,1.6fr)_6.5rem_6.5rem] md:gap-x-4';

/** Every match in one table, grouped by service under rows that stay in view. */
function Results({ result }: { result: VulnerabilitySearch }) {
  const byService = new Map<number, { name: string; hits: Hit[] }>();
  for (const hit of result.items) {
    const group = byService.get(hit.service.id) ?? { name: hit.service.name, hits: [] };
    group.hits.push(hit);
    byService.set(hit.service.id, group);
  }
  return (
    <div role="table" aria-label={`Where ${result.vuln_id} is present`} className="mt-8">
      <div
        role="rowgroup"
        className="sticky top-14 z-20 bg-[color-mix(in_srgb,var(--bg)_92%,transparent)] backdrop-blur-sm md:top-0"
      >
        <div
          role="row"
          className={`${GRID} hidden border-b border-line px-4 py-2 text-caption font-medium text-text-muted md:grid`}
        >
          <span role="columnheader">Ref</span>
          <span role="columnheader">Image</span>
          <span role="columnheader">Package</span>
          <span role="columnheader">Severity</span>
          <span role="columnheader" className="text-right">
            Scanned
          </span>
        </div>
      </div>
      {[...byService.entries()].map(([serviceId, group]) => {
        const images = new Set(group.hits.map((h) => h.artifact_name)).size;
        const worst =
          SEVERITY_ORDER.find((s) => group.hits.some((h) => severityKey(h.severity) === s)) ??
          'unknown';
        return (
          <div key={serviceId} role="rowgroup" aria-label={group.name}>
            <div
              role="row"
              className="sticky top-14 z-10 flex items-center gap-3 border-b border-line bg-sunken px-4 py-2.5 md:top-[2.25rem]"
            >
              <span role="rowheader" className="flex min-w-0 flex-1 items-center gap-2.5">
                <SeverityMark severity={worst} size={12} />
                <Link
                  to={`/services/${serviceId}`}
                  className="truncate font-semibold text-text hover:underline"
                >
                  {group.name}
                </Link>
                <span className="shrink-0 text-small text-text-muted">
                  {countOf(images, 'image')}
                </span>
              </span>
              <Link
                to={`/services/${serviceId}/findings?${new URLSearchParams({ q: result.vuln_id })}`}
                className={`${linkClass} shrink-0 text-small`}
              >
                Open findings
              </Link>
            </div>
            {group.hits.map((h) => {
              const sev = severityKey(h.severity);
              return (
                <div
                  key={`${h.scan_id}\u0000${h.pkg_name}\u0000${h.installed_version}`}
                  role="row"
                  className={`${GRID} items-center border-b border-line/70 px-4 py-2.5 transition-colors duration-micro hover:bg-wash`}
                >
                  <span role="cell" className="md:hidden" aria-hidden="true">
                    <SeverityMark severity={sev} size={12} />
                  </span>
                  <span role="cell" className="min-w-0 max-md:col-start-2 max-md:row-start-2">
                    <span
                      className="block truncate font-mono text-small text-text-muted"
                      title={h.ref}
                    >
                      {h.ref}
                    </span>
                  </span>
                  <span role="cell" className="min-w-0 max-md:col-start-2 max-md:row-start-1">
                    <span className="block truncate text-body-s text-text" title={h.artifact_name}>
                      {h.artifact_name}
                    </span>
                  </span>
                  <span role="cell" className="min-w-0 max-md:col-start-2">
                    <span
                      className="block truncate font-mono text-small text-text-muted"
                      title={`${h.pkg_name} ${h.installed_version} → ${h.fixed_version || 'no fix'}`}
                    >
                      <span className="text-text">{h.pkg_name}</span>
                      <span className="text-text-faint">@</span>
                      {h.installed_version}
                      <span aria-hidden="true" className="px-1.5 font-sans text-text-faint">
                        →
                      </span>
                      <span className="sr-only"> fixed in </span>
                      {h.fixed_version ? (
                        <span className="tone-ok tone-ink">{h.fixed_version}</span>
                      ) : (
                        <span className="font-sans text-text-faint">no fix yet</span>
                      )}
                    </span>
                  </span>
                  <span role="cell" className="hidden items-center gap-2 md:flex">
                    <SeverityMark severity={sev} size={12} />
                    <span className={`tone-${sev} tone-ink text-small font-medium`}>
                      {SEVERITY_LABEL[sev]}
                    </span>
                  </span>
                  <span role="cell" className="text-small max-md:col-start-2 md:text-right">
                    <Link
                      to={`/scans/${h.scan_id}`}
                      title={formatExactTime(h.scanned_at)}
                      className="text-text-muted underline decoration-line-strong underline-offset-2 hover:text-text"
                    >
                      {relativeTime(h.scanned_at)}
                    </Link>
                  </span>
                </div>
              );
            })}
          </div>
        );
      })}
    </div>
  );
}

function Outcome({
  result,
  onScope,
}: {
  result: VulnerabilitySearch;
  onScope: (scope: SearchScope) => void;
}) {
  if (result.items.length === 0) {
    return (
      <div className="panel">
        <EmptyState
          art="clean"
          title="No service is affected in its current scans"
          body={
            <>
              <span className="font-mono">{result.vuln_id}</span> is not in the latest scan of any
              service, searching {SCOPE_TEXT[result.scope]}.
            </>
          }
          action={
            result.scope === 'dashboard' ? (
              <button
                type="button"
                className={buttonClass('secondary')}
                onClick={() => onScope('all')}
              >
                Search all branches
              </button>
            ) : undefined
          }
        />
      </div>
    );
  }
  return (
    <>
      <Summary result={result} />
      {result.truncated ? (
        <p role="status" className="mt-4 text-body-s text-text-muted">
          {formatInt(result.total_findings)} findings match. Showing the{' '}
          {formatInt(result.items.length)} most severe.
        </p>
      ) : null}
      <Results result={result} />
    </>
  );
}

export function SearchPage() {
  const [params, setParams] = useSearchParams();
  const idParam = params.get('id')?.trim() ?? '';
  const [rejected, setRejected] = useState<string | null>(null);
  const valid = SEARCH_ID_PATTERN.test(idParam);
  const scope: SearchScope = params.get('scope') === 'all' ? 'all' : 'dashboard';
  const search = useVulnerabilitySearch(valid ? idParam : null, scope);

  function submit(value: string) {
    const text = value.trim();
    if (text === '') {
      setRejected(null);
      setParams({}, { replace: false });
    } else if (SEARCH_ID_PATTERN.test(text)) {
      setRejected(null);
      setParams(query(text, scope));
    } else {
      setRejected(text);
    }
  }

  function changeScope(next: SearchScope) {
    setParams(valid ? query(idParam, next) : next === 'all' ? { scope: next } : {});
  }

  const invalid = rejected !== null || (idParam !== '' && !valid);

  return (
    <section>
      <PageHeader
        title="Vulnerability search"
        subtitle="Find every service whose current scans contain a vulnerability."
      />
      <div className="max-w-3xl space-y-3">
        <SearchInput
          label="Vulnerability ID"
          hideLabel={false}
          placeholder="CVE-2024-3094"
          value={idParam}
          primary
          delayMs={86_400_000}
          onChange={submit}
        />
        <p
          id="search-hint"
          role={invalid ? 'alert' : undefined}
          className={`text-small ${invalid ? 'tone-fail tone-ink' : 'text-text-muted'}`}
        >
          {invalid && rejected ? (
            <>
              <span className="font-mono">{rejected}</span> is not a vulnerability ID. {HINT}
            </>
          ) : (
            <>{HINT} Press Enter to search.</>
          )}
        </p>
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          <span className="text-small text-text-muted">Search in</span>
          <Segmented
            label="Search scope"
            options={SCOPE_OPTIONS}
            value={scope}
            onChange={changeScope}
          />
        </div>
      </div>
      <div className="mt-8">
        {!valid ? (
          idParam === '' && !invalid ? (
            <div className="panel">
              <EmptyState
                art="search"
                title="Search by vulnerability ID"
                body="Enter a CVE or GHSA identifier to see which services and images have it now."
                action={
                  <span className="flex flex-wrap justify-center gap-2">
                    {EXAMPLES.map((example) => (
                      <Link
                        key={example}
                        to={`/search?${new URLSearchParams(query(example, scope))}`}
                        onClick={() => setRejected(null)}
                        className={`${buttonClass('ghost', 'sm')} font-mono`}
                      >
                        {example}
                      </Link>
                    ))}
                  </span>
                }
              />
            </div>
          ) : null
        ) : search.isPending ? (
          <div aria-busy="true" className="space-y-6">
            <Skeleton className="h-44 w-full rounded-panel" />
            <div className="overflow-hidden rounded-panel border border-line">
              <SkeletonTable rows={4} columns={5} />
            </div>
          </div>
        ) : search.isError ? (
          <ErrorState
            message={
              search.error instanceof ApiError ? search.error.message : 'Try again in a moment.'
            }
            onRetry={() => void search.refetch()}
            retrying={search.isFetching}
            className="panel"
          />
        ) : (
          <Outcome result={search.data} onScope={changeScope} />
        )}
      </div>
    </section>
  );
}
