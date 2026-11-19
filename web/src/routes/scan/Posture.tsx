// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { Info } from 'lucide-react';
import { Link } from 'react-router';
import { useNewerScanId, type Scan } from '../../api/scan';
import { Card, Grid, Section } from '../../components/layout';
import { SeverityBar, SeverityCounts, type SeverityLinks } from '../../components/Severity';
import { SEVERITY_ORDER } from '../../components/severityModel';
import { StatTile } from '../../components/StatTile';

/** The findings explorer for this scan's service and ref, with extra query parameters. */
export function findingsHref(scan: Scan, extra: string) {
  return `/services/${scan.service_id}/findings?ref=${encodeURIComponent(scan.ref)}${extra}`;
}

export function Posture({ scan }: { scan: Scan }) {
  const newer = useNewerScanId(scan);
  const links: SeverityLinks = {};
  for (const key of SEVERITY_ORDER)
    links[key] = findingsHref(scan, `&severity=${key.toUpperCase()}`);

  return (
    <Section title="Posture" description="Findings in this scan, as Trivy reported them.">
      {newer.data != null ? (
        <p
          role="note"
          className="mb-4 flex items-center gap-2 rounded-card border border-border bg-surface px-4 py-3 text-sm text-text-muted"
        >
          <Info size={16} strokeWidth={1.5} aria-hidden="true" className="shrink-0" />
          <span>
            A newer scan of this artifact exists.{' '}
            <Link to={`/scans/${newer.data}`} className="text-text underline underline-offset-2">
              Open the newer scan
            </Link>
            . The findings explorer shows the current state of this ref.
          </span>
        </p>
      ) : null}
      <Card className="mb-4">
        <SeverityBar counts={scan.counts} links={links} size="lg" />
        <SeverityCounts counts={scan.counts} links={links} className="mt-4" />
      </Card>
      <Grid cols={3}>
        <StatTile
          label="Fixable"
          value={scan.fixable}
          hint="with a fixed version"
          to={findingsHref(scan, '&fixable=true')}
        />
        <StatTile
          label="Secrets"
          value={scan.secrets}
          to={`/services/${scan.service_id}/findings?ref=${encodeURIComponent(scan.ref)}&tab=secrets`}
        />
        <StatTile
          label="Suppressed"
          value={scan.suppressed}
          to={findingsHref(scan, '&suppressed=true')}
        />
      </Grid>
    </Section>
  );
}
