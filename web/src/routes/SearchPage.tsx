// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useState } from 'react';
import { Link, useSearchParams } from 'react-router';
import { ApiError } from '../api/client';
import { useVulnerabilitySearch, type VulnerabilitySearch } from '../api/findings';
import { DataTable } from '../components/DataTable';
import { SearchInput } from '../components/inputs';
import { Card, PageHeader } from '../components/layout';
import { Mono } from '../components/Mono';
import { SeverityBadge } from '../components/Severity';
import { EmptyState, ErrorState, SkeletonTable } from '../components/states';
import type { Column } from '../components/tableCore';
import { countOf, formatExactTime, relativeTime } from '../format';

type Hit = VulnerabilitySearch['items'][number];

/** 3 to 100 characters: letters, digits and - _ : . */
export const SEARCH_ID_PATTERN = /^[A-Za-z0-9_:.-]{3,100}$/;
const HINT = 'Use 3 to 100 characters: letters, digits and - _ : .';
const EXAMPLES = ['CVE-2024-3094', 'CVE-2021-44228', 'GHSA-jfh8-c2jp-5v3q'];

const columns: Column<Hit>[] = [
  { key: 'ref', header: 'Ref', cell: (h) => <Mono value={h.ref} /> },
  { key: 'artifact', header: 'Artifact', cell: (h) => <Mono value={h.artifact_name} /> },
  {
    key: 'package',
    header: 'Package',
    cell: (h) => (
      <span className="inline-flex flex-wrap items-center gap-1.5">
        <Mono value={h.pkg_name} />
        <Mono value={h.installed_version} className="text-text-muted" />
        <span aria-hidden="true" className="text-text-muted">
          →
        </span>
        <span className="sr-only">to</span>
        {h.fixed_version ? (
          <Mono value={h.fixed_version} />
        ) : (
          <span className="text-text-muted">No fix</span>
        )}
      </span>
    ),
  },
  { key: 'severity', header: 'Severity', cell: (h) => <SeverityBadge severity={h.severity} /> },
  {
    key: 'scanned',
    header: 'Scanned',
    cell: (h) => (
      <Link
        to={`/scans/${h.scan_id}`}
        title={formatExactTime(h.scanned_at)}
        className="underline decoration-border-strong underline-offset-2 hover:decoration-text"
      >
        {relativeTime(h.scanned_at)}
      </Link>
    ),
  },
];

function Results({ result }: { result: VulnerabilitySearch }) {
  if (result.items.length === 0) {
    return (
      <Card padded={false}>
        <EmptyState
          compact
          title="No service is affected in its current scans."
          body={
            <>
              <Mono value={result.vuln_id} /> does not appear in the latest scan of any service.
            </>
          }
        />
      </Card>
    );
  }
  const byService = new Map<number, { name: string; hits: Hit[] }>();
  for (const hit of result.items) {
    const group = byService.get(hit.service.id) ?? { name: hit.service.name, hits: [] };
    group.hits.push(hit);
    byService.set(hit.service.id, group);
  }
  const artifacts = new Set(result.items.map((h) => `${h.service.id}\u0000${h.artifact_name}`));
  const newest = result.items.reduce(
    (a, h) => (Date.parse(h.scanned_at) > Date.parse(a) ? h.scanned_at : a),
    result.items[0].scanned_at,
  );
  return (
    <div className="space-y-4">
      <div>
        <p className="text-lg font-semibold tracking-tight text-text">
          <Mono value={result.vuln_id} /> affects {result.truncated ? 'at least ' : ''}
          {countOf(byService.size, 'service')} in {countOf(artifacts.size, 'artifact')}
        </p>
        {result.truncated ? (
          <p role="status" className="mt-1 text-sm text-text-muted">
            More findings match than can be listed. Showing the first 1 000, ordered by service
            name.
          </p>
        ) : null}
        <p className="mt-1 text-sm text-text-muted">
          Newest scan{' '}
          <time dateTime={newest} title={formatExactTime(newest)} className="text-text">
            {relativeTime(newest)}
          </time>
        </p>
      </div>
      {[...byService.entries()].map(([serviceId, group]) => (
        <section key={serviceId} aria-label={group.name} className="space-y-2">
          <h2 className="text-base font-semibold tracking-tight text-text">
            <Link
              to={`/services/${serviceId}`}
              className="underline decoration-border-strong underline-offset-2 hover:decoration-text"
            >
              {group.name}
            </Link>
          </h2>
          <DataTable
            caption={`${group.name} affected artifacts`}
            columns={columns}
            rows={group.hits}
            rowKey={(h) => `${h.scan_id}\u0000${h.pkg_name}\u0000${h.installed_version}`}
          />
        </section>
      ))}
    </div>
  );
}

export function SearchPage() {
  const [params, setParams] = useSearchParams();
  const idParam = params.get('id')?.trim() ?? '';
  const [rejected, setRejected] = useState<string | null>(null);
  const valid = SEARCH_ID_PATTERN.test(idParam);
  const search = useVulnerabilitySearch(valid ? idParam : null);

  function submit(value: string) {
    const text = value.trim();
    if (text === '') {
      setRejected(null);
      setParams({}, { replace: false });
    } else if (SEARCH_ID_PATTERN.test(text)) {
      setRejected(null);
      setParams({ id: text });
    } else {
      setRejected(text);
    }
  }

  const invalid = rejected !== null || (idParam !== '' && !valid);

  return (
    <section className="mx-auto max-w-6xl p-6">
      <PageHeader
        title="Vulnerability search"
        subtitle="Find every service whose current scans contain a vulnerability."
      />
      <div className="max-w-2xl space-y-2">
        <SearchInput
          label="Vulnerability ID"
          hideLabel={false}
          placeholder="CVE-2024-3094"
          value={idParam}
          delayMs={86_400_000}
          onChange={submit}
        />
        <p
          id="search-hint"
          role={invalid ? 'alert' : undefined}
          className={`text-sm ${invalid ? 'text-fail' : 'text-text-muted'}`}
        >
          {HINT}. Press Enter to search.
        </p>
        <p className="text-sm text-text-muted">
          Examples:{' '}
          {EXAMPLES.map((example, index) => (
            <span key={example}>
              {index > 0 ? ', ' : ''}
              <Link
                to={`/search?id=${example}`}
                onClick={() => setRejected(null)}
                className="font-mono text-text underline decoration-border-strong underline-offset-2 hover:decoration-text"
              >
                {example}
              </Link>
            </span>
          ))}
        </p>
      </div>
      <div className="mt-8">
        {!valid ? (
          idParam === '' && !invalid ? (
            <Card padded={false}>
              <EmptyState
                compact
                title="Search by vulnerability ID"
                body="Enter a CVE or GHSA identifier to see where it is present."
              />
            </Card>
          ) : null
        ) : search.isPending ? (
          <div className="overflow-hidden rounded-card border border-border bg-surface">
            <SkeletonTable rows={4} columns={5} />
          </div>
        ) : search.isError ? (
          <ErrorState
            message={
              search.error instanceof ApiError ? search.error.message : 'Try again in a moment.'
            }
            onRetry={() => void search.refetch()}
            retrying={search.isFetching}
            className="rounded-card border border-border bg-surface"
          />
        ) : (
          <Results result={search.data} />
        )}
      </div>
    </section>
  );
}
