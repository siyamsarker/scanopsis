// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { Link, useParams } from 'react-router';
import { ApiError } from '../api/client';
import { useScan, useScanDiff, useScanService } from '../api/scan';
import { Card, Section } from '../components/layout';
import { EmptyState, ErrorState, Skeleton, SkeletonTable } from '../components/states';
import { Changes } from './scan/Changes';
import { Posture } from './scan/Posture';
import { ScanHeader, ScanHeaderSkeleton } from './scan/ScanHeader';

function message(error: unknown) {
  return error instanceof ApiError ? error.message : 'The request failed. Try again.';
}

function DiffSection({ id }: { id: number }) {
  const diff = useScanDiff(id);
  if (diff.isPending) {
    return (
      <Section title="Changes since the previous scan">
        <Skeleton className="mb-6 h-28 w-full rounded-card" />
        <Card padded={false}>
          <SkeletonTable columns={5} />
        </Card>
      </Section>
    );
  }
  if (diff.isError) {
    return (
      <Section title="Changes since the previous scan">
        <Card padded={false}>
          <ErrorState
            title="The changes could not be loaded"
            message={message(diff.error)}
            onRetry={() => void diff.refetch()}
            retrying={diff.isFetching}
            compact
          />
        </Card>
      </Section>
    );
  }
  return <Changes diff={diff.data} />;
}

export function ScanPage() {
  const params = useParams();
  const id = Number(params.id);
  const scan = useScan(id);
  const service = useScanService(scan.data?.service_id);

  if (scan.isPending) {
    return (
      <div className="mx-auto max-w-6xl p-6">
        <ScanHeaderSkeleton />
        <Skeleton className="h-48 w-full rounded-card" />
      </div>
    );
  }
  if (scan.isError) {
    const missing = scan.error instanceof ApiError && scan.error.status === 404;
    return (
      <div className="mx-auto max-w-6xl p-6">
        {missing ? (
          <EmptyState
            title="Scan not found"
            body="It may have been removed, or the link is wrong."
            action={
              <Link to="/" className="underline underline-offset-2">
                Back to the portfolio
              </Link>
            }
          />
        ) : (
          <ErrorState
            title="The scan could not be loaded"
            message={message(scan.error)}
            onRetry={() => void scan.refetch()}
            retrying={scan.isFetching}
          />
        )}
      </div>
    );
  }

  const data = scan.data;
  return (
    <div className="mx-auto max-w-6xl p-6">
      <ScanHeader
        scan={data}
        serviceName={service.data?.name ?? (service.isError ? `Service ${data.service_id}` : '…')}
      />
      <Posture scan={data} />
      <div className="mt-8">
        <DiffSection id={id} />
      </div>
    </div>
  );
}
