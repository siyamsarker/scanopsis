// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useMemo, useState, type ReactNode } from 'react';
import { MemoryRouter, useInRouterContext } from 'react-router';
import { Chart } from '../charts/Chart';
import { lineSeries, stackedSeverityBars, treemap } from '../charts/options';
import { primaryButtonClass } from '../ui';
import { GateBadge, RatingBadge } from './badges';
import { CopyButton } from './CopyButton';
import { DataTable } from './DataTable';
import { FilterChips } from './FilterChips';
import { Freshness } from './Freshness';
import { SearchInput, Select } from './inputs';
import { Card, CardHeader, Grid, PageHeader, Section } from './layout';
import { Mono } from './Mono';
import { Pagination } from './Pagination';
import { Segmented } from './Segmented';
import { SeverityBadge, SeverityBar, SeverityCounts } from './Severity';
import { SEVERITY_ORDER, worstSeverity, type SeverityCountValues } from './severityModel';
import { StatTile } from './StatTile';
import {
  EmptyState,
  ErrorState,
  Skeleton,
  SkeletonChart,
  SkeletonStat,
  SkeletonTable,
  SkeletonText,
} from './states';
import type { Column } from './tableCore';
import { TabPanel, Tabs } from './Tabs';
import { VirtualTable } from './VirtualTable';

/**
 * Development only: every component and chart with sample data, and the atoms in
 * both themes side by side. It is not routed. Mount it behind a dev flag, for
 * example `import.meta.env.DEV`, to look at the design system.
 */

const SAMPLE: SeverityCountValues = { critical: 2, high: 44, medium: 130, low: 61, unknown: 3 };
const NOW = new Date('2026-11-19T12:00:00Z');
const SHA = '9f3c2a1b7d8e4f6a0b1c2d3e4f5a6b7c8d9e0f1a';

interface Service {
  id: number;
  name: string;
  counts: SeverityCountValues;
  commit: string;
  scanned: string;
}

const SERVICES: Service[] = [
  { id: 1, name: 'payments-api', counts: SAMPLE, commit: SHA, scanned: '2026-11-19T11:57:00Z' },
  {
    id: 2,
    name: 'auth',
    counts: { critical: 0, high: 3, medium: 12, low: 9, unknown: 0 },
    commit: '1a2b3c4d5e6f',
    scanned: '2026-11-18T09:00:00Z',
  },
  {
    id: 3,
    name: 'web',
    counts: { critical: 0, high: 0, medium: 0, low: 4, unknown: 0 },
    commit: 'fedcba987654',
    scanned: '2026-11-10T09:00:00Z',
  },
];

const total = (c: SeverityCountValues) => SEVERITY_ORDER.reduce((s, k) => s + c[k], 0);

const SERVICE_COLUMNS: Column<Service>[] = [
  {
    key: 'name',
    header: 'Service',
    sortKey: 'name',
    sortValue: (r) => r.name,
    cell: (r) => <span className="font-medium text-text">{r.name}</span>,
  },
  {
    key: 'sev',
    header: 'Severity',
    width: 'w-64 shrink-0',
    cell: (r) => <SeverityBar counts={r.counts} size="sm" />,
  },
  {
    key: 'total',
    header: 'Findings',
    numeric: true,
    width: 'w-28 shrink-0',
    sortKey: 'total',
    sortValue: (r) => total(r.counts),
    cell: (r) => total(r.counts),
  },
  {
    key: 'commit',
    header: 'Commit',
    width: 'w-40 shrink-0',
    cell: (r) => <Mono value={r.commit} kind="sha" copy copyLabel="Copy commit" />,
  },
  {
    key: 'scanned',
    header: 'Scanned',
    width: 'w-44 shrink-0',
    sortKey: 'scanned',
    sortValue: (r) => r.scanned,
    cell: (r) => <Freshness at={r.scanned} now={NOW} prefix="" />,
  },
];

interface Finding {
  id: number;
  cve: string;
  pkg: string;
  version: string;
  severity: 'critical' | 'high' | 'medium' | 'low';
}

const FINDINGS: Finding[] = Array.from({ length: 10_000 }, (_, i) => ({
  id: i,
  cve: `CVE-2026-${String(1000 + i)}`,
  pkg: ['openssl', 'zlib', 'glibc', 'curl', 'busybox'][i % 5],
  version: `1.${i % 9}.${i % 31}`,
  severity: (['critical', 'high', 'medium', 'low'] as const)[i % 4],
}));

const FINDING_COLUMNS: Column<Finding>[] = [
  {
    key: 'cve',
    header: 'Vulnerability',
    className: 'font-mono',
    cell: (r) => r.cve,
    sortKey: 'cve',
    sortValue: (r) => r.cve,
  },
  {
    key: 'sev',
    header: 'Severity',
    width: 'w-36 shrink-0',
    cell: (r) => <SeverityBadge severity={r.severity} />,
  },
  { key: 'pkg', header: 'Package', cell: (r) => <Mono value={r.pkg} /> },
  {
    key: 'ver',
    header: 'Installed',
    width: 'w-32 shrink-0',
    cell: (r) => <Mono value={r.version} />,
  },
];

