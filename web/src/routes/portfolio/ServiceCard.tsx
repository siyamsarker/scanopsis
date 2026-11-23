// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { GitBranch } from 'lucide-react';
import { m } from 'motion/react';
import { forwardRef } from 'react';
import { Link } from 'react-router';
import type { PortfolioItem, ServiceHistory } from '../../api/portfolio';
import { TargetLink } from '../../components/Freshness';
import { Skeleton } from '../../components/states';
import { formatInt, formatPercent, relativeTime } from '../../format';
import {
  Delta,
  GateGlyph,
  PipelineSpark,
  RiskIris,
  SeverityGlyph,
  SEVERITIES,
  SEVERITY_NAME,
  useWidth,
} from '../../viz';
import { gateOf, isStale, riskChange, sparkPoints } from './model';

export const CI_GUIDE_URL =
  'https://github.com/siyamsarker/scanopsis/blob/main/docs/integrations/gitlab-ci.md';

const GATE_TEXT = { pass: 'Gate passed', fail: 'Gate failed', none: 'No gate' } as const;

/**
 * The open findings as glyph and count per severity, zeros left out, read out
 * as words, and how many of them have a fixed version.
 */
export function CountsLine({
  counts,
  fixable,
}: {
  counts: PortfolioItem['counts'];
  fixable: number;
}) {
  const present = SEVERITIES.filter((s) => counts[s] > 0);
  if (present.length === 0) {
    return <p className="text-body-s text-text-muted">No open findings</p>;
  }
  return (
    <p className="flex flex-wrap items-center gap-x-3 gap-y-1 text-body-s text-text tabular-nums">
      {present.map((s, i) => (
        <span key={s} className="inline-flex items-center gap-1">
          <SeverityGlyph severity={s} />
          {counts[s]}
          <span className="sr-only">
            {' '}
            {SEVERITY_NAME[s].toLowerCase()}
            {i < present.length - 1 ? ', ' : ''}
          </span>
        </span>
      ))}
      {fixable > 0 ? (
        <span className="ml-auto text-text-muted">
          <span className="sr-only">; </span>
          {formatInt(fixable)} fixable
        </span>
      ) : null}
    </p>
  );
}

function Spark({ points, gitRef }: { points: ServiceHistory['points']; gitRef: string }) {
  const [box, width] = useWidth<HTMLDivElement>(220);
  return (
    <div ref={box} className="min-w-0 flex-1">
      <PipelineSpark
        points={sparkPoints(points, gitRef)}
        width={Math.max(80, Math.floor(width))}
        height={30}
        limit={20}
        label={`Open findings added and removed, last ${points.length - 1} pipelines`}
      />
    </div>
  );
}

/**
 * One service: its risk iris, where it lives, how its last pipelines moved
 * the open findings, and the quality gate, coverage and freshness. The name
 * link covers the whole card; the iris stays focusable for its explanation.
 */
export const ServiceCard = forwardRef<
  HTMLLIElement,
  {
    item: PortfolioItem;
    history?: ServiceHistory['points'];
    historyLoading?: boolean;
    now: Date;
    animate?: boolean;
  }
