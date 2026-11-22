// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { ArrowUpRight, Info } from 'lucide-react';
import { Link } from 'react-router';
import { useNewerScanId, type Scan } from '../../api/scan';
import { countOf, formatInt } from '../../format';
import { linkClass } from '../../ui';
import { RiskIris, SeverityBar, explainRisk, riskPoints, type Severity } from '../../viz';

/** The findings explorer for this scan's service and ref, with extra query parameters. */
export function findingsHref(scan: Scan, extra: string) {
  return `/services/${scan.service_id}/findings?ref=${encodeURIComponent(scan.ref)}${extra}`;
}

function Stat({ label, value, to }: { label: string; value: number; to: string }) {
  return (
    <Link
      to={to}
      className="group flex min-w-0 items-baseline gap-2 rounded-inner px-3 py-2 transition-colors duration-micro hover:bg-wash"
    >
      <span className="text-h3 font-semibold text-text tabular-nums">{formatInt(value)}</span>
      <span className="text-body-s text-text-muted">{label}</span>
      <ArrowUpRight
        size={14}
        strokeWidth={1.75}
        aria-hidden="true"
        className="text-text-faint opacity-0 transition-opacity duration-micro group-hover:opacity-100"
      />
    </Link>
  );
}

export function Posture({ scan }: { scan: Scan }) {
  const newer = useNewerScanId(scan);
  const risk = riskPoints(scan.counts);
  const total =
    scan.counts.critical +
    scan.counts.high +
    scan.counts.medium +
    scan.counts.low +
    scan.counts.unknown;

  return (
    <section aria-labelledby="posture-title" className="mb-10">
      <h2 id="posture-title" className="sr-only">
        Posture
      </h2>
      {newer.data != null ? (
        <p
          role="note"
          className="mb-4 flex items-center gap-2 rounded-inner border border-line bg-sunken px-4 py-3 text-body-s text-text-muted"
        >
          <Info size={16} strokeWidth={1.75} aria-hidden="true" className="shrink-0" />
          <span>
            A newer scan of this artifact exists.{' '}
            <Link to={`/scans/${newer.data}`} className={linkClass}>
              Open the newer scan
            </Link>
            . The findings explorer shows the current state of this ref.
          </span>
        </p>
      ) : null}
      {scan.kind === 'secret-fs' ? (
        <div className="panel flex flex-wrap items-center gap-6 px-6 py-5">
          <p className="flex items-baseline gap-3">
            <span className="text-display-s font-semibold text-text tabular-nums">
              {formatInt(scan.secrets)}
            </span>
            <span className="text-body text-text-muted">
              {scan.secrets === 1 ? 'secret' : 'secrets'} found in the source tree
            </span>
          </p>
          <Link
            to={`/services/${scan.service_id}/findings?ref=${encodeURIComponent(scan.ref)}&tab=secrets`}
            className={linkClass}
          >
            See where they are
          </Link>
          {scan.suppressed > 0 ? (
            <span className="text-body-s text-text-muted">
              {countOf(scan.suppressed, 'suppressed finding')}
            </span>
          ) : null}
        </div>
      ) : (
        <div className="panel grid items-center gap-6 px-5 py-5 sm:grid-cols-[auto_minmax(0,1fr)] sm:px-6">
          <RiskIris counts={scan.counts} size={96} name={scan.artifact_name} animate />
          <div className="min-w-0 space-y-3">
            <p className="text-body-s text-text-muted">
              <span className="font-semibold text-text">{countOf(total, 'open finding')}</span>{' '}
              worth <span className="font-semibold text-text tabular-nums">{formatInt(risk)}</span>{' '}
              risk points. <span className="sr-only">{explainRisk(scan.counts)}</span>
            </p>
            <SeverityBar
              counts={scan.counts}
              size="lg"
              label="Open findings in this scan"
              hrefFor={(s: Severity) => findingsHref(scan, `&severity=${s.toUpperCase()}`)}
            />
            <div className="-mx-3 flex flex-wrap gap-x-2">
              <Stat label="fixable" value={scan.fixable} to={findingsHref(scan, '&fixable=true')} />
              <Stat
                label="suppressed"
                value={scan.suppressed}
                to={findingsHref(scan, '&suppressed=true')}
              />
              <Stat
                label={scan.secrets === 1 ? 'secret' : 'secrets'}
                value={scan.secrets}
                to={`/services/${scan.service_id}/findings?ref=${encodeURIComponent(scan.ref)}&tab=secrets`}
              />
            </div>
          </div>
        </div>
      )}
    </section>
  );
}
