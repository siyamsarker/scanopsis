// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { Link } from 'react-router';
import { useOverview, type Overview } from '../../api/service';
import { ArtifactName } from '../../components/ArtifactName';
import { Mono } from '../../components/Mono';
import { SeverityBar, SeverityCounts } from '../../components/Severity';
import { StatTile } from '../../components/StatTile';
import { Card, CardHeader, Grid } from '../../components/layout';
import type { SeverityCountValues } from '../../components/severityModel';
import { EmptyState, Skeleton, SkeletonStat } from '../../components/states';
import { countOf, formatShortDate, relativeTime } from '../../format';
import { findingsHref } from './links';
import { SCAN_KIND_LABEL, SectionError } from './parts';

const GITLAB_CI_GUIDE =
  'https://github.com/siyamsarker/scanopsis/blob/main/docs/integrations/gitlab-ci.md';

const ZERO: SeverityCountValues = { critical: 0, high: 0, medium: 0, low: 0, unknown: 0 };

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
    hasOther: Object.values(other).some((n) => n > 0),
  };
}

function PostureSkeleton() {
  return (
    <div className="space-y-6" role="status" aria-label="Loading Trivy posture">
      <Card className="space-y-3">
        <Skeleton className="h-5 w-40" />
        <Skeleton className="h-4 w-full" />
        <Skeleton className="h-4 w-2/3" />
      </Card>
      <Grid cols={4}>
        {[0, 1, 2, 3].map((i) => (
          <SkeletonStat key={i} />
        ))}
      </Grid>
      <Grid cols={2}>
        <SkeletonStat />
        <SkeletonStat />
      </Grid>
      <Card padded={false}>
        <div className="space-y-3 p-6">
          <Skeleton className="h-5 w-32" />
          {[0, 1, 2, 3, 4].map((i) => (
            <Skeleton key={i} className="h-12 w-full" />
          ))}
        </div>
      </Card>
    </div>
  );
}

