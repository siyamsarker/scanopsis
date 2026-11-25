// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { useRef } from 'react';
import { Link } from 'react-router';
import { useCssom } from '../../components/cssom';
import type { Overview } from '../../api/service';
import { EmptyState } from '../../components/states';
import { countOf, formatInt } from '../../format';
import { riskPoints, SeverityGlyph, SEVERITIES, SEVERITY_NAME } from '../../viz';
import { findingsHref } from './links';

type Package = Overview['top_packages'][number];

function Share({ share }: { share: number }) {
  const ref = useRef<HTMLSpanElement>(null);
  useCssom(ref, { width: `${Math.max(2, Math.min(100, share * 100)).toFixed(1)}%` });
  return (
    <span aria-hidden="true" className="block h-1 w-full overflow-hidden rounded-full bg-sunken">
      <span
        ref={ref}
        className="block h-full rounded-full bg-[linear-gradient(90deg,var(--lens-a),var(--lens-b))]"
      />
    </span>
  );
}

function Row({
  pkg,
  rank,
  total,
  href,
}: {
  pkg: Package;
  rank: number;
  total: number;
  href: string;
}) {
  const fixable = pkg.fix_version !== '' && pkg.risk_cleared > 0;
  // With a fix, the counts are those of the findings the upgrade fixes, as in
  // the upgrade plan; without one, those of the findings that stay open.
  const shown = fixable ? pkg.fixable_by_severity : pkg.by_severity;
  const summary = fixable
    ? `${countOf(pkg.fixable, 'finding')} fixed`
    : countOf(pkg.findings, 'finding');
  return (
    <li>
      <Link
        to={href}
        className="group grid grid-cols-[1.5rem_minmax(0,1fr)_auto] items-start gap-x-3 gap-y-1.5 rounded-inner px-3 py-3 transition-colors duration-micro hover:bg-wash"
      >
        <span aria-hidden="true" className="pt-0.5 text-body-s text-text-faint tabular-nums">
          {rank}
        </span>
        <span className="min-w-0">
          <span className="block truncate font-mono text-body-s font-medium text-text">
            {pkg.pkg_name}
          </span>
          <span className="mt-0.5 block truncate font-mono text-caption text-text-muted">
            {pkg.installed_version}
            {fixable ? (
              <>
                <span aria-hidden="true"> → </span>
                <span className="sr-only"> upgrade to </span>
                <span className="text-text">{pkg.fix_version}</span>
              </>
            ) : null}
          </span>
          {fixable ? (
            <span className="mt-2 block max-w-64">
              <Share share={total > 0 ? pkg.risk_cleared / total : 0} />
            </span>
          ) : null}
        </span>
        <span className="flex flex-col items-end gap-1 text-right">
          {fixable ? (
            <span className="text-body-s font-semibold text-text tabular-nums">
              −{formatInt(pkg.risk_cleared)}
              <span className="font-normal text-text-muted"> risk</span>
            </span>
          ) : (
            <span className="text-caption text-text-muted">No fixed version yet</span>
          )}
          <span
            className="flex flex-wrap justify-end gap-x-2 text-caption text-text-muted tabular-nums"
            title={summary}
          >
            <span className="sr-only">{summary}: </span>
            {SEVERITIES.filter((s) => shown[s] > 0).map((s) => (
              <span key={s} className="inline-flex items-center gap-1">
                <SeverityGlyph severity={s} size={9} />
                {shown[s]}
                <span className="sr-only"> {SEVERITY_NAME[s].toLowerCase()}</span>
              </span>
            ))}
          </span>
        </span>
      </Link>
    </li>
  );
}

/**
 * The upgrades that clear the most risk, each with the exact version to move
 * to and the risk points it removes, as a share of the service's risk.
 */
export function FixFirst({ overview, serviceId }: { overview: Overview; serviceId: number }) {
  const total = riskPoints(overview.counts);
  const packages = overview.top_packages;
  const cleared = packages.reduce((sum, p) => sum + (p.fix_version ? p.risk_cleared : 0), 0);
  return (
    <section aria-labelledby="fix-first" className="panel flex flex-col p-2 sm:p-3">
      <div className="px-3 pt-3 pb-2">
        <h2 id="fix-first" className="text-body font-semibold tracking-tight text-text">
          Fix first
        </h2>
        <p className="mt-0.5 text-body-s text-text-muted">
          Upgrades that clear the most risk on <span className="font-mono">{overview.ref}</span>
        </p>
      </div>
      {packages.length === 0 ? (
        <EmptyState
          compact
          art="clean"
          headingLevel={3}
          title="Nothing to upgrade"
          body="Trivy reported no vulnerable packages in the latest scans."
        />
      ) : (
        <>
          <ol aria-label="Packages to upgrade" className="flex flex-col">
            {packages.map((pkg, i) => (
              <Row
                key={`${pkg.pkg_name}@${pkg.installed_version}`}
                pkg={pkg}
                rank={i + 1}
                total={total}
                href={findingsHref(serviceId, overview.ref, { q: pkg.pkg_name })}
              />
            ))}
          </ol>
          <p className="mt-auto border-t border-line px-3 pt-3 pb-2 text-body-s text-text-muted">
            {cleared > 0
              ? `These upgrades clear ${formatInt(cleared)} of ${formatInt(total)} risk points. `
              : ''}
            <Link
              to={findingsHref(serviceId, overview.ref, { tab: 'fix' })}
              className="font-medium text-text underline decoration-line-strong underline-offset-2 hover:decoration-text"
            >
              See every upgrade
            </Link>
          </p>
        </>
      )}
    </section>
  );
}
