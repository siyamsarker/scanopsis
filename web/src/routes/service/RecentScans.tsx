// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { useMemo } from 'react';
import { Link } from 'react-router';
import { useServiceScans, type ScanSummary } from '../../api/service';
import { ArtifactDetail, ArtifactName } from '../../components/ArtifactName';
import { DataTable } from '../../components/DataTable';
import { Mono } from '../../components/Mono';
import { EmptyState } from '../../components/states';
import type { Column } from '../../components/tableCore';
import { artifactLabel, countOf, formatInt, relativeTime, scannedName } from '../../format';
import { buttonClass } from '../../ui';
import { DistributionMini } from '../../viz';
import { SCAN_KIND_LABEL, SectionError } from './parts';

const columns: Column<ScanSummary>[] = [
  {
    key: 'scanned',
    header: 'Scanned',
    cell: (s) => (
      <time dateTime={s.scanned_at} title={s.scanned_at} className="whitespace-nowrap">
        {relativeTime(s.scanned_at)}
      </time>
    ),
  },
  {
    key: 'artifact',
    header: 'Artifact',
    cell: (s) => (
      <Link
        to={`/scans/${s.id}`}
        className="block max-w-72 font-medium text-text hover:underline"
        aria-label={`Open the scan of ${scannedName(s) ?? artifactLabel(s.artifact_key, s.kind)} from pipeline ${s.gitlab_pipeline_id}`}
      >
        <ArtifactName artifact={s} truncate />
        <ArtifactDetail artifact={s} className="font-normal" />
      </Link>
    ),
  },
  { key: 'kind', header: 'Kind', cell: (s) => SCAN_KIND_LABEL[s.kind] ?? s.kind },
  {
    key: 'pipeline',
    header: 'Pipeline',
    numeric: true,
    cell: (s) => <Mono value={`#${s.gitlab_pipeline_id}`} />,
  },
  { key: 'commit', header: 'Commit', cell: (s) => <Mono value={s.commit_sha} kind="sha" /> },
  {
    key: 'counts',
    header: 'Findings',
    width: 'w-44',
    cell: (s) =>
      s.kind === 'secret-fs' ? (
        <span className="text-text-muted">{countOf(s.secrets, 'secret')}</span>
      ) : (
        <DistributionMini counts={s.counts} showTotal label={`Findings of scan ${s.id}`} />
      ),
  },
];

/** Every scan of the shown ref, newest first, in pages. */
export function RecentScans({ serviceId, gitRef }: { serviceId: number; gitRef?: string }) {
  const query = useServiceScans(serviceId, gitRef);
  const rows = useMemo(() => query.data?.pages.flatMap((p) => p.items) ?? [], [query.data]);

  return (
    <section aria-labelledby="recent-scans" className="min-w-0">
      <div className="mb-4 flex flex-wrap items-end justify-between gap-3">
        <div>
          <h2 id="recent-scans" className="text-h3 font-semibold tracking-tight text-text">
            Recent scans
          </h2>
          <p className="mt-0.5 text-body-s text-text-muted">
            Every upload of this branch, newest first
          </p>
        </div>
        {rows.length > 0 ? (
          <p className="text-body-s text-text-muted tabular-nums">{formatInt(rows.length)} shown</p>
        ) : null}
      </div>
      {query.isError ? (
        <SectionError
          what="Recent scans"
          error={query.error}
          onRetry={() => void query.refetch()}
          retrying={query.isFetching}
        />
      ) : (
        <>
          <DataTable
            caption="Recent scans"
            columns={columns}
            rows={rows}
            rowKey={(s) => s.id}
            loading={query.isPending}
            scrollClass="max-h-[28rem]"
            empty={
              <EmptyState
                compact
                art="timeline"
                title="No scans yet"
                body="Scans appear here after a pipeline on this branch uploads a Trivy report."
              />
            }
          />
          {query.hasNextPage ? (
            <div className="flex justify-center pt-4">
              <button
                type="button"
                className={buttonClass('secondary')}
                disabled={query.isFetchingNextPage}
                onClick={() => void query.fetchNextPage()}
              >
                {query.isFetchingNextPage ? 'Loading' : 'Load more scans'}
              </button>
            </div>
          ) : null}
        </>
      )}
    </section>
  );
}
