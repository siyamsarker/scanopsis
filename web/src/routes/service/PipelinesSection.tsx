// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { useMemo } from 'react';
import { useNavigate } from 'react-router';
import { useSonar, useTrends } from '../../api/service';
import { Segmented } from '../../components/Segmented';
import { Skeleton } from '../../components/states';
import { countOf, formatInt } from '../../format';
import { PipelineStrip } from '../../viz';
import { coverageSource, pipelineKey, pipelinePoints } from './model';
import { SectionError } from './parts';

export const DAY_OPTIONS = ['30', '90', '180'] as const;
export type Days = (typeof DAY_OPTIONS)[number];
export type Lanes = 'ref' | 'all';

const GITLAB_CI_GUIDE =
  'https://github.com/siyamsarker/scanopsis/blob/main/docs/integrations/gitlab-ci.md';

/**
 * The spine of the service page: one column per pipeline, what it added
 * above the line and what it fixed below, its quality gate and coverage.
 * Choosing a column opens the scan behind it.
 */
export function PipelinesSection({
  serviceId,
  gitRef,
  shownRef,
  days,
  lanes,
  onDaysChange,
  onLanesChange,
  animate,
}: {
  serviceId: number;
  gitRef?: string;
  /** The ref the page shows, for the words. */
  shownRef?: string | null;
  days: Days;
  lanes: Lanes;
  onDaysChange: (days: Days) => void;
  onLanesChange: (lanes: Lanes) => void;
  animate: boolean;
}) {
  const navigate = useNavigate();
  const trends = useTrends(serviceId, gitRef, Number(days), lanes === 'all');
  const sonar = useSonar(serviceId);
  const coverage = useMemo(
    () => coverageSource(sonar.data?.latest, sonar.data?.history ?? []),
    [sonar.data],
  );
  const strip = useMemo(
    () => pipelinePoints(trends.data?.points ?? [], sonar.data?.history ?? [], coverage),
    [trends.data, sonar.data, coverage],
  );
  const where = lanes === 'all' ? 'on every branch' : shownRef ? `on ${shownRef}` : '';
  const truncated = trends.data?.truncated === true;

  return (
    <section aria-labelledby="pipelines-title" className="mb-12">
      <div className="mb-4 flex flex-wrap items-end justify-between gap-x-6 gap-y-3">
        <div className="min-w-0">
          <h2 id="pipelines-title" className="text-h3 font-semibold tracking-tight text-text">
            Pipelines
          </h2>
          <p className="mt-0.5 max-w-prose text-body-s text-text-muted">
            What each pipeline changed: new findings above the line by severity, fixed findings
            below it. Choose a pipeline to open its scan.
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Segmented<Lanes>
            label="Branches"
            value={lanes}
            onChange={onLanesChange}
            options={[
              { value: 'ref', label: 'This branch' },
              { value: 'all', label: 'All branches' },
            ]}
          />
          <Segmented<Days>
            label="Time range in days"
            value={days}
            onChange={onDaysChange}
            options={DAY_OPTIONS.map((d) => ({ value: d, label: `${d} days` }))}
          />
        </div>
      </div>

      {trends.isPending ? (
        <div role="status" aria-label="Loading pipelines">
          <Skeleton className="h-64 w-full" />
        </div>
      ) : trends.isError ? (
        <SectionError
          what="The pipelines"
          error={trends.error}
          onRetry={() => void trends.refetch()}
          retrying={trends.isFetching}
        />
      ) : (
        <>
          <PipelineStrip
            key={lanes}
            points={strip.points}
            gateCoverageThreshold={coverage.threshold}
            coverageLabel={coverage.label}
            lanes
            defaultSplit={lanes === 'all'}
            animate={animate}
            label={`Pipelines ${where} in the last ${days} days`}
            onSelect={(p) => {
              const scan = strip.scanOf.get(pipelineKey(p.ref, p.pipelineId));
              if (scan !== undefined) void navigate(`/scans/${scan}`);
            }}
            emptyMessage={
              <>
                No pipeline {where} uploaded a scan in the last {days} days.{' '}
                {days !== '180' ? (
                  <button
                    type="button"
                    className="font-medium text-text underline underline-offset-2"
                    onClick={() => onDaysChange('180')}
                  >
                    Show 180 days
                  </button>
                ) : (
                  <a
                    href={GITLAB_CI_GUIDE}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="font-medium text-text underline underline-offset-2"
                  >
                    Read the GitLab CI guide
                  </a>
                )}
              </>
            }
          />
          {strip.totals.pipelines > 0 ? (
            <p className="mt-3 text-body-s text-text-muted tabular-nums" data-testid="strip-totals">
              {countOf(strip.totals.pipelines, 'pipeline')} {where} in the last {days} days:{' '}
              <span className="text-text">{formatInt(strip.totals.newFindings)} new</span> and{' '}
              <span className="text-text">{formatInt(strip.totals.fixed)} fixed</span>
              {truncated && trends.data
                ? `. The strip holds the newest ${formatInt(trends.data.points.length)} of the ${formatInt(trends.data.total)} scans in this range.`
                : '.'}
              {strip.totals.baselines > 0
                ? ' The first scan of an artifact is its starting point and does not count as new.'
                : ''}
            </p>
          ) : null}
        </>
      )}
    </section>
  );
}