const POINTS = Array.from({ length: 14 }, (_, i) => ({
  label: `#${4200 + i}`,
  detail: `commit ${(0x9f3c2a + i * 977).toString(16)}`,
  counts: {
    critical: i % 5 === 0 ? 2 : 1,
    high: 44 - i,
    medium: 130 - i * 3,
    low: 60 + (i % 4),
    unknown: i % 3,
  },
}));

const TREE = SERVICES.map((s) => ({
  id: String(s.id),
  name: s.name,
  value: total(s.counts),
  severity: worstSeverity(s.counts),
  detail: `platform/${s.name}`,
}));

function Panel({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="mt-8">
      <h3 className="mb-3 font-mono text-sm text-text-muted">{title}</h3>
      {children}
    </div>
  );
}

function BothThemes({ children }: { children: ReactNode }) {
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      {(['dark', 'light'] as const).map((theme) => (
        <div key={theme} data-theme-scope={theme} className="rounded-card border border-border p-6">
          <p className="mb-4 font-mono text-sm text-text-muted">{theme}</p>
          <div className="space-y-6">{children}</div>
        </div>
      ))}
    </div>
  );
}

function Atoms() {
  return (
    <>
      <Grid cols={4}>
        <StatTile label="Findings" value={240} hint="across 4 services" to="/findings" />
        <StatTile
          label="Critical"
          value={2}
          tone="critical"
          delta={{ value: -1, label: 'since last scan', better: 'down' }}
          to="/findings?severity=critical"
        />
        <StatTile label="Fixable" value={12345} delta={{ value: 3, better: 'down' }} />
        <StatTile label="Coverage" value="84.2%" tone="pass" hint="main branch" />
      </Grid>
      <SeverityBar counts={SAMPLE} size="lg" showSummary />
      <SeverityBar counts={SAMPLE} links={{ critical: '/c', high: '/h' }} />
      <SeverityCounts counts={SAMPLE} />
      <div className="flex flex-wrap gap-2">
        {SEVERITY_ORDER.map((s) => (
          <SeverityBadge key={s} severity={s} count={SAMPLE[s]} />
        ))}
      </div>
      <div className="flex flex-wrap items-center gap-4">
        {(['A', 'B', 'C', 'D', 'E', null] as const).map((r) => (
          <RatingBadge key={String(r)} rating={r} label="Security" />
        ))}
      </div>
      <div className="flex flex-wrap gap-2">
        {(['OK', 'WARN', 'ERROR', 'NONE', ''] as const).map((g) => (
          <GateBadge key={g} status={g} />
        ))}
      </div>
      <div className="flex flex-wrap gap-4">
        <Freshness
          at="2026-11-19T11:57:00Z"
          now={NOW}
          link={{ label: 'Pipeline #4213', href: 'https://gitlab.example.com/p/1' }}
        />
        <Freshness at="2026-11-15T11:57:00Z" now={NOW} staleAfterMs={24 * 3600_000} />
      </div>
      <p>
        Commit <Mono value={SHA} kind="sha" copy copyLabel="Copy commit" /> in{' '}
        <Mono value="openssl 3.0.13-r0" />
      </p>
    </>
  );
}

