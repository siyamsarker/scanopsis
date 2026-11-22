// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { KeyRound, Wrench, EyeOff } from 'lucide-react';
import type { ReactNode } from 'react';
import { Link } from 'react-router';
import type { Overview } from '../../api/service';
import { ArtifactName } from '../../components/ArtifactName';
import type { SeverityCountValues } from '../../components/severityModel';
import { countOf, formatExactTime, formatInt, relativeTime } from '../../format';
import { SeverityBar, type Severity } from '../../viz';
import { findingsHref } from './links';
import { SCAN_KIND_LABEL } from './parts';

const ZERO: SeverityCountValues = { critical: 0, high: 0, medium: 0, low: 0, unknown: 0 };

const total = (c: SeverityCountValues) => c.critical + c.high + c.medium + c.low + c.unknown;

/** Splits by_class into OS packages, language packages and everything else. */
export function classCounts(byClass: Overview['by_class']) {
  const other = { ...ZERO };
  for (const [name, counts] of Object.entries(byClass)) {
    if (name === 'os-pkgs' || name === 'lang-pkgs') continue;
    for (const key of Object.keys(other) as (keyof SeverityCountValues)[]) {
      other[key] += counts[key] ?? 0;
    }
  }
  return {
    os: byClass['os-pkgs'] ?? ZERO,
    lang: byClass['lang-pkgs'] ?? ZERO,
    other,
    hasOther: total(other) > 0,
  };
}

const SEVERITY_PARAM: Record<Severity, string> = {
  critical: 'CRITICAL',
  high: 'HIGH',
  medium: 'MEDIUM',
  low: 'LOW',
  unknown: 'UNKNOWN',
};

function Fact({
  to,
  icon,
  value,
  label,
}: {
  to?: string;
  icon: ReactNode;
  value: number;
  label: string;
}) {
  const body = (
    <>
      <span className="text-text-muted">{icon}</span>
      <span className="font-semibold text-text tabular-nums">{formatInt(value)}</span>{' '}
      <span className="text-text-muted">{label}</span>
    </>
  );
  const className =
    'inline-flex min-h-9 items-center gap-1.5 rounded-inner border border-line px-3 text-body-s';
  return to ? (
    <Link
      to={to}
      className={`${className} transition-colors duration-micro hover:border-line-strong hover:bg-wash`}
    >
      {body}
    </Link>
  ) : (
    <span className={className}>{body}</span>
  );
}

function ClassRow({
  label,
  counts,
  max,
}: {
  label: string;
  counts: SeverityCountValues;
  max: number;
}) {
  return (
    <div className="grid grid-cols-[8.5rem_minmax(0,1fr)_3rem] items-center gap-3">
      <span className="text-body-s text-text-muted">{label}</span>
      <SeverityBar
        counts={counts}
        size="md"
        max={max}
        label={`${label}, open findings`}
        legend={false}
      />
      <span className="text-right text-body-s text-text tabular-nums">
        {formatInt(total(counts))}
      </span>
    </div>
  );
}

/**
 * The current findings of the shown ref: by severity (each segment opens the
 * matching findings), where they live (OS against language packages), the
 * secrets and what has a fix, and the latest scan of every artifact.
 */
export function OpenFindings({ overview, serviceId }: { overview: Overview; serviceId: number }) {
  const at = (filter?: Record<string, string>) => findingsHref(serviceId, overview.ref, filter);
  const classes = classCounts(overview.by_class);
  const max = Math.max(total(classes.os), total(classes.lang), total(classes.other), 1);

  return (
    <section aria-labelledby="open-findings" className="panel flex flex-col gap-5 p-5 sm:p-6">
      <div>
        <h2 id="open-findings" className="text-body font-semibold tracking-tight text-text">
          Open findings
        </h2>
        <p className="mt-0.5 text-body-s text-text-muted">
          Current state of <span className="font-mono">{overview.ref}</span>, latest scan per
          artifact
        </p>
      </div>

      <SeverityBar
        counts={overview.counts}
        size="lg"
        label={`Open findings of ${overview.ref}`}
        hrefFor={(s) => at({ severity: SEVERITY_PARAM[s] })}
      />

      <div className="flex flex-wrap gap-2">
        <Fact
          to={at({ fixable: 'true' })}
          icon={<Wrench size={14} strokeWidth={1.75} aria-hidden="true" />}
          value={overview.fixable}
          label="fixable"
        />
        <Fact
          to={at({ tab: 'secrets' })}
          icon={<KeyRound size={14} strokeWidth={1.75} aria-hidden="true" />}
          value={overview.secrets}
          label={overview.secrets === 1 ? 'secret' : 'secrets'}
        />
        {overview.suppressed > 0 ? (
          <Fact
            to={at({ suppressed: 'true' })}
            icon={<EyeOff size={14} strokeWidth={1.75} aria-hidden="true" />}
            value={overview.suppressed}
            label="suppressed"
          />
        ) : null}
      </div>

      <div className="space-y-2.5 border-t border-line pt-4">
        <h3 className="text-body-s font-medium text-text">Where they are</h3>
        <ClassRow label="OS packages" counts={classes.os} max={max} />
        <ClassRow label="Language packages" counts={classes.lang} max={max} />
        {classes.hasOther ? <ClassRow label="Other" counts={classes.other} max={max} /> : null}
      </div>

      <div className="border-t border-line pt-4">
        <h3 className="text-body-s font-medium text-text">Artifacts</h3>
        <ul className="mt-1 -mx-2">
          {overview.artifacts.map((scan) => (
            <li key={scan.id}>
              <Link
                to={`/scans/${scan.id}`}
                className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-1 rounded-inner px-2 py-2 transition-colors duration-micro hover:bg-wash"
              >
                <span className="min-w-0">
                  <ArtifactName
                    name={scan.artifact_name}
                    kind={scan.kind}
                    truncate
                    className="text-body-s font-medium text-text"
                  />
                  <span className="mt-0.5 block text-caption text-text-muted">
                    {SCAN_KIND_LABEL[scan.kind] ?? scan.kind}, pipeline #{scan.gitlab_pipeline_id},{' '}
                    <time
                      dateTime={scan.scanned_at}
                      title={formatExactTime(new Date(scan.scanned_at))}
                    >
                      {relativeTime(scan.scanned_at)}
                    </time>
                  </span>
                </span>
                <span className="text-right text-body-s text-text-muted tabular-nums">
                  {scan.kind === 'secret-fs'
                    ? countOf(scan.secrets, 'secret')
                    : countOf(total(scan.counts), 'vulnerability', 'vulnerabilities')}
                </span>
              </Link>
            </li>
          ))}
        </ul>
      </div>
    </section>
  );
}
