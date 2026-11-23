// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { Archive, ChevronDown, GitBranch, Pencil, Plus, Settings, Trash2 } from 'lucide-react';
import { useState, type ReactNode } from 'react';
import { MemoryRouter, useInRouterContext } from 'react-router';
import { HeatStrip, PipelineStrip, RiskIris } from '../viz';
import { samplePipelines, sampleServices } from '../viz/samples';
import { buttonClass, iconButtonClass, inputClass, monoInputClass } from '../ui';
import { StepIndicator } from './AuthShell';
import { Chip, GateBadge, RatingBadge, StatusPill, Tag } from './badges';
import { ConfirmDialog } from './ConfirmDialog';
import { CopyButton } from './CopyButton';
import { DataTable } from './DataTable';
import { Dialog, Sheet } from './Dialog';
import { FilterChips } from './FilterChips';
import { Freshness } from './Freshness';
import { SearchInput, Select } from './inputs';
import { Card, CardHeader, Grid, PageHeader, Section } from './layout';
import {
  Menu,
  MenuContent,
  MenuItem,
  MenuLabel,
  MenuSeparator,
  MenuTrigger,
  RowActions,
} from './Menu';
import { Mono } from './Mono';
import { Pagination } from './Pagination';
import { Popover, PopoverContent, PopoverTrigger } from './Popover';
import { Segmented } from './Segmented';
import { SeverityBadge, SeverityBar, SeverityCounts } from './Severity';
import { SeverityMark, StatusMark } from './SeverityMark';
import { SEVERITY_ORDER, type SeverityCountValues } from './severityModel';
import { StatTile } from './StatTile';
import {
  EmptyState,
  ErrorState,
  ForbiddenState,
  NotFoundState,
  PageLoader,
  SkeletonChart,
  SkeletonStat,
  SkeletonTable,
  SkeletonText,
} from './states';
import type { Column } from './tableCore';
import { TabPanel, Tabs } from './Tabs';
import { ThemeSwitch } from './ThemeSwitch';
import { toast } from './Toast';
import { Tooltip } from './Tooltip';
import { VirtualTable } from './VirtualTable';

