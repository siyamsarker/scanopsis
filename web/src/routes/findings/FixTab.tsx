// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { ArrowRight } from 'lucide-react';
import { useMemo, useState } from 'react';
import { Link } from 'react-router';
import type { Vulnerability } from '../../api/findings';
import { ApiError } from '../../api/client';
import { CopyIconButton } from '../../components/CopyIconButton';
import { SeverityMark } from '../../components/SeverityMark';
import { SEVERITY_ORDER } from '../../components/severityModel';
import { EmptyState, ErrorState, Skeleton } from '../../components/states';
import { countOf, formatInt, formatPercent } from '../../format';
import { linkClass } from '../../ui';
import { riskPoints } from '../../viz';
import { RiskBar } from './RiskBar';
import { countSeverities, upgradeGroups, upgradeCommand, type UpgradeGroup } from './model';

/** How many upgrades the plan proposes before the reader picks another cut. */
const PLAN = 5;

/** Link to the vulnerabilities tab filtered to one package, keeping the ref. */
export function packageLink(serviceId: number, gitRef: string | null, pkg: string) {
  const params = new URLSearchParams();
  if (gitRef) params.set('ref', gitRef);
  params.set('q', pkg);
  return `/services/${serviceId}/findings?${params.toString()}`;
}

function UpgradeRow({
  group,
  rank,
  max,
  total,
  inPlan,
  serviceId,
  urlRef,
  onPick,
}: {
  group: UpgradeGroup;
  rank: number;
  max: number;
  total: number;
  inPlan: boolean;
  serviceId: number;
  urlRef: string | null;
  onPick: (rank: number) => void;
}) {
  const command = upgradeCommand(group.pkgType, group.pkg, group.upgradeTo);
  const ids = group.vulnIds.slice(0, 3);
  return (
    <li
      data-in-plan={inPlan || undefined}
      onMouseEnter={() => onPick(rank)}
      onFocus={() => onPick(rank)}
      className={`upgrade-row grid grid-cols-[2rem_minmax(0,1fr)] gap-x-3 gap-y-2 border-b border-line px-1 py-4 last:border-b-0 sm:px-2 lg:grid-cols-[2.5rem_minmax(0,1.3fr)_minmax(12rem,1fr)] lg:gap-x-6 ${inPlan ? '' : 'opacity-80'}`}
    >
      <span
        className={`mt-0.5 inline-flex size-7 items-center justify-center rounded-full text-small font-semibold tabular-nums ${inPlan ? 'lens-ring text-text' : 'border border-line text-text-muted'}`}
        aria-label={`Rank ${rank}`}
      >
        {rank}
      </span>
      <div className="min-w-0">
        <p className="flex flex-wrap items-center gap-x-2 gap-y-1 font-mono text-body-s">
          <Link
            to={packageLink(serviceId, urlRef, group.pkg)}
            className="font-medium text-text hover:underline"
          >
            {group.pkg}
          </Link>
          <span className="text-text-muted">{group.installed}</span>
          <ArrowRight size={14} strokeWidth={1.75} aria-hidden="true" className="text-text-faint" />
          <span className="sr-only">to</span>
          <span className="tone-ok tone-ink font-medium">{group.upgradeTo}</span>
        </p>
        {command ? (
          <div className="mt-1.5 flex max-w-full items-center gap-1">
            <code className="min-w-0 truncate font-mono text-small text-text-muted" title={command}>
              {command}
            </code>
            <CopyIconButton text={command} label={`Copy the upgrade command for ${group.pkg}`} />
          </div>
        ) : null}
        <p className="mt-1 text-small text-text-faint">
          Clears {ids.join(', ')}
          {group.vulnIds.length > ids.length
            ? ` and ${group.vulnIds.length - ids.length} more`
            : ''}
          {group.artifacts.length > 0 ? ` in ${group.artifacts.join(', ')}` : ''}
        </p>
      </div>
      <div className="col-start-2 min-w-0 self-center lg:col-start-3">
        <div className="flex items-baseline justify-between gap-3 text-small">
          <span className="flex items-center gap-2 text-text-muted">
            {SEVERITY_ORDER.filter((s) => group.counts[s] > 0).map((s) => (
              <span key={s} className="inline-flex items-center gap-1 tabular-nums">
                <SeverityMark severity={s} size={10} />
                {formatInt(group.counts[s])}
                <span className="sr-only"> {s}</span>
              </span>
            ))}
          </span>
          <span className="tabular-nums">
            <span className="font-semibold text-text">−{formatInt(group.risk)}</span>
            <span className="text-text-muted"> pts</span>
            <span className="text-text-faint">
              {' '}
              · {formatPercent((group.risk / Math.max(1, total)) * 100, { digits: 0 })}
            </span>
          </span>
        </div>
        <RiskBar counts={group.counts} max={max} className="mt-2 h-2" />
      </div>
    </li>
  );
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
  const open = useMemo(() => rows.filter((v) => !v.suppressed), [rows]);
  const allCounts = useMemo(() => countSeverities(open), [open]);
  const [picked, setPicked] = useState<number | null>(null);

  if (error && rows.length === 0) {
    return (
      <ErrorState
        title="Vulnerabilities could not be loaded"
        message={error instanceof ApiError ? error.message : 'Try again in a moment.'}
        onRetry={onRetry}
      />
    );
  }
  if (loading) {
    return (
      <div className="space-y-4" aria-busy="true">
        <Skeleton className="h-40 w-full rounded-panel" />
        <Skeleton className="h-64 w-full rounded-panel" />
      </div>
    );
  }
  if (groups.length === 0) {
    return (
      <div className="panel">
        <EmptyState
          art="clean"
          title="No fixable vulnerabilities"
          body="Nothing in the current scans has a fixed version to upgrade to."
        />
      </div>
    );
  }

  // Totals of a list that stopped loading are lower bounds, never the full picture.
  const incomplete = rows.length > 0 && (Boolean(error) || truncated);
  const atLeast = incomplete ? 'at least ' : '';
  const plan = Math.min(picked ?? PLAN, groups.length);
  const planned = groups.slice(0, plan);
  const totalRisk = riskPoints(allCounts);
  const fixableRisk = groups.reduce((sum, g) => sum + g.risk, 0);
  const cleared = planned.reduce((sum, g) => sum + g.risk, 0);
  const clearedFindings = planned.reduce((sum, g) => sum + g.cleared, 0);
  const remaining = totalRisk - cleared;
  const maxItem = groups[0].risk;

  return (
    <div className="space-y-6">
      {incomplete ? (
        <div role="alert" className="panel border-line-strong px-5 py-4 text-body-s text-text">
          Loading stopped after {formatInt(rows.length)} findings; these totals are incomplete.
          {error ? (
            <>
              {' '}
              <button type="button" onClick={onRetry} className="underline underline-offset-2">
                Try again
              </button>
            </>
          ) : null}
        </div>
      ) : null}

      <section aria-labelledby="plan-title" className="panel px-5 py-5 sm:px-6">
        <h2 id="plan-title" className="text-body font-semibold text-text">
          Upgrade plan
        </h2>
        <p className="mt-3 flex flex-wrap items-baseline gap-x-3 gap-y-1">
          <span className="text-display-s font-semibold tracking-tight text-text tabular-nums">
            −{formatInt(cleared)}
          </span>
          <span className="text-body text-text-muted" aria-live="polite">
            risk points: upgrading {countOf(plan, 'package')} clears {atLeast}
            {formatInt(cleared)} of {atLeast}
            {formatInt(totalRisk)} (
            {formatPercent((cleared / Math.max(1, totalRisk)) * 100, { digits: 0 })},{' '}
            {countOf(clearedFindings, 'finding')})
          </span>
        </p>
        <div className="mt-4">
          <svg
            viewBox="0 0 1000 16"
            preserveAspectRatio="none"
            role="img"
            aria-label={`${formatInt(cleared)} of ${formatInt(totalRisk)} risk points cleared by the plan, ${formatInt(fixableRisk - cleared)} more fixable, ${formatInt(totalRisk - fixableRisk)} without a fix`}
            className="block h-4 w-full overflow-hidden rounded-full bg-line/60"
          >
            <rect
              x={0}
              y={0}
              height={16}
              width={(cleared / Math.max(1, totalRisk)) * 1000}
              className="fill-ok"
            />
            <rect
              x={(cleared / Math.max(1, totalRisk)) * 1000 + 3}
              y={0}
              height={16}
              width={Math.max(0, ((fixableRisk - cleared) / Math.max(1, totalRisk)) * 1000 - 3)}
              className="fill-ok opacity-30"
            />
          </svg>
          <ul className="mt-3 flex flex-wrap gap-x-5 gap-y-1 text-small text-text-muted">
            <li className="inline-flex items-center gap-2">
              <span aria-hidden="true" className="size-2.5 rounded-full bg-ok" />
              Cleared by these {countOf(plan, 'upgrade')}
            </li>
            <li className="inline-flex items-center gap-2">
              <span aria-hidden="true" className="size-2.5 rounded-full bg-ok opacity-30" />
              {formatInt(fixableRisk - cleared)} pts more fixable
            </li>
            <li className="inline-flex items-center gap-2">
              <span aria-hidden="true" className="size-2.5 rounded-full bg-line" />
              {formatInt(totalRisk - fixableRisk)} pts without a fix
            </li>
            <li className="ml-auto tabular-nums">
              Left after the plan:{' '}
              <span className="font-semibold text-text">{formatInt(remaining)}</span> pts
            </li>
          </ul>
        </div>
        <p className="mt-4 text-small text-text-muted">
          Point at a package to see the plan up to it. Each package goes to the highest fixed
          version among its findings; check ecosystem rules such as Debian epochs first. Suppressed
          findings are left out.
          {loadingMore
            ? ` Loaded ${formatInt(rows.length)} so far; the plan updates as more arrive.`
            : ''}
        </p>
      </section>

      <section aria-labelledby="ranked-title">
        <div className="flex flex-wrap items-baseline justify-between gap-2 pb-2">
          <h2 id="ranked-title" className="text-body font-semibold text-text">
            {countOf(groups.length, 'package')} to upgrade, by risk cleared
          </h2>
          {picked !== null && picked !== PLAN ? (
            <button
              type="button"
              className={`${linkClass} text-small`}
              onClick={() => setPicked(null)}
            >
              Back to the first {PLAN}
            </button>
          ) : null}
        </div>
        <ol
          aria-label="Packages to upgrade"
          onMouseLeave={() => setPicked(null)}
          className="border-t border-line"
        >
          {groups.map((group, index) => (
            <UpgradeRow
              key={group.key}
              group={group}
              rank={index + 1}
              max={maxItem}
              total={totalRisk}
              inPlan={index < plan}
              serviceId={serviceId}
              urlRef={urlRef}
              onPick={setPicked}
            />
          ))}
        </ol>
      </section>
    </div>
  );
}
