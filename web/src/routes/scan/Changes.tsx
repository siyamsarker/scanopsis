// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useState } from 'react';
import { Link } from 'react-router';
import type { Scan, ScanDiff, Vulnerability } from '../../api/scan';
import { SeverityMark } from '../../components/SeverityMark';
import { SEVERITY_LABEL, severityKey } from '../../components/severityModel';
import { EmptyState } from '../../components/states';
import { TabPanel, Tabs } from '../../components/Tabs';
import { countOf, formatExactTime, formatInt, relativeTime } from '../../format';
import { linkClass } from '../../ui';
import { Delta, riskPoints } from '../../viz';
import { VersionLine } from '../findings/FindingsList';
import { VulnDrawer } from '../findings/VulnDrawer';
import { DiffChart } from './DiffChart';
import { ScanPicker, describeScan } from './ScanPicker';

/** Shown rows above which the list stops and says so. */
const LIST_LIMIT = 500;

/** One list of the comparison: a row per finding that opens its details. */
export function VulnList({
  rows,
  label,
  empty,
  onOpen,
}: {
  rows: Vulnerability[];
  label: string;
  empty: string;
  onOpen: (v: Vulnerability) => void;
}) {
  if (rows.length === 0) return <EmptyState compact art="clean" title={empty} headingLevel={3} />;
  return (
    <ul aria-label={label} className="divide-y divide-line/70">
      {rows.map((v) => {
        const sev = severityKey(v.severity);
        return (
          <li key={v.id}>
            <button
              type="button"
              onClick={() => onOpen(v)}
              aria-haspopup="dialog"
              className="grid w-full grid-cols-[1.25rem_minmax(0,1fr)] items-center gap-x-3 px-3 py-3 text-left transition-colors duration-micro hover:bg-wash sm:grid-cols-[6rem_minmax(0,1fr)_6rem] sm:px-4"
            >
              <span className="flex items-center gap-2">
                <SeverityMark severity={sev} size={13} />
                <span className={`tone-${sev} tone-ink text-small font-medium max-sm:sr-only`}>
                  {SEVERITY_LABEL[sev]}
                </span>
              </span>
              <span className="min-w-0">
                <span className="flex min-w-0 items-baseline gap-2">
                  <span className="shrink-0 font-mono text-body-s font-medium text-text">
                    {v.vuln_id}
                  </span>
                  <span className="min-w-0 truncate text-body-s text-text-muted">
                    {v.title || 'No title reported'}
                  </span>
                </span>
                <VersionLine vuln={v} className="mt-1" />
              </span>
              <span className="hidden text-right text-small text-text-muted sm:block">
                <span className="block text-caption text-text-faint">First seen</span>
                <time
                  dateTime={v.first_seen_at}
                  title={`First seen ${formatExactTime(v.first_seen_at)}`}
                >
                  {relativeTime(v.first_seen_at)}
                </time>
              </span>
            </button>
          </li>
        );
      })}
    </ul>
  );
}

type TabId = 'new' | 'fixed' | 'unchanged';

/** The first list with something in it, so the view never opens on an empty tab. */
export function firstTab(diff: ScanDiff): TabId {
  if (diff.new_total > 0) return 'new';
  if (diff.fixed_total > 0) return 'fixed';
  if (diff.unchanged > 0) return 'unchanged';
  return 'new';
}

