// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { usePortfolio } from '../api/portfolio';
import { PageHeader } from '../components/layout';
import { ErrorState, Skeleton, SkeletonStat } from '../components/states';
import { PortfolioView } from './portfolio/PortfolioView';
import { SkeletonTile } from './portfolio/ServiceTile';

export function PortfolioPage({ now }: { now?: Date } = {}) {
  const query = usePortfolio();

  if (query.isPending) {
    return (
      <>
        <PageHeader
          title="Portfolio"
          subtitle="Every service with its open vulnerabilities, quality gate and freshness."
        />
        <div className="mb-8 grid grid-cols-2 gap-4 xl:grid-cols-4">
          {[0, 1, 2, 3].map((i) => (
            <SkeletonStat key={i} />
          ))}
        </div>
        <Skeleton className="mb-6 h-10 w-full sm:w-80" />
        <ul
          role="status"
          aria-label="Loading services"
          className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-4"
        >
          {[0, 1, 2, 3, 4, 5].map((i) => (
            <SkeletonTile key={i} />
          ))}
        </ul>
      </>
    );
  }
  if (query.isError) {
    return (
      <>
        <PageHeader title="Portfolio" />
        <ErrorState
          title="The portfolio could not be loaded"
          message="Scanopsis could not reach the server. Your data is unchanged."
          onRetry={() => void query.refetch()}
          retrying={query.isRefetching}
        />
      </>
    );
  }
  return <PortfolioView data={query.data} now={now} />;
}