/**
 * Development only: every token, primitive, state and chart with sample data,
 * the atoms in both themes side by side. In the development server it is at
 * /_showcase (not part of the production build). It is the visual reference
 * for building pages.
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

const PIPELINES = samplePipelines(24);
const HEAT_ROWS = sampleServices(5);

const THEMED_TOKENS = [
  ['bg', 'Canvas'],
  ['surface', 'Panels'],
  ['surface-raised', 'Raised panels, menus'],
  ['surface-overlay', 'Dialogs, drawers, palette'],
  ['sunken', 'Wells, inputs, table headers'],
  ['line', 'Hairlines'],
  ['line-strong', 'Control borders'],
  ['text', 'Primary text'],
  ['text-muted', 'Secondary text'],
  ['text-faint', 'Tertiary, axis labels'],
  ['brand', 'Links, primary button, focus'],
  ['lens-a', 'Lens violet'],
  ['lens-b', 'Lens cyan'],
] as const;

const STATUS_TOKENS = [
  'sev-critical',
  'sev-high',
  'sev-medium',
  'sev-low',
  'sev-unknown',
  'ok',
  'fail',
  'warn',
] as const;

const swatchClass: Record<string, string> = {
  bg: 'bg-bg',
  surface: 'bg-surface',
  'surface-raised': 'bg-surface-raised',
  'surface-overlay': 'bg-surface-overlay',
  sunken: 'bg-sunken',
  line: 'bg-line',
  'line-strong': 'bg-line-strong',
  text: 'bg-text',
  'text-muted': 'bg-text-muted',
  'text-faint': 'bg-text-faint',
  brand: 'bg-brand',
  'lens-a': 'bg-lens-a',
  'lens-b': 'bg-lens-b',
  'sev-critical': 'bg-sev-critical',
  'sev-high': 'bg-sev-high',
  'sev-medium': 'bg-sev-medium',
  'sev-low': 'bg-sev-low',
  'sev-unknown': 'bg-sev-unknown',
  ok: 'bg-ok',
  fail: 'bg-fail',
  warn: 'bg-warn',
};

function Panel({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="mt-8 first:mt-0">
      <h3 className="mb-3 text-body-s font-medium text-text-muted">{title}</h3>
      {children}
    </div>
  );
}

function BothThemes({ children }: { children: ReactNode }) {
  return (
    <div className="grid gap-4 xl:grid-cols-2">
      {(['dark', 'light'] as const).map((theme) => (
        <div
          key={theme}
          data-theme-scope={theme}
          className="rounded-panel border border-line p-5 sm:p-6"
        >
          <p className="mb-5 text-caption font-medium text-text-faint">
            {theme === 'dark' ? 'Dark' : 'Light'}
          </p>
          <div className="space-y-8">{children}</div>
        </div>
      ))}
    </div>
  );
}

function Tokens() {
  return (
    <>
      <Panel title="Surfaces, lines and text">
        <ul className="grid grid-cols-2 gap-3 sm:grid-cols-3">
          {THEMED_TOKENS.map(([token, use]) => (
            <li key={token} className="flex items-center gap-3">
              <span
                aria-hidden="true"
                className={`size-9 shrink-0 rounded-[8px] border border-line ${swatchClass[token]}`}
              />
              <span className="min-w-0">
                <span className="block font-mono text-caption text-text">--{token}</span>
                <span className="block truncate text-caption text-text-faint">{use}</span>
              </span>
            </li>
          ))}
        </ul>
      </Panel>
      <Panel title="Severity and status (always with a shape and a word)">
        <ul className="flex flex-wrap gap-3">
          {STATUS_TOKENS.map((token) => (
            <li key={token} className="flex items-center gap-2">
              <span aria-hidden="true" className={`size-5 rounded-full ${swatchClass[token]}`} />
              <span className="font-mono text-caption text-text-muted">--{token}</span>
            </li>
          ))}
        </ul>
        <div className="mt-4 flex flex-wrap items-center gap-4">
          {SEVERITY_ORDER.map((s) => (
            <span key={s} className="inline-flex items-center gap-1.5 text-body-s text-text-muted">
              <SeverityMark severity={s} size={14} />
              {s}
            </span>
          ))}
          {(['ok', 'fail', 'warn', 'none'] as const).map((s) => (
            <span key={s} className="inline-flex items-center gap-1.5 text-body-s text-text-muted">
              <StatusMark status={s} size={16} />
              {s}
            </span>
          ))}
        </div>
      </Panel>
      <Panel title="Lens gradient (signature elements, active navigation, brand moments only)">
        <div className="h-2 w-full rounded-full bg-[image:var(--lens)]" />
      </Panel>
    </>
  );
}

function TypeScale() {
  const rows = [
    ['text-display', '44/48 display', '1,204'],
    ['text-display-s', '32/40 display-s', '348'],
    ['text-h2', '24/32 page title', 'Portfolio'],
    ['text-h3', '20/28 section', 'Findings by severity'],
    ['text-body', '16/24 body', 'Every service with its open vulnerabilities.'],
    ['text-body-s', '14/20 body-s', 'Last scan 3 days ago on main.'],
    ['text-small', '13/18 small', 'Pills and dense tables.'],
    ['text-caption', '12/16 caption', 'Axis labels, hints.'],
  ] as const;
  return (
    <Panel title="Type: IBM Plex Sans, IBM Plex Mono for code values">
      <dl className="space-y-3">
        {rows.map(([cls, label, sample]) => (
          <div key={cls} className="grid grid-cols-[8rem_1fr] items-baseline gap-4">
            <dt className="text-caption text-text-faint">{label}</dt>
            <dd className={`${cls} min-w-0 truncate text-text tabular-nums`}>{sample}</dd>
          </div>
        ))}
        <div className="grid grid-cols-[8rem_1fr] items-baseline gap-4">
          <dt className="text-caption text-text-faint">mono</dt>
          <dd className="truncate font-mono text-body-s text-text">
            CVE-2024-3094 openssl 3.0.13-r0 9f3c2a1b
          </dd>
        </div>
      </dl>
    </Panel>
  );
}

function Shapes() {
  return (
    <Panel title="Radii and elevation">
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <div className="panel p-4 text-caption text-text-muted">panel · 16px · elev 1</div>
        <div className="raised p-4 text-caption text-text-muted">raised · 10px · elev 2</div>
        <div className="overlay rounded-panel p-4 text-caption text-text-muted">
          overlay · elev 3
        </div>
        <div className="flex items-center gap-2">
          <span className="chip tone-brand">pill</span>
          <span className="tag">tag</span>
        </div>
      </div>
    </Panel>
  );
}

function Buttons() {
  const variants = ['primary', 'secondary', 'ghost', 'danger'] as const;
  return (
    <Panel title="Buttons (32, 40, 44px; 44px on touch screens)">
      <div className="space-y-3">
        {variants.map((variant) => (
          <div key={variant} className="flex flex-wrap items-center gap-2">
            <button type="button" className={buttonClass(variant, 'sm')}>
              {variant}
            </button>
            <button type="button" className={buttonClass(variant, 'md')}>
              <Plus size={16} strokeWidth={1.75} aria-hidden="true" />
              Add a service
            </button>
            <button type="button" className={buttonClass(variant, 'lg')}>
              Large
            </button>
            <button type="button" className={buttonClass(variant, 'md')} disabled>
              Disabled
            </button>
            <button type="button" aria-label="Settings" className={iconButtonClass(variant, 'md')}>
              <Settings size={18} strokeWidth={1.75} aria-hidden="true" />
            </button>
          </div>
        ))}
      </div>
    </Panel>
  );
}

function Pills() {
  return (
    <Panel title="Badges, pills and tags">
      <div className="space-y-4">
        <div className="flex flex-wrap gap-2">
          {SEVERITY_ORDER.map((s) => (
            <SeverityBadge key={s} severity={s} count={SAMPLE[s]} />
          ))}
        </div>
        <div className="flex flex-wrap gap-2">
          {(['OK', 'WARN', 'ERROR', 'NONE', ''] as const).map((g) => (
            <GateBadge key={g} status={g} />
          ))}
          <StatusPill status="fail">Disabled</StatusPill>
          <Chip tone="brand">Default branch</Chip>
          <Tag>GitLab</Tag>
          <Tag icon={<GitBranch size={12} strokeWidth={1.75} aria-hidden="true" />}>
            <span className="font-mono">main</span>
          </Tag>
        </div>
        <div className="flex flex-wrap items-center gap-4">
          {(['A', 'B', 'C', 'D', 'E', null] as const).map((r) => (
            <RatingBadge key={String(r)} rating={r} label="Security" />
          ))}
        </div>
        <SeverityCounts counts={SAMPLE} />
        <SeverityBar counts={SAMPLE} size="md" showSummary />
      </div>
    </Panel>
  );
}

function Stats() {
  return (
    <Panel title="Stat tiles (trend slot for a sparkline, delta)">
      <Grid cols={2}>
        <StatTile label="Services" value={9} to="/" hint="2 archived" />
        <StatTile
          label="Critical"
          value={2}
          tone="critical"
          delta={{ value: -1, label: 'since last scan', better: 'down' }}
          to="/findings?severity=critical"
          trend={<DemoSpark />}
        />
        <StatTile label="Fixable" value={12345} delta={{ value: 3, better: 'down' }} />
        <StatTile label="Coverage" value="84.2%" tone="ok" delta={{ value: 0 }} hint="main" />
      </Grid>
    </Panel>
  );
}

/** A stand-in for the sparkline the chart kit provides. */
function DemoSpark() {
  return (
    <svg
      viewBox="0 0 80 24"
      width="80"
      height="24"
      aria-hidden="true"
      className="text-sev-critical"
    >
      <polyline
        points="0,18 12,14 24,16 36,8 48,10 60,4 72,9 80,6"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.75"
        strokeLinejoin="round"
        strokeLinecap="round"
      />
    </svg>
  );
}

