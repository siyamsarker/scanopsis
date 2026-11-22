// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useState } from 'react';
import type { ScanDiff, Vulnerability } from '../../api/scan';
import { Link } from 'react-router';
import { countOf, safeExternalUrl } from '../../format';
import { Mono } from '../../components/Mono';
import { Card, Grid, Section } from '../../components/layout';
import { SeverityBadge } from '../../components/Severity';
import { SEVERITY_ORDER, severityKey } from '../../components/severityModel';
import { StatTile } from '../../components/StatTile';
import { EmptyState } from '../../components/states';
import type { Column } from '../../components/tableCore';
import { DataTable } from '../../components/DataTable';
import { TabPanel, Tabs } from '../../components/Tabs';
import { VirtualTable } from '../../components/VirtualTable';

const columns: Column<Vulnerability>[] = [
  {
    key: 'severity',
    header: 'Severity',
    sortKey: 'severity',
    sortValue: (v) => SEVERITY_ORDER.indexOf(severityKey(v.severity)),
    cell: (v) => <SeverityBadge severity={severityKey(v.severity)} />,
    width: 'w-32 shrink-0',
  },
  {
    key: 'vuln',
    header: 'Vulnerability',
    sortKey: 'vuln',
    sortValue: (v) => v.vuln_id,
    width: 'w-44 shrink-0',
    cell: (v) => {
      const href = safeExternalUrl(v.primary_url);
      return href ? (
        <a
          href={href}
          target="_blank"
          rel="noopener noreferrer"
          className="underline decoration-border-strong underline-offset-2 hover:decoration-text"
        >
          <Mono value={v.vuln_id} />
        </a>
      ) : (
        <Mono value={v.vuln_id} />
      );
    },
  },
  {
    key: 'pkg',
    header: 'Package',
    sortKey: 'pkg',
    sortValue: (v) => v.pkg_name,
    cell: (v) => <Mono value={v.pkg_name} />,
  },
  {
    key: 'versions',
    header: 'Installed → fixed',
    sortKey: 'versions',
    sortValue: (v) => v.installed_version,
    cell: (v) => (
      <span className="whitespace-nowrap">
        <Mono value={v.installed_version} /> →{' '}
        {v.fixed_version ? (
          <Mono value={v.fixed_version} />
        ) : (
          <span className="text-text-muted">no fix</span>
        )}
      </span>
    ),
  },
  {
    key: 'target',
    header: 'Target',
    sortKey: 'target',
    sortValue: (v) => v.target,
    cell: (v) => <Mono value={v.target} truncate />,
  },
];

/** The table of one list; above 500 rows only the visible rows are rendered. */
export function VulnTable({
  rows,
  caption,
  empty,
}: {
  rows: Vulnerability[];
  caption: string;
  empty: string;
}) {
  const emptyState = <EmptyState compact title={empty} headingLevel={3} />;
  const common = {
    columns,
    rows,
    rowKey: (v: Vulnerability) => v.id,
    caption,
    defaultSort: { key: 'severity', direction: 'asc' as const },
    empty: emptyState,
  };
  return rows.length > 500 ? (
    <VirtualTable {...common} heightClass="h-[32rem]" minWidthClass="min-w-[48rem]" />
  ) : (
    <DataTable {...common} scrollClass="max-h-[32rem]" />
  );
}

function TruncatedNotice({ diff }: { diff: ScanDiff }) {
  if (!diff.truncated) return null;
  return (
    <p role="status" className="mb-4 text-sm text-text-muted">
      Showing the 500 most severe findings of each list. The counts include all findings.
    </p>
  );
}

export function Changes({ diff }: { diff: ScanDiff }) {
  const [tab, setTab] = useState('new');

  if (diff.previous_scan_id === null) {
    return (
      <Section title="Changes since the previous scan">
        <p className="mb-4 text-text">
          First scan of this artifact on this ref: all {diff.new_total.toLocaleString('en-US')}{' '}
          findings are new.
        </p>
        <TruncatedNotice diff={diff} />
        <VulnTable
          rows={diff.new}
          caption="New findings"
          empty="Nothing new since the previous scan"
        />
      </Section>
    );
  }

  return (
    <Section
      title="Changes since the previous scan"
      description={
        <>
          Compared with{' '}
          <Link
            to={`/scans/${diff.previous_scan_id}`}
            className="text-text underline underline-offset-2"
          >
            the previous scan
          </Link>
          .
        </>
      }
    >
      <Grid cols={3} className="mb-6">
        <StatTile
          label="New"
          value={diff.new_total}
          tone="high"
          to={`/scans/${diff.previous_scan_id}`}
          hint="not in the previous scan"
        />
        <StatTile
          label="Fixed"
          value={diff.fixed_total}
          tone="pass"
          to={`/scans/${diff.previous_scan_id}`}
          hint="no longer reported"
        />
        <StatTile
          label="Unchanged"
          value={diff.unchanged}
          to={`/scans/${diff.previous_scan_id}`}
          hint={countOf(diff.unchanged, 'finding') + ' in both'}
        />
      </Grid>
      <TruncatedNotice diff={diff} />
      <Card padded={false} className="px-6 pb-6">
        <Tabs
          idBase="scan-diff"
          label="Changes"
          value={tab}
          onChange={setTab}
          tabs={[
            { id: 'new', label: 'New', count: diff.new_total },
            { id: 'fixed', label: 'Fixed', count: diff.fixed_total },
          ]}
        />
        <TabPanel idBase="scan-diff" tab="new" value={tab}>
          <VulnTable
            rows={diff.new}
            caption="New findings"
            empty="Nothing new since the previous scan"
          />
        </TabPanel>
        <TabPanel idBase="scan-diff" tab="fixed" value={tab}>
          <VulnTable
            rows={diff.fixed}
            caption="Fixed findings"
            empty="Nothing was fixed since the previous scan"
          />
        </TabPanel>
      </Card>
    </Section>
  );
}