function Body() {
  const [tab, setTab] = useState('overview');
  const [view, setView] = useState<'table' | 'treemap'>('table');
  const [chips, setChips] = useState<string[]>(['high']);
  const [branch, setBranch] = useState('main');
  const stacked = useMemo(() => stackedSeverityBars(POINTS), []);
  const coverage = useMemo(
    () =>
      lineSeries({
        categories: POINTS.map((p) => p.label),
        series: [{ name: 'Coverage', values: POINTS.map((_, i) => 70 + i * 1.2) }],
        percent: true,
      }),
    [],
  );
  const tree = useMemo(() => treemap(TREE), []);

  return (
    <div className="mx-auto max-w-7xl p-6 sm:p-8">
      <PageHeader
        title="Design system"
        subtitle="Every component with sample data. Development only."
        freshness={{ at: '2026-11-19T11:57:00Z', now: NOW }}
        actions={
          <button type="button" className={primaryButtonClass}>
            Primary action
          </button>
        }
      />

      <Section title="Atoms in both themes">
        <BothThemes>
          <Atoms />
        </BothThemes>
      </Section>

      <Section title="States">
        <Grid cols={2}>
          <Card padded={false}>
            <EmptyState
              title="No services yet"
              body="Services appear here after the first pipeline uploads a scan."
              action={
                <button type="button" className={primaryButtonClass}>
                  Add a service
                </button>
              }
            />
          </Card>
          <Card padded={false}>
            <EmptyState
              variant="connect"
              title="Connect your first pipeline"
              steps={[
                { title: 'Create an upload token', body: 'In settings, under tokens.' },
                { title: 'Add the scan job', body: 'Copy the snippet into .gitlab-ci.yml.' },
                { title: 'Run a pipeline', body: 'Results appear within a minute.' },
              ]}
            />
          </Card>
          <Card padded={false}>
            <ErrorState
              message="The server did not answer in time."
              detail="request 7f3a"
              onRetry={() => {}}
            />
          </Card>
          <Card>
            <div className="grid gap-4 sm:grid-cols-2">
              <SkeletonStat />
              <SkeletonStat />
            </div>
            <SkeletonText className="mt-4" />
            <Skeleton className="mt-4 h-24 w-full" />
          </Card>
        </Grid>
      </Section>

      <Section title="Controls">
        <Card className="space-y-6">
          <Tabs
            idBase="demo"
            label="Demo sections"
            value={tab}
            onChange={setTab}
            tabs={[
              { id: 'overview', label: 'Overview' },
              { id: 'findings', label: 'Findings', count: 1204 },
              { id: 'scans', label: 'Scans' },
            ]}
          />
          <TabPanel idBase="demo" tab="overview" value={tab}>
            Overview content.
          </TabPanel>
          <TabPanel idBase="demo" tab="findings" value={tab}>
            Findings content.
          </TabPanel>
          <TabPanel idBase="demo" tab="scans" value={tab}>
            Scans content.
          </TabPanel>
          <div className="flex flex-wrap items-end gap-4">
            <Segmented
              label="View"
              value={view}
              onChange={setView}
              options={[
                { value: 'table', label: 'Table' },
                { value: 'treemap', label: 'Treemap' },
              ]}
            />
            <Select
              label="Branch"
              value={branch}
              onChange={setBranch}
              options={[
                { value: 'main', label: 'main' },
                { value: 'dev', label: 'develop' },
              ]}
            />
            <SearchInput
              label="Search findings"
              placeholder="Search by CVE or package"
              onChange={() => {}}
            />
            <CopyButton text="npm ci" label="Copy command" />
          </div>
          <FilterChips
            label="Filter by severity"
            selected={chips}
            onChange={setChips}
            options={[
              { value: 'critical', label: 'Critical', count: 2, severity: 'critical' },
              { value: 'high', label: 'High', count: 44, severity: 'high' },
              { value: 'fixable', label: 'Fixable', count: 12345 },
            ]}
          />
          <Pagination shown={50} total={1204} hasMore onLoadMore={() => {}} noun="finding" />
        </Card>
      </Section>

      <Section title="Charts">
        <Grid cols={2}>
          <Card padded={false}>
            <CardHeader padded title="How did findings change scan by scan?" />
            <div className="px-4 pb-4">
              <Chart option={stacked} ariaLabel="Findings per scan by severity, last 14 scans" />
            </div>
          </Card>
          <Card padded={false}>
            <CardHeader padded title="How is test coverage moving?" />
            <div className="px-4 pb-4">
              <Chart option={coverage} ariaLabel="Coverage percentage per scan, last 14 scans" />
            </div>
          </Card>
          <Card padded={false}>
            <CardHeader padded title="Which services carry the most findings?" />
            <div className="px-4 pb-4">
              <Chart option={tree} ariaLabel="Findings per service, coloured by worst severity" />
            </div>
          </Card>
          <Card padded={false}>
            <CardHeader padded title="Loading, empty and error states" />
            <div className="grid gap-2 px-4 pb-4">
              <SkeletonChart className="h-24" />
              <Chart
                option={tree}
                ariaLabel="empty"
                status="empty"
                heightClass="h-32"
                emptyTitle="No scans yet"
              />
            </div>
          </Card>
        </Grid>
      </Section>

      <Section title="Tables">
        <Panel title="DataTable with density toggle">
          <DataTable
            caption="Services"
            columns={SERVICE_COLUMNS}
            rows={SERVICES}
            rowKey={(r) => r.id}
            defaultSort={{ key: 'total', direction: 'desc' }}
            densityToggle
            toolbar={
              <SearchInput
                label="Filter services"
                placeholder="Filter services"
                onChange={() => {}}
              />
            }
          />
        </Panel>
        <Panel title="VirtualTable, 10 000 rows">
          <VirtualTable
            caption="Findings"
            columns={FINDING_COLUMNS}
            rows={FINDINGS}
            rowKey={(r) => r.id}
            densityToggle
            heightClass="h-96"
          />
        </Panel>
        <Panel title="Loading table">
          <Card padded={false}>
            <SkeletonTable rows={4} columns={4} />
          </Card>
        </Panel>
      </Section>
    </div>
  );
}

/** The whole showcase. Brings its own router when the app has none around it. */
export default function Showcase() {
  const inRouter = useInRouterContext();
  return inRouter ? (
    <Body />
  ) : (
    <MemoryRouter>
      <Body />
    </MemoryRouter>
  );
}