>(function ServiceCard({ item, history, historyLoading = false, now, animate = false }, ref) {
  const { service, counts } = item;
  const scanned = item.last_scan_at !== null;
  const gate = gateOf(item);
  const change = riskChange(history);
  const stale = isStale(item, now);

  return (
    <m.li
      ref={ref}
      initial={{ opacity: 0, scale: 0.96 }}
      animate={{ opacity: 1, scale: 1, transition: { duration: 0.2, ease: [0.2, 0.8, 0.2, 1] } }}
      exit={{ opacity: 0, scale: 0.96, transition: { duration: 0.2, ease: [0.4, 0, 1, 1] } }}
      className="group/card relative flex flex-col gap-4 rounded-panel border border-line bg-surface p-5 shadow-1 transition-[border-color,background-color,transform,box-shadow] duration-micro ease-enter focus-within:border-line-strong hover:-translate-y-px hover:border-line-strong hover:bg-surface-raised hover:shadow-2 motion-reduce:transform-none"
    >
      <div className="flex items-start gap-4">
        <div className="relative z-10 shrink-0">
          <RiskIris
            counts={scanned ? counts : null}
            gate={gate}
            size={96}
            name={service.name}
            animate={animate}
          />
        </div>
        <div className="min-w-0 flex-1 pt-1">
          <h3 className="truncate text-body font-semibold tracking-tight text-text">
            <Link
              to={`/services/${service.id}`}
              className="rounded-[4px] after:absolute after:inset-0 after:rounded-panel after:content-['']"
            >
              {service.name}
            </Link>
          </h3>
          <p className="truncate font-mono text-small text-text-muted">
            {service.gitlab_project_path}
          </p>
          {item.ref ? (
            <p
              className="mt-1 flex min-w-0 items-center gap-1 text-caption text-text-muted"
              title={
                item.ref_source === 'default'
                  ? 'The dashboard branch an administrator chose'
                  : 'The latest scanned branch; no dashboard branch is chosen'
              }
            >
              <GitBranch size={12} strokeWidth={1.75} aria-hidden="true" className="shrink-0" />
              <span className="sr-only">Branch </span>
              <span className="truncate font-mono">{item.ref}</span>
            </p>
          ) : null}
          {change ? (
            <p className="mt-2 text-caption text-text-muted">
              <Delta
                value={change.delta}
                unit="risk points"
                since={`since pipeline #${change.sincePipeline}`}
                className="mr-1 text-body-s font-medium"
              />
              <span aria-hidden="true">since #{change.sincePipeline}</span>
            </p>
          ) : null}
        </div>
      </div>

      {scanned ? (
        <>
          <CountsLine counts={counts} fixable={item.fixable} />
          <div className="flex min-h-[30px] items-center gap-3">
            {history && history.length > 1 ? (
              <Spark points={history} gitRef={item.ref ?? ''} />
            ) : historyLoading ? (
              <Skeleton className="h-[30px] flex-1" />
            ) : (
              <p className="text-caption text-text-faint">
                The spark fills in from the second pipeline.
              </p>
            )}
          </div>
        </>
      ) : (
        <p className="rounded-inner border border-dashed border-line-strong px-3 py-2 text-body-s text-text-muted">
          {item.ref ? `No scans of ${item.ref} yet.` : 'No scans yet.'}{' '}
          <TargetLink
            target={{ label: 'Add the upload step', href: CI_GUIDE_URL }}
            className="relative z-10 text-text underline underline-offset-2"
          />
        </p>
      )}

      <dl className="mt-auto flex flex-wrap items-center gap-x-4 gap-y-1 border-t border-line pt-3 text-caption text-text-muted">
        <div className="inline-flex items-center gap-1.5">
          <dt className="sr-only">Quality gate</dt>
          <dd className="inline-flex items-center gap-1.5">
            {gate ? <GateGlyph gate={gate} size={12} /> : null}
            {gate ? GATE_TEXT[gate] : 'No SonarQube analysis yet'}
          </dd>
        </div>
        {item.last_analysis_at ? (
          <div className="inline-flex gap-1">
            <dt>Coverage</dt>
            <dd className="text-text tabular-nums">
              {item.coverage === null ? 'not reported' : formatPercent(item.coverage)}
            </dd>
          </div>
        ) : null}
        {item.last_scan_at ? (
          <div className="ml-auto inline-flex items-center gap-1">
            <dt className="sr-only">Last scan</dt>
            <dd className={stale ? 'text-warn' : undefined}>
              <time dateTime={item.last_scan_at} title={item.last_scan_at}>
                {stale ? 'Stale, scanned ' : 'Scanned '}
                {relativeTime(item.last_scan_at, now)}
              </time>
            </dd>
          </div>
        ) : null}
      </dl>
    </m.li>
  );
});

export function SkeletonCard() {
  return (
    <li
      aria-hidden="true"
      className="flex min-h-64 flex-col gap-4 rounded-panel border border-line bg-surface p-5"
    >
      <div className="flex gap-4">
        <Skeleton className="size-24 rounded-full" />
        <div className="flex-1 space-y-2 pt-1">
          <Skeleton className="h-5 w-2/3" />
          <Skeleton className="h-4 w-full" />
          <Skeleton className="h-3 w-1/3" />
        </div>
      </div>
      <Skeleton className="h-4 w-3/4" />
      <Skeleton className="h-7 w-full" />
      <Skeleton className="mt-auto h-4 w-full" />
    </li>
  );
}