export function Changes({
  scan,
  diff,
  onBaseChange,
  switching = false,
}: {
  scan: Scan;
  diff: ScanDiff;
  onBaseChange: (id: number | null) => void;
  /** Another base is loading; the current comparison stays until it arrives. */
  switching?: boolean;
}) {
  const [picked, setPicked] = useState<{ base: number | null; tab: TabId } | null>(null);
  const [open, setOpen] = useState<Vulnerability | null>(null);
  const tab = picked && picked.base === diff.previous_scan_id ? picked.tab : firstTab(diff);
  const base = diff.base;
  const before = riskPoints(base?.counts);
  const after = riskPoints(scan.counts);

  const tabs = [
    { id: 'new', label: 'New', count: diff.new_total },
    { id: 'fixed', label: 'Fixed', count: diff.fixed_total },
    { id: 'unchanged', label: 'Unchanged', count: diff.unchanged },
  ];
  const lists: Record<
    TabId,
    { rows: Vulnerability[]; total: number; empty: string; label: string }
  > = {
    new: {
      rows: diff.new,
      total: diff.new_total,
      empty: 'Nothing new in this scan',
      label: 'New findings',
    },
    fixed: {
      rows: diff.fixed,
      total: diff.fixed_total,
      empty: 'Nothing was fixed',
      label: 'Fixed findings',
    },
    unchanged: {
      rows: diff.unchanged_items,
      total: diff.unchanged,
      empty: 'No finding is in both scans',
      label: 'Unchanged findings',
    },
  };

  return (
    <section aria-labelledby="changes-title" aria-busy={switching || undefined}>
      <div className="flex flex-wrap items-end justify-between gap-x-6 gap-y-3 pb-4">
        <div className="min-w-0">
          <h2 id="changes-title" className="text-h3 font-semibold tracking-tight text-text">
            What changed
          </h2>
          <p className="mt-0.5 text-body-s text-text-muted">
            {base ? (
              <>
                Since{' '}
                <Link to={`/scans/${base.id}`} className={linkClass}>
                  {describeScan(base)}
                </Link>
                .
              </>
            ) : (
              <>First scan of this artifact on this ref: every finding is new.</>
            )}
          </p>
        </div>
        {scan.kind === 'image' ? (
          <ScanPicker scan={scan} base={base} onChange={onBaseChange} />
        ) : null}
      </div>

      <div
        className={`panel grid gap-x-10 gap-y-6 px-5 py-5 transition-opacity duration-enter sm:px-6 lg:grid-cols-[minmax(0,1fr)_16rem] ${switching ? 'opacity-60' : ''}`}
      >
        <DiffChart added={diff.new_counts} fixed={diff.fixed_counts} />
        <dl className="grid content-start gap-4 border-line lg:border-l lg:pl-8">
          <div>
            <dt className="text-small text-text-muted">Net risk change</dt>
            <dd className="mt-1">
              <Delta
                value={after - before}
                unit="risk points"
                since="since the compared scan"
                className="text-display-s tracking-tight"
              />
            </dd>
            <dd className="mt-1 text-small text-text-muted tabular-nums">
              {formatInt(before)} → {formatInt(after)} points
            </dd>
          </div>
          <div className="grid grid-cols-3 gap-2 text-small">
            <div>
              <dt className="text-text-muted">New</dt>
              <dd className="text-body font-semibold text-text tabular-nums">
                {formatInt(diff.new_total)}
              </dd>
            </div>
            <div>
              <dt className="text-text-muted">Fixed</dt>
              <dd className="text-body font-semibold text-text tabular-nums">
                {formatInt(diff.fixed_total)}
              </dd>
            </div>
            <div>
              <dt className="text-text-muted">Unchanged</dt>
              <dd className="text-body font-semibold text-text tabular-nums">
                {formatInt(diff.unchanged)}
              </dd>
            </div>
          </div>
        </dl>
      </div>

      <div className="mt-6">
        <Tabs
          idBase="scan-diff"
          label="Changes"
          value={tab}
          onChange={(next) => setPicked({ base: diff.previous_scan_id, tab: next as TabId })}
          tabs={tabs}
        />
        {(Object.keys(lists) as TabId[]).map((id) => (
          <TabPanel key={id} idBase="scan-diff" tab={id} value={tab}>
            {lists[id].total > lists[id].rows.length ? (
              <p role="status" className="px-3 pt-3 text-small text-text-muted sm:px-4">
                Showing the {formatInt(Math.min(LIST_LIMIT, lists[id].rows.length))} most severe of{' '}
                {countOf(lists[id].total, 'finding')}.
              </p>
            ) : null}
            <VulnList
              rows={lists[id].rows}
              label={lists[id].label}
              empty={lists[id].empty}
              onOpen={setOpen}
            />
          </TabPanel>
        ))}
      </div>
      {open ? (
        <VulnDrawer serviceId={scan.service_id} vuln={open} onClose={() => setOpen(null)} />
      ) : null}
    </section>
  );
}