function Atoms() {
  return (
    <>
      <Tokens />
      <TypeScale />
      <Shapes />
      <Buttons />
      <Pills />
      <Stats />
      <Panel title="Freshness and code values">
        <div className="flex flex-wrap gap-4">
          <Freshness
            at="2026-11-19T11:57:00Z"
            now={NOW}
            link={{ label: 'Pipeline #4213', href: 'https://gitlab.example.com/p/1' }}
          />
          <Freshness at="2026-11-15T11:57:00Z" now={NOW} staleAfterMs={24 * 3600_000} />
        </div>
        <p className="mt-3 text-body-s">
          Commit <Mono value={SHA} kind="sha" copy copyLabel="Copy commit" /> in{' '}
          <Mono value="openssl 3.0.13-r0" />
        </p>
      </Panel>
    </>
  );
}

function Overlays() {
  const [dialog, setDialog] = useState(false);
  const [confirm, setConfirm] = useState<null | 'default' | 'danger'>(null);
  const [sheet, setSheet] = useState(false);
  const [role, setRole] = useState('viewer');
  return (
    <Card className="space-y-6">
      <div className="flex flex-wrap items-center gap-2">
        <button type="button" className={buttonClass('secondary')} onClick={() => setDialog(true)}>
          Open a dialog
        </button>
        <button
          type="button"
          className={buttonClass('secondary')}
          onClick={() => setConfirm('default')}
        >
          Confirm
        </button>
        <button
          type="button"
          className={buttonClass('danger')}
          onClick={() => setConfirm('danger')}
        >
          Delete user
        </button>
        <button type="button" className={buttonClass('secondary')} onClick={() => setSheet(true)}>
          Open a sheet
        </button>
        <Menu>
          <MenuTrigger className={buttonClass('secondary')}>
            Menu
            <ChevronDown size={16} strokeWidth={1.75} aria-hidden="true" />
          </MenuTrigger>
          <MenuContent align="start">
            <MenuLabel>Service</MenuLabel>
            <MenuItem icon={<Pencil size={16} strokeWidth={1.75} aria-hidden="true" />}>
              Edit
            </MenuItem>
            <MenuItem
              icon={<Archive size={16} strokeWidth={1.75} aria-hidden="true" />}
              shortcut="A"
            >
              Archive
            </MenuItem>
            <MenuSeparator />
            <MenuItem
              tone="danger"
              icon={<Trash2 size={16} strokeWidth={1.75} aria-hidden="true" />}
            >
              Delete
            </MenuItem>
          </MenuContent>
        </Menu>
        <RowActions label="Actions for payments-api">
          <MenuItem>Open findings</MenuItem>
          <MenuItem>Copy project path</MenuItem>
        </RowActions>
        <Popover>
          <PopoverTrigger className={buttonClass('secondary')}>Popover</PopoverTrigger>
          <PopoverContent aria-label="Risk points">
            <p className="font-medium">How risk is counted</p>
            <p className="mt-1 text-text-muted">
              critical × 10 + high × 5 + medium × 3 + low × 1 + unknown × 5
            </p>
          </PopoverContent>
        </Popover>
        <Tooltip content="Copies the project path">
          <button type="button" className={buttonClass('ghost')}>
            Tooltip on hover or focus
          </button>
        </Tooltip>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <button
          type="button"
          className={buttonClass('secondary', 'sm')}
          onClick={() =>
            toast('User disabled', { description: 'alice was signed out everywhere.' })
          }
        >
          Success toast
        </button>
        <button
          type="button"
          className={buttonClass('secondary', 'sm')}
          onClick={() => toast.error('Saving failed', { description: 'Check your connection.' })}
        >
          Error toast
        </button>
        <button
          type="button"
          className={buttonClass('secondary', 'sm')}
          onClick={() => toast.info('A new scan arrived')}
        >
          Info toast
        </button>
        <Select
          label="Role for alice"
          hideLabel
          size="sm"
          className="w-32"
          value={role}
          onChange={setRole}
          options={[
            { value: 'admin', label: 'Admin' },
            { value: 'viewer', label: 'Viewer' },
          ]}
        />
      </div>

      <Dialog
        open={dialog}
        onOpenChange={setDialog}
        title="Edit payments-api"
        description="Change the name or the SonarQube project."
        footer={
          <>
            <button
              type="button"
              className={buttonClass('secondary')}
              onClick={() => setDialog(false)}
            >
              Cancel
            </button>
            <button
              type="button"
              className={buttonClass('primary')}
              onClick={() => setDialog(false)}
            >
              Save
            </button>
          </>
        }
      >
        <label className="flex flex-col gap-1.5">
          <span className="text-body-s font-medium text-text">Name</span>
          <input className={inputClass} defaultValue="payments-api" />
        </label>
      </Dialog>
      {confirm ? (
        <ConfirmDialog
          title={confirm === 'danger' ? 'Delete alice?' : 'Archive billing-worker?'}
          confirmLabel={confirm === 'danger' ? 'Delete user' : 'Archive'}
          tone={confirm}
          onConfirm={() => setConfirm(null)}
          onCancel={() => setConfirm(null)}
        >
          <p>
            {confirm === 'danger'
              ? 'This removes alice with their sessions and two-factor setup. The audit log keeps its entries.'
              : 'Archived services are hidden from the portfolio and receive no new uploads.'}
          </p>
        </ConfirmDialog>
      ) : null}
      <Sheet
        open={sheet}
        onOpenChange={setSheet}
        title={<Mono value="CVE-2024-3094" />}
        description="xz-utils backdoor"
        footer={
          <button
            type="button"
            className={buttonClass('secondary')}
            onClick={() => setSheet(false)}
          >
            Done
          </button>
        }
      >
        <SkeletonText lines={6} />
      </Sheet>
    </Card>
  );
}

