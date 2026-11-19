// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useMemo } from 'react';
import { Link } from 'react-router';
import { useServiceScans, useSonar, useTrends, type ScanSummary } from '../../api/service';
import { Chart } from '../../charts/Chart';
import { lineSeries, stackedSeverityBars } from '../../charts/options';
import { ArtifactName } from '../../components/ArtifactName';
import { Mono } from '../../components/Mono';
import { Segmented } from '../../components/Segmented';
import { SeverityBar } from '../../components/Severity';
import { DataTable } from '../../components/DataTable';
import { Card, CardHeader } from '../../components/layout';
import { EmptyState } from '../../components/states';
import type { Column } from '../../components/tableCore';
import { formatInt, formatShortDate, relativeTime, shortSha } from '../../format';
import { secondaryButtonClass } from '../../ui';
import { SCAN_KIND_LABEL, SectionError } from './parts';

export const DAY_OPTIONS = ['30', '90', '180'] as const;
export type Days = (typeof DAY_OPTIONS)[number];

export function TrendsChart({
  serviceId,
  gitRef,
  days,
  onDaysChange,
}: {
  serviceId: number;
  gitRef?: string;
  days: Days;
  onDaysChange: (days: Days) => void;
}) {
  const query = useTrends(serviceId, gitRef, Number(days));
  // Secret scans have no vulnerability counts and would show as empty bars.
  const points = useMemo(() => query.data?.points.filter((p) => p.kind === 'image'), [query.data]);
  const option = useMemo(
    () =>
      stackedSeverityBars(
        (points ?? []).map((p) => ({
          label: `${formatShortDate(p.scanned_at)} #${p.gitlab_pipeline_id}`,
          counts: p.counts,
          detail: `${p.artifact_name} · ${shortSha(p.commit_sha)}`,
        })),
      ),
    [points],
  );
  const status = query.isPending
    ? 'loading'
    : query.isError
      ? 'error'
      : points && points.length === 0
        ? 'empty'
        : 'ready';
  return (
    <Card padded={false} className="min-w-0">
      <CardHeader
        padded
        title="How did the findings change scan by scan?"
        description="One bar per image scan, critical at the bottom. Secret scans have no vulnerability counts and are not shown."
        actions={
          <Segmented
            label="Time range in days"
            value={days}
            onChange={onDaysChange}
            options={DAY_OPTIONS.map((d) => ({ value: d, label: `${d} days` }))}
          />
        }
      />
      <div className="px-4 pb-4">
        <Chart
          option={option}
          ariaLabel={`Findings per scan, last ${days} days`}
          heightClass="h-72"
          status={status}
          emptyTitle="No scans in this period"
          emptyBody="Choose a longer range, or wait for the next pipeline."
          errorMessage="The scan history could not be loaded."
          onRetry={() => void query.refetch()}
        />
      </div>
    </Card>
  );
}

export function CoverageChart({ serviceId }: { serviceId: number }) {
  const query = useSonar(serviceId);
  const history = query.data?.history;
  const { categories, values } = useMemo(() => {
    const oldestFirst = [...(history ?? [])].reverse();
    return {
      categories: oldestFirst.map((a) => formatShortDate(a.analysed_at)),
      // An analysis without coverage is a gap, never a zero.
      values: oldestFirst.map((a) => a.coverage),
    };
  }, [history]);
  const option = useMemo(
    () => lineSeries({ categories, series: [{ name: 'Coverage', values }], percent: true }),
    [categories, values],
  );
  const reported = values.some((v) => v !== null);
  const status = query.isPending
    ? 'loading'
    : query.isError
      ? 'error'
      : reported
        ? 'ready'
        : 'empty';
  return (
    <Card padded={false} className="min-w-0">
      <CardHeader
        padded
        title="Is test coverage moving?"
        description="Coverage per SonarQube analysis"
      />
      <div className="px-4 pb-4">
        <Chart
          option={option}
          ariaLabel="Coverage per SonarQube analysis"
          heightClass="h-72"
          status={status}
          emptyTitle="No coverage reported"
          emptyBody="SonarQube has not reported coverage for this project."
          errorMessage="The coverage history could not be loaded."
          onRetry={() => void query.refetch()}
        />
      </div>
    </Card>
  );
}

export function RecentScans({ serviceId, gitRef }: { serviceId: number; gitRef?: string }) {
  const query = useServiceScans(serviceId, gitRef);
  const rows = useMemo(() => query.data?.pages.flatMap((p) => p.items) ?? [], [query.data]);

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
        <ArtifactName name={s.artifact_name} kind={s.kind} truncate className="max-w-64" />
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
      cell: (s) => <SeverityBar counts={s.counts} size="sm" />,
    },
    {
      key: 'open',
      header: 'Scan',
      cell: (s) => (
        <Link
          to={`/scans/${s.id}`}
          className="text-brand underline underline-offset-2"
          aria-label={`Open scan ${s.id}`}
        >
          Open
        </Link>
      ),
    },
  ];

  return (
    <section aria-labelledby="recent-scans" className="min-w-0">
      <h2 id="recent-scans" className="pb-4 text-lg font-semibold tracking-tight text-text">
        Recent scans
      </h2>
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
                title="No scans yet"
                body="Scans appear here after a pipeline uploads a Trivy report."
              />
            }
          />
          {query.hasNextPage ? (
            <div className="flex justify-center pt-4">
              <button
                type="button"
                className={secondaryButtonClass}
                disabled={query.isFetchingNextPage}
                onClick={() => void query.fetchNextPage()}
              >
                {query.isFetchingNextPage ? 'Loading' : 'Load more'}
              </button>
            </div>
          ) : null}
          {rows.length > 0 ? (
            <p className="pt-2 text-center text-sm text-text-muted tabular-nums">
              {formatInt(rows.length)} scans shown
            </p>
          ) : null}
        </>
      )}
    </section>
  );
}
