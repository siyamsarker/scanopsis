// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { Link, useParams, useSearchParams } from 'react-router';
import { ApiError } from '../api/client';
import { useScan, useScanDiff, useScanService, type Scan } from '../api/scan';
import { EmptyState, ErrorState, Skeleton } from '../components/states';
import { buttonClass } from '../ui';
import { Changes } from './scan/Changes';
import { Posture } from './scan/Posture';
import { ScanHeader, ScanHeaderSkeleton } from './scan/ScanHeader';
import './findings/findings.css';

function message(error: unknown) {
  return error instanceof ApiError ? error.message : 'The request failed. Try again.';
}

function DiffSection({ scan }: { scan: Scan }) {
  const [params, setParams] = useSearchParams();
  const baseParam = params.get('base');
  const base = baseParam && /^\d+$/.test(baseParam) ? Number(baseParam) : null;
  const diff = useScanDiff(scan.id, base);

  function setBase(id: number | null) {
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (id === null) next.delete('base');
        else next.set('base', String(id));
        return next;
      },
      { replace: true },
    );
  }

  if (diff.isPending) {
    return (
      <section aria-busy="true">
        <Skeleton className="mb-4 h-8 w-48" />
        <Skeleton className="h-48 w-full rounded-panel" />
      </section>
    );
  }
  if (diff.isError) {
    const badBase = diff.error instanceof ApiError && diff.error.status === 400 && base !== null;
    return (
      <section aria-labelledby="changes-error">
        <h2 id="changes-error" className="pb-4 text-h3 font-semibold tracking-tight text-text">
          What changed
        </h2>
        <div className="panel">
          {badBase ? (
            <EmptyState
              compact
              art="lost"
              title="That scan cannot be compared"
              body="Only an earlier scan of the same artifact can be compared with this one."
              action={
                <button
                  type="button"
                  className={buttonClass('secondary')}
                  onClick={() => setBase(null)}
                >
                  Compare with the previous scan
                </button>
              }
            />
          ) : (
            <ErrorState
              title="The changes could not be loaded"
              message={message(diff.error)}
              onRetry={() => void diff.refetch()}
              retrying={diff.isFetching}
              compact
            />
          )}
        </div>
      </section>
    );
  }
  return (
    <Changes
      scan={scan}
      diff={diff.data}
      onBaseChange={setBase}
      switching={diff.isPlaceholderData}
    />
  );
}

export function ScanPage() {
  const params = useParams();
  const id = Number(params.id);
  const scan = useScan(id);
  const service = useScanService(scan.data?.service_id);

  if (scan.isPending) {
    return (
      <div>
        <ScanHeaderSkeleton />
        <Skeleton className="h-40 w-full rounded-panel" />
      </div>
    );
  }
  if (scan.isError) {
    const missing = scan.error instanceof ApiError && scan.error.status === 404;
    return (
      <div className="panel">
        {missing ? (
          <EmptyState
            art="lost"
            title="Scan not found"
            body="It may have been removed by retention, or the link is wrong."
            action={
              <Link to="/" className={buttonClass('secondary')}>
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
    <div>
      <ScanHeader
        scan={data}
        serviceName={service.data?.name ?? (service.isError ? `Service ${data.service_id}` : '…')}
      />
      <Posture scan={data} />
      {/* Diffs compare vulnerabilities; a secret scan has none to compare. */}
      {data.kind === 'image' ? <DiffSection key={data.id} scan={data} /> : null}
    </div>
  );
}