function Body() {
  const [tab, setTab] = useState('overview');
  const [view, setView] = useState<'table' | 'treemap'>('table');
  const [chips, setChips] = useState<string[]>(['high']);
  const [branch, setBranch] = useState('main');

  return (
    <div className="mx-auto max-w-[1344px] px-4 py-8 sm:px-6 lg:px-8">
      <PageHeader
        breadcrumb={[{ label: 'Development', to: '/' }, { label: 'Design system' }]}
        title="Optic design system"
        subtitle="Every token, primitive and state with sample data, in both themes. Development only."
        freshness={{ at: '2026-11-19T11:57:00Z', now: NOW }}
        actions={
          <>
            <ThemeSwitch />
            <button type="button" className={buttonClass('primary')}>
              Primary action
            </button>
          </>
        }
      />

      <Section title="Atoms in both themes">
        <BothThemes>
          <Atoms />
        </BothThemes>
      </Section>

      <Section
        title="Overlays and feedback"
        description="Rendered in a portal at document level; they follow the page theme."
      >
        <Overlays />
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
              className="w-48"
              options={[
                { value: 'main', label: 'main', hint: 'default' },
                { value: 'dev', label: 'develop' },
                { value: 'release', label: 'release/2.x' },
              ]}
            />
            <SearchInput
              label="Search findings"
              placeholder="Search by CVE or package"
              onChange={() => {}}
              className="w-72"
            />
            <label className="flex w-56 flex-col gap-1.5">
              <span className="text-body-s font-medium text-text-muted">Project key</span>
              <input className={monoInputClass} defaultValue="platform_payments-api" />
            </label>
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
          <StepIndicator
            steps={{ labels: ['Scan the code', 'Enter a code', 'Save recovery codes'], current: 1 }}
            className="max-w-md"
          />
          <Pagination shown={50} total={1204} hasMore onLoadMore={() => {}} noun="finding" />
        </Card>
      </Section>

      <Section title="States">
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          <Card padded={false}>
            <EmptyState
              compact
              art="services"
              title="No services yet"
              body="Services appear here after the first pipeline uploads a scan."
              action={
                <button type="button" className={buttonClass('primary')}>
                  Add a service
                </button>
              }
            />
          </Card>
          <Card padded={false}>
            <EmptyState
              compact
              art="search"
              title="No services match"
              body="Change the search or the filter to see more services."
              action={
                <button type="button" className={buttonClass('secondary')}>
                  Clear search and filter
                </button>
              }
            />
          </Card>
          <Card padded={false}>
            <EmptyState
              compact
              art="clean"
              title="No vulnerabilities"
              body="Trivy found nothing in the latest scan of main."
            />
          </Card>
          <Card padded={false}>
            <EmptyState
              compact
              art="timeline"
              title="No scans of release/2.x yet"
              body="Results appear after a pipeline on this branch uploads a scan."
            />
          </Card>
          <Card padded={false}>
            <EmptyState
              compact
              art="lens"
              title="Nothing to show"
              body="The default illustration, for anything without a better fit."
            />
          </Card>
          <Card padded={false}>
            <ErrorState
              compact
              message="The server did not answer in time."
              detail="request 7f3a"
              onRetry={() => {}}
            />
          </Card>
          <Card padded={false} className="md:col-span-2 xl:col-span-1">
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
          <Card>
            <div className="grid gap-4 sm:grid-cols-2">
              <SkeletonStat />
              <SkeletonStat />
            </div>
            <SkeletonText className="mt-4" />
            <PageLoader label="Loading the portfolio" />
          </Card>
          <Card padded={false}>
            <NotFoundState />
          </Card>
          <Card padded={false}>
            <ForbiddenState />
          </Card>
        </div>
      </Section>

      <Section title="Charts">
        <p className="mb-4 text-body-s text-text-muted">
          The visual kit, with every variant, is at /gallery.html.
        </p>
        <Grid cols={2}>
          <Card>
            <CardHeader title="Risk iris" />
            <div className="flex items-center gap-6">
              <RiskIris counts={{ critical: 2, high: 14, medium: 30, low: 9 }} gate="fail" />
              <RiskIris counts={{ low: 3 }} gate="pass" size={40} />
            </div>
          </Card>
          <Card>
            <CardHeader title="Loading" />
            <SkeletonChart className="h-24" />
          </Card>
          <Card className="sm:col-span-2">
            <CardHeader title="Pipeline strip" />
            <PipelineStrip points={PIPELINES} gateCoverageThreshold={80} animate={false} />
          </Card>
          <Card className="sm:col-span-2" padded={false}>
            <HeatStrip rows={HEAT_ROWS} caption="Sample services" />
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