export function TrivyPosture({ serviceId, gitRef }: { serviceId: number; gitRef?: string }) {
  const query = useOverview(serviceId, gitRef);
  if (query.isPending) return <PostureSkeleton />;
  if (query.isError) {
    return (
      <SectionError
        what="Trivy posture"
        error={query.error}
        onRetry={() => void query.refetch()}
        retrying={query.isFetching}
      />
    );
  }
  const o = query.data;
  if (o.ref !== null && o.artifacts.length === 0) {
    const chosen = o.ref_source === 'default';
    return (
      <Card padded={false}>
        <EmptyState
          title={`No scans of ${o.ref} yet`}
          body={
            (chosen
              ? 'An administrator chose this branch for the dashboards. '
              : 'No pipeline on this branch has uploaded a report. ') +
            (o.refs.length > 0
              ? 'Other scanned branches are available in the Git ref list.'
              : 'Scans appear here after a pipeline on it uploads a Trivy report.')
          }
          action={
            <a
              className="text-brand underline underline-offset-2"
              href={GITLAB_CI_GUIDE}
              target="_blank"
              rel="noopener noreferrer"
            >
              Read the GitLab CI guide
            </a>
          }
        />
      </Card>
    );
  }
  if (o.ref === null) {
    return (
      <Card padded={false}>
        <EmptyState
          title="No Trivy scans yet"
          body="Scans appear here after a pipeline of this project uploads its first Trivy report."
          action={
            <a
              className="text-brand underline underline-offset-2"
              href={GITLAB_CI_GUIDE}
              target="_blank"
              rel="noopener noreferrer"
            >
              Read the GitLab CI guide
            </a>
          }
        />
      </Card>
    );
  }
  const at = (filter?: Record<string, string>) => findingsHref(serviceId, o.ref, filter);
  const classes = classCounts(o.by_class);

  return (
    <div className="space-y-6">
      <Card className="space-y-4">
        <CardHeader
          title="Findings by severity"
          description={`Current state of ${o.ref}, latest scan per artifact`}
        />
        <SeverityBar counts={o.counts} size="lg" showSummary />
        <div className="grid gap-x-8 gap-y-2 border-t border-border pt-4 sm:grid-cols-[auto_1fr] sm:items-center">
          <span className="text-sm font-medium text-text-muted">OS packages</span>
          <SeverityCounts counts={classes.os} />
          <span className="text-sm font-medium text-text-muted">Language packages</span>
          <SeverityCounts counts={classes.lang} />
          {classes.hasOther ? (
            <>
              <span className="text-sm font-medium text-text-muted">Other</span>
              <SeverityCounts counts={classes.other} />
            </>
          ) : null}
        </div>
      </Card>

      <Grid cols={4}>
        <StatTile
          label="Critical"
          value={o.counts.critical}
          tone="critical"
          to={at({ severity: 'CRITICAL' })}
        />
        <StatTile label="High" value={o.counts.high} tone="high" to={at({ severity: 'HIGH' })} />
        <StatTile
          label="Fixable"
          value={o.fixable}
          hint="A fixed version exists"
          tone="pass"
          to={at({ fixable: 'true' })}
        />
        <StatTile
          label="Secrets"
          value={o.secrets}
          hint="Found in files"
          tone={o.secrets > 0 ? 'high' : undefined}
          to={at({ tab: 'secrets' })}
        />
      </Grid>

      <Grid cols={2}>
        <StatTile
          label="New since previous scan"
          value={o.new_since_previous}
          hint="Findings not in the previous scan"
        />
        <StatTile
          label="Fixed since previous scan"
          value={o.fixed_since_previous}
          hint="Findings gone since the previous scan"
        />
      </Grid>

      <Card padded={false}>
        <CardHeader
          padded
          title="Fix first"
          description="Packages whose upgrade clears the most findings"
        />
        {o.top_packages.length === 0 ? (
          <EmptyState compact title="No vulnerable packages" body="Trivy reported none." />
        ) : (
          <ol className="divide-y divide-border border-t border-border">
            {o.top_packages.map((pkg, index) => (
              <li key={`${pkg.pkg_name}@${pkg.installed_version}`}>
                <Link
                  to={at({ q: pkg.pkg_name })}
                  className="flex flex-wrap items-center gap-x-4 gap-y-2 px-6 py-3 transition-colors duration-150 ease-out hover:bg-surface-raised"
                >
                  <span aria-hidden="true" className="w-5 text-sm tabular-nums text-text-muted">
                    {index + 1}
                  </span>
                  <span className="min-w-0 flex-1 basis-48">
                    <Mono value={pkg.pkg_name} className="font-medium text-text" />
                    <span className="mt-0.5 block text-sm text-text-muted">
                      <Mono value={pkg.installed_version} />
                      {pkg.fix_version ? (
                        <>
                          <span aria-hidden="true"> → </span>
                          <span className="sr-only"> upgrade to </span>
                          <Mono value={pkg.fix_version} className="text-text" />
                        </>
                      ) : (
                        <span> · No fix available</span>
                      )}
                    </span>
                  </span>
                  <span className="flex flex-col items-start gap-1 sm:items-end">
                    <span className="text-sm font-medium tabular-nums text-text">
                      {countOf(pkg.findings, 'finding')}
                    </span>
                    <SeverityCounts counts={pkg.by_severity} hideZero />
                  </span>
                </Link>
              </li>
            ))}
          </ol>
        )}
      </Card>

      <Card padded={false}>
        <CardHeader padded title="Artifacts" description="Latest scan of each artifact" />
        <ul className="divide-y divide-border border-t border-border">
          {o.artifacts.map((scan) => (
            <li key={scan.id}>
              <Link
                to={`/scans/${scan.id}`}
                className="grid gap-x-4 gap-y-2 px-6 py-3 transition-colors duration-150 ease-out hover:bg-surface-raised sm:grid-cols-[1fr_12rem]"
              >
                <span className="min-w-0">
                  <ArtifactName
                    name={scan.artifact_name}
                    kind={scan.kind}
                    truncate
                    className="font-medium text-text"
                  />
                  <span className="mt-0.5 block text-sm text-text-muted">
                    {SCAN_KIND_LABEL[scan.kind] ?? scan.kind} · scanned{' '}
                    <time dateTime={scan.scanned_at} title={scan.scanned_at}>
                      {relativeTime(scan.scanned_at)}
                    </time>{' '}
                    ({formatShortDate(scan.scanned_at)})
                  </span>
                </span>
                <span className="flex flex-col justify-center gap-1">
                  {scan.kind === 'secret-fs' ? (
                    // A secret scan reports secrets, not vulnerabilities.
                    <span className="text-sm tabular-nums text-text-muted">
                      {countOf(scan.secrets, 'secret')}
                    </span>
                  ) : (
                    <>
                      <SeverityBar counts={scan.counts} size="sm" />
                      <span className="text-sm tabular-nums text-text-muted">
                        {countOf(
                          scan.counts.critical +
                            scan.counts.high +
                            scan.counts.medium +
                            scan.counts.low +
                            scan.counts.unknown,
                          'vulnerability',
                          'vulnerabilities',
                        )}
                      </span>
                    </>
                  )}
                </span>
              </Link>
            </li>
          ))}
        </ul>
      </Card>
    </div>
  );
}
