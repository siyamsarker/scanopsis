// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { useBackups } from '../api/backups';
import { useMe } from '../api/hooks';
import { usePortfolio } from '../api/portfolio';
import { PageHeader } from '../components/layout';
import { ErrorState, Skeleton } from '../components/states';
import { scheduleAlertShown } from './admin/backups/model';
import { ScheduleFailedNotice } from './admin/backups/notices';
import { PortfolioView } from './portfolio/PortfolioView';
import { SkeletonCard } from './portfolio/ServiceCard';

const TITLE = 'Portfolio';

export function PortfolioPage({ now }: { now?: Date } = {}) {
  const query = usePortfolio();
  // Administrators learn here when the last scheduled backup failed.
  const me = useMe();
  const backups = useBackups(me.data?.role === 'admin', false);
  const schedule = backups.data?.schedule;

  if (query.isPending) {
    return (
      <>
        <PageHeader title={TITLE} subtitle="Every service ranked by risk." />
        <div className="panel mb-8 grid gap-6 p-6 lg:grid-cols-[5fr_7fr]" aria-hidden="true">
          <div className="space-y-4">
            <Skeleton className="h-4 w-28" />
            <Skeleton className="h-12 w-48" />
            <Skeleton className="h-11 w-full" />
            <Skeleton className="h-2.5 w-full rounded-full" />
          </div>
          <div className="space-y-3">
            {[0, 1, 2, 3].map((i) => (
              <Skeleton key={i} className="h-10 w-full" />
            ))}
          </div>
        </div>
        <Skeleton className="mb-6 h-10 w-full sm:w-80" />
        <ul
          role="status"
          aria-label="Loading services"
          className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3"
        >
          {[0, 1, 2, 3, 4, 5].map((i) => (
            <SkeletonCard key={i} />
          ))}
        </ul>
      </>
    );
  }
  if (query.isError) {
    return (
      <>
        <PageHeader title={TITLE} />
        <ErrorState
          title="The portfolio could not be loaded"
          message="Scanopsis could not reach the server. Your data is unchanged."
          onRetry={() => void query.refetch()}
          retrying={query.isRefetching}
        />
      </>
    );
  }
  return (
    <PortfolioView
      data={query.data}
      now={now}
      notice={
        scheduleAlertShown(schedule) ? <ScheduleFailedNotice schedule={schedule} link /> : undefined
      }
    />
  );
}
