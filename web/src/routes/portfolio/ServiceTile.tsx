// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { Link } from 'react-router';
import type { PortfolioItem } from '../../api/portfolio';
import { RatingBadge, GateBadge, type GateStatus } from '../../components/badges';
import { Freshness, TargetLink } from '../../components/Freshness';
import { Mono } from '../../components/Mono';
import { SeverityBar, type SeverityLinks } from '../../components/Severity';
import { SEVERITY_ORDER, summarizeCounts } from '../../components/severityModel';
import { Skeleton } from '../../components/states';
import { formatInt, formatPercent } from '../../format';
import { STALE_AFTER_MS } from './model';

export const CI_GUIDE_URL =
  'https://github.com/siyamsarker/scanopsis/blob/main/docs/integrations/gitlab-ci.md';

/** Same minimum height for the tile and its skeleton, so loading does not shift the page. */
const TILE_BOX = 'min-h-72';

function findingLinks(id: number): SeverityLinks {
  const links: SeverityLinks = {};
  for (const key of SEVERITY_ORDER) links[key] = `/services/${id}/findings?severity=${key}`;
  return links;
}

export function ServiceTile({ item, now }: { item: PortfolioItem; now?: Date }) {
  const { service, counts } = item;
  const scanned = item.last_scan_at !== null;
  const analysed = item.last_analysis_at !== null;
  return (
    <li
      className={`${TILE_BOX} flex flex-col gap-4 rounded-card border border-border bg-surface p-5 shadow-[inset_0_1px_0_color-mix(in_srgb,var(--text)_5%,transparent)] transition-colors duration-150 ease-out hover:border-border-strong`}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h3 className="truncate text-base font-semibold tracking-tight text-text">
            <Link to={`/services/${service.id}`} className="hover:underline">
              {service.name}
            </Link>
          </h3>
          <Mono value={service.gitlab_project_path} truncate className="text-sm text-text-muted" />
        </div>
        <GateBadge status={item.quality_gate_status as GateStatus} className="shrink-0" />
      </div>

      {scanned ? (
        <div>
          <SeverityBar counts={counts} links={findingLinks(service.id)} />
          <p className="mt-2 flex flex-wrap justify-between gap-x-3 text-sm text-text-muted tabular-nums">
            <span>{summarizeCounts(counts)}</span>
            <span>{formatInt(item.fixable)} fixable</span>
          </p>
        </div>
      ) : (
        <p className="rounded-control border border-dashed border-border-strong px-3 py-2 text-sm text-text-muted">
          No scans yet.{' '}
          <TargetLink
            target={{ label: 'Add the upload step', href: CI_GUIDE_URL }}
            className="text-text underline underline-offset-2"
          />
        </p>
      )}

      {analysed ? (
        <>
          <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
            <RatingBadge label="Reliability" rating={item.reliability_rating} />
            <RatingBadge label="Security" rating={item.security_rating} />
            <RatingBadge label="Maintainability" rating={item.maintainability_rating} />
          </div>

          <p className="text-sm text-text-muted">
            Coverage{' '}
            <span className="font-medium text-text tabular-nums">
              {item.coverage === null ? 'Not available' : formatPercent(item.coverage)}
            </span>
          </p>
        </>
      ) : (
        <p className="text-sm text-text-muted">No SonarQube analysis yet</p>
      )}

      <div className="mt-auto flex flex-col gap-1">
        {item.last_scan_at ? (
          <Freshness
            at={item.last_scan_at}
            prefix="Last scan"
            staleAfterMs={STALE_AFTER_MS}
            now={now}
          />
        ) : null}
        {item.last_analysis_at ? (
          <Freshness at={item.last_analysis_at} prefix="Last analysis" now={now} />
        ) : null}
      </div>
    </li>
  );
}

export function SkeletonTile() {
  return (
    <li
      aria-hidden="true"
      className={`${TILE_BOX} rounded-card border border-border bg-surface p-5`}
    >
      <Skeleton className="h-5 w-1/2" />
      <Skeleton className="mt-2 h-4 w-3/4" />
      <Skeleton className="mt-6 h-2.5 w-full rounded-full" />
      <Skeleton className="mt-3 h-4 w-2/3" />
      <Skeleton className="mt-6 h-7 w-full" />
      <Skeleton className="mt-6 h-4 w-1/3" />
    </li>
  );
}
