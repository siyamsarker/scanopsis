// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useMemo } from 'react';
import { Link } from 'react-router';
import type { Vulnerability } from '../../api/findings';
import { ApiError } from '../../api/client';
import { Card } from '../../components/layout';
import { DataTable } from '../../components/DataTable';
import { Mono } from '../../components/Mono';
import { SeverityBar, SeverityCounts } from '../../components/Severity';
import { EmptyState, ErrorState } from '../../components/states';
import type { Column } from '../../components/tableCore';
import { countOf, formatInt } from '../../format';
import { upgradeGroups, type UpgradeGroup } from './model';

const EMPHASISED = 5;

/** Link to the vulnerabilities tab filtered to one package, keeping the ref. */
export function packageLink(serviceId: number, gitRef: string | null, pkg: string) {
  const params = new URLSearchParams();
  if (gitRef) params.set('ref', gitRef);
  params.set('q', pkg);
  return `/services/${serviceId}/findings?${params.toString()}`;
}

export function FixTab({
  serviceId,
  urlRef,
  rows,
  loading,
  loadingMore,
  truncated = false,
  error,
  onRetry,
}: {
  serviceId: number;
  urlRef: string | null;
  rows: Vulnerability[];
  loading: boolean;
  loadingMore: boolean;
  /** Loading stopped at the row ceiling although more findings exist. */
  truncated?: boolean;
  error: unknown;
  onRetry: () => void;
}) {
  const groups = useMemo(() => upgradeGroups(rows), [rows]);

  if (error && rows.length === 0) {
    return (
      <ErrorState
        title="Vulnerabilities could not be loaded"
        message={error instanceof ApiError ? error.message : 'Try again in a moment.'}
        onRetry={onRetry}
      />
    );
  }

  // Totals of a list that stopped loading are lower bounds, never the full picture.
  const incomplete = rows.length > 0 && (Boolean(error) || truncated);
  const top = groups.slice(0, EMPHASISED);
  const total = groups.reduce((sum, g) => sum + g.cleared, 0);
  const cleared = top.reduce((sum, g) => sum + g.cleared, 0);

  const columns: Column<UpgradeGroup>[] = [
    {
      key: 'rank',
      header: 'Order',
      width: 'w-20',
      cell: (g) => {
        const rank = groups.indexOf(g) + 1;
        return rank <= EMPHASISED ? (
          <span className="chip tone-neutral font-semibold tabular-nums">{rank}</span>
        ) : (
          <span className="tabular-nums text-text-muted">{rank}</span>
        );
      },
    },
    {
      key: 'package',
      header: 'Package',
      cell: (g) => (
        <Link
          to={packageLink(serviceId, urlRef, g.pkg)}
          className="underline decoration-border-strong underline-offset-2 hover:decoration-text"
        >
          <Mono value={g.pkg} />
        </Link>
      ),
    },
    {
      key: 'versions',
      header: 'Upgrade',
      cell: (g) => (
        <span className="inline-flex items-center gap-1.5">
          <Mono value={g.installed} />
          <span aria-hidden="true" className="text-text-muted">
            →
          </span>
          <span className="sr-only">to</span>
          <Mono value={g.upgradeTo} className="font-semibold" />
        </span>
      ),
    },
    {
      key: 'cleared',
      header: 'Clears',
      numeric: true,
      cell: (g) => countOf(g.cleared, 'finding'),
    },
    {
      key: 'severity',
      header: 'By severity',
      cell: (g) => <SeverityCounts counts={g.counts} hideZero />,
    },
  ];

  return (
    <div className="space-y-4">
      {incomplete ? (
        <div role="alert" className="rounded-card border border-border-strong bg-surface p-4">
          <p className="text-text">
            Loading stopped after {formatInt(rows.length)} findings; these totals are incomplete.
            {error ? (
              <>
                {' '}
                <button type="button" onClick={onRetry} className="underline underline-offset-2">
                  Try again
                </button>
              </>
            ) : null}
          </p>
        </div>
      ) : null}
      {groups.length > 0 ? (
        <Card className="space-y-3">
          <p className="text-lg font-semibold tracking-tight text-text tabular-nums">
            Upgrading {countOf(top.length, 'package')} clears {incomplete ? 'at least ' : ''}
            {formatInt(cleared)} of {incomplete ? 'at least ' : ''}
            {formatInt(total)} fixable {total === 1 ? 'vulnerability' : 'vulnerabilities'}
          </p>
          <SeverityBar
            size="sm"
            counts={top.reduce(
              (acc, g) => ({
                critical: acc.critical + g.counts.critical,
                high: acc.high + g.counts.high,
                medium: acc.medium + g.counts.medium,
                low: acc.low + g.counts.low,
                unknown: acc.unknown + g.counts.unknown,
              }),
              { critical: 0, high: 0, medium: 0, low: 0, unknown: 0 },
            )}
          />
          <p className="text-sm text-text-muted">
            The first {EMPHASISED} are numbered. Each package is upgraded to the highest fixed
            version among its findings; check ecosystem rules such as Debian epochs before
            upgrading. Suppressed findings are left out.
          </p>
          {loadingMore ? (
            <p role="status" className="text-sm text-text-muted tabular-nums">
              Loaded {formatInt(rows.length)} so far, this list updates as more arrive
            </p>
          ) : null}
        </Card>
      ) : null}
      <DataTable
        caption="Packages to upgrade"
        columns={columns}
        rows={groups}
        rowKey={(g) => g.key}
        loading={loading}
        empty={
          <EmptyState
            compact
            title="No fixable vulnerabilities"
            body="Nothing in the current scans has a fixed version to upgrade to."
          />
        }
      />
    </div>
  );
}
