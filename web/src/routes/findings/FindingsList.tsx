// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { useWindowVirtualizer } from '@tanstack/react-virtual';
import { ArrowDown, ArrowUp, ChevronDown, ChevronsUpDown } from 'lucide-react';
import {
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type HTMLAttributes,
  type KeyboardEvent,
  type ReactNode,
} from 'react';
import type { Vulnerability } from '../../api/findings';
import { ArtifactDetail } from '../../components/ArtifactName';
import { useCssom } from '../../components/cssom';
import { SeverityMark } from '../../components/SeverityMark';
import { SEVERITY_LABEL, severityKey } from '../../components/severityModel';
import { SkeletonTable } from '../../components/states';
import { countOf, formatExactTime, formatInt, relativeTime } from '../../format';
import { DistributionMini } from '../../viz';
import {
  groupRows,
  isFixable,
  type FindingGroup,
  type GroupBy,
  type Sort,
  type SortKey,
} from './model';

const ROW = 64;
const GROUP = 56;

type Item =
  | { type: 'group'; group: FindingGroup; expanded: boolean; level: 1 }
  | { type: 'row'; vuln: Vulnerability; level: 1 | 2; groupIndex: number | null };

/** The grid shared by the header and the rows: severity, finding, image, first seen. */
export const ROW_GRID =
  'grid grid-cols-[1.25rem_minmax(0,1fr)] gap-x-3 sm:grid-cols-[6rem_minmax(0,1fr)] md:grid-cols-[6rem_minmax(0,1fr)_minmax(0,12rem)_6rem] md:gap-x-4 xl:grid-cols-[6.5rem_minmax(0,1fr)_minmax(0,15rem)_6.5rem]';

/**
 * The scanned target without the image name Trivy repeats in it:
 * "python:3.9 (debian 10.13)" in image "python:3.9" reads "debian 10.13", and
 * "web-frontend-stage (alpine 3.20.3)" in web-frontend-stage "alpine 3.20.3".
 * The name must be the whole first word, so "app/package-lock.json" in image
 * "app" stays as it is.
 */
export function targetLabel(target: string, artifact: string) {
  if (!artifact || !target.startsWith(artifact)) return target;
  if (target.length > artifact.length && !/\s/.test(target[artifact.length])) return target;
  const rest = target
    .slice(artifact.length)
    .trim()
    .replace(/^\((.*)\)$/, '$1');
  return rest || target;
}

/** The target of a finding without the image it is in, named by key or, in older scans, by tag. */
export function findingTarget(v: Pick<Vulnerability, 'target' | 'artifact_key' | 'artifact_name'>) {
  const byKey = targetLabel(v.target, v.artifact_key);
  return byKey !== v.target ? byKey : targetLabel(v.target, v.artifact_name);
}

/** "pkg@1.0 → 1.1" in one truncating line; the fixed version in the ok colour. */
export function VersionLine({ vuln, className = '' }: { vuln: Vulnerability; className?: string }) {
  const fixed = isFixable(vuln);
  return (
    <span
      className={`block min-w-0 truncate font-mono text-small text-text-muted ${className}`}
      title={`${vuln.pkg_name} ${vuln.installed_version} → ${fixed ? vuln.fixed_version : 'no fix'}`}
    >
      <span className="text-text">{vuln.pkg_name}</span>
      <span className="text-text-faint">@</span>
      {vuln.installed_version}
      <span aria-hidden="true" className="px-1.5 font-sans text-text-faint">
        →
      </span>
      <span className="sr-only"> fixed in </span>
      {fixed ? (
        <span className="tone-ok tone-ink">{vuln.fixed_version}</span>
      ) : (
        <span className="font-sans text-text-faint">no fix yet</span>
      )}
    </span>
  );
}

function SortButton({
  label,
  column,
  sort,
  onSort,
  className = '',
}: {
  label: string;
  column: SortKey;
  sort: Sort;
  onSort: (key: SortKey) => void;
  className?: string;
}) {
  const active = sort.key === column;
  const Icon = !active ? ChevronsUpDown : sort.direction === 'asc' ? ArrowUp : ArrowDown;
  return (
    <button
      type="button"
      onClick={() => onSort(column)}
      className={`-mx-1 inline-flex items-center gap-1 rounded px-1 transition-colors duration-micro hover:text-text ${active ? 'text-text' : ''} ${className}`}
    >
      {label}
      <Icon size={13} strokeWidth={1.75} aria-hidden="true" />
    </button>
  );
}

function ariaSortOf(sort: Sort, key: SortKey) {
  if (sort.key !== key) return 'none' as const;
  return sort.direction === 'asc' ? ('ascending' as const) : ('descending' as const);
}

function Positioned({
  start,
  children,
  className,
  ...rest
}: {
  start: number;
  children: ReactNode;
  className: string;
} & HTMLAttributes<HTMLDivElement> &
  Record<`data-${string}`, string | number | undefined>) {
  const ref = useRef<HTMLDivElement>(null);
  useCssom(ref, { transform: `translateY(${start}px)` });
  return (
    <div ref={ref} className={className} {...rest}>
      {children}
    </div>
  );
}

export interface FindingsListProps {
  rows: Vulnerability[];
  groupBy: GroupBy;
  sort: Sort;
  onSortChange: (sort: Sort) => void;
  onOpen: (vuln: Vulnerability) => void;
  /** The finding shown in the drawer, highlighted in the list. */
  openId?: number | null;
  loading?: boolean;
  empty?: ReactNode;
  /** Changes when the filters change; the list plays its focus transition. */
  focusKey?: string;
}

/**
 * Every finding of the selection in one list that scrolls with the page.
 * Only the rows in view are in the DOM. Grouped, each group is a row of its
 * own (a tree grid) that collapses. Keyboard: Tab enters the active row;
 * the arrow keys, Page Up/Down, Home and End move; Enter opens a finding or
 * folds a group; Left folds a group or moves to it, Right unfolds.
 */
export function FindingsList({
  rows,
  groupBy,
  sort,
  onSortChange,
  onOpen,
  openId = null,
  loading = false,
  empty,
  focusKey = '',
}: FindingsListProps) {
  const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(() => new Set());
  const groups = useMemo(
    () => (groupBy === 'none' ? null : groupRows(rows, groupBy)),
    [rows, groupBy],
  );
  const items = useMemo<Item[]>(() => {
    if (!groups) return rows.map((vuln) => ({ type: 'row', vuln, level: 1, groupIndex: null }));
    const out: Item[] = [];
    for (const group of groups) {
      const expanded = !collapsed.has(group.key);
      const groupIndex = out.length;
      out.push({ type: 'group', group, expanded, level: 1 });
      if (expanded)
        for (const vuln of group.rows) out.push({ type: 'row', vuln, level: 2, groupIndex });
    }
    return out;
  }, [groups, rows, collapsed]);

  const listRef = useRef<HTMLDivElement>(null);
  const bodyRef = useRef<HTMLDivElement>(null);
  const [margin, setMargin] = useState(0);
  const [active, setActive] = useState(0);
  const pendingFocus = useRef<number | null>(null);

  // The list starts below the page header and the filters; the virtualizer
  // needs that offset, which moves whenever the content above changes size.
  const shown = !loading && items.length > 0;
  useLayoutEffect(() => {
    const el = bodyRef.current;
    if (!shown || !el) return;
    const measure = () => setMargin(Math.round(el.getBoundingClientRect().top + window.scrollY));
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(document.body);
    return () => observer.disconnect();
  }, [shown]);

  const virtualizer = useWindowVirtualizer({
    count: items.length,
    estimateSize: (index) => (items[index]?.type === 'group' ? GROUP : ROW),
    overscan: 12,
    scrollMargin: margin,
  });

  useEffect(() => {
    virtualizer.measure();
  }, [items, virtualizer]);

  useCssom(bodyRef, { height: `${virtualizer.getTotalSize()}px` });

  // The focus lens: when the filters change, the list settles into focus.
  useEffect(() => {
    const el = bodyRef.current;
    if (!el || !focusKey || typeof el.animate !== 'function') return;
    if (window.matchMedia?.('(prefers-reduced-motion: reduce)').matches) return;
    el.animate(
      [
        { opacity: 0.35, transform: 'scale(0.995)', filter: 'blur(1.5px)' },
        { opacity: 1, transform: 'none', filter: 'none' },
      ],
      { duration: 240, easing: 'cubic-bezier(.2,.8,.2,1)' },
    );
  }, [focusKey]);

  useEffect(() => {
    const index = pendingFocus.current;
    if (index === null) return;
    const el = listRef.current?.querySelector<HTMLElement>(`[data-item-index="${index}"]`);
    if (el) {
      pendingFocus.current = null;
      el.focus({ preventScroll: true });
    }
  });

  const last = items.length - 1;
  const current = Math.min(active, Math.max(last, 0));
  const virtualItems = virtualizer.getVirtualItems();
  const activeInView = virtualItems.some((item) => item.index === current);

  function moveTo(index: number) {
    const next = Math.max(0, Math.min(last, index));
    setActive(next);
    pendingFocus.current = next;
    virtualizer.scrollToIndex(next, { align: 'auto' });
  }

  function toggle(group: FindingGroup, open?: boolean) {
    setCollapsed((prev) => {
      const next = new Set(prev);
      const isOpen = !prev.has(group.key);
      if ((open ?? !isOpen) === true) next.delete(group.key);
      else next.add(group.key);
      return next;
    });
  }

  function onKeyDown(event: KeyboardEvent) {
    const item = items[current];
    const page = Math.max(1, Math.floor(window.innerHeight / ROW) - 2);
    switch (event.key) {
      case 'ArrowDown':
        moveTo(current + 1);
        break;
      case 'ArrowUp':
        moveTo(current - 1);
        break;
      case 'PageDown':
        moveTo(current + page);
        break;
      case 'PageUp':
        moveTo(current - page);
        break;
      case 'Home':
        moveTo(0);
        break;
      case 'End':
        moveTo(last);
        break;
      case 'ArrowLeft':
        if (item?.type === 'group' && item.expanded) toggle(item.group, false);
        else if (item?.type === 'row' && item.groupIndex !== null) moveTo(item.groupIndex);
        else return;
        break;
      case 'ArrowRight':
        if (item?.type === 'group' && !item.expanded) toggle(item.group, true);
        else return;
        break;
      case 'Enter':
      case ' ':
        if (item?.type === 'group') toggle(item.group);
        else if (item?.type === 'row') onOpen(item.vuln);
        else return;
        break;
      default:
        return;
    }
    event.preventDefault();
  }

  function onSort(key: SortKey) {
    onSortChange(
      sort.key === key
        ? { key, direction: sort.direction === 'asc' ? 'desc' : 'asc' }
        : { key, direction: key === 'package' ? 'asc' : 'desc' },
    );
  }

  const tree = groups !== null;
  const header = (
    <div
      role="row"
      aria-rowindex={1}
      className={`${ROW_GRID} items-center border-b border-line px-3 py-2 text-caption font-medium text-text-muted sm:px-4`}
    >
      <span role="columnheader" aria-sort={ariaSortOf(sort, 'severity')}>
        <SortButton
          label="Severity"
          column="severity"
          sort={sort}
          onSort={onSort}
          className="max-sm:sr-only"
        />
      </span>
      <span role="columnheader" aria-sort={ariaSortOf(sort, 'package')}>
        <SortButton
          label="Vulnerability and package"
          column="package"
          sort={sort}
          onSort={onSort}
        />
      </span>
      <span role="columnheader" className="hidden md:block">
        Image and target
      </span>
      <span
        role="columnheader"
        aria-sort={ariaSortOf(sort, 'seen')}
        className="hidden text-right md:block"
      >
        <SortButton label="First seen" column="seen" sort={sort} onSort={onSort} />
      </span>
    </div>
  );

  if (loading) {
    return (
      <div className="overflow-hidden rounded-panel border border-line">
        <SkeletonTable columns={4} />
      </div>
    );
  }
  if (items.length === 0) return <>{empty}</>;

  return (
    <div
      ref={listRef}
      role={tree ? 'treegrid' : 'grid'}
      aria-label="Vulnerabilities"
      aria-rowcount={items.length + 1}
      aria-colcount={4}
      onKeyDown={onKeyDown}
      className="findings-list"
    >
      <div
        role="rowgroup"
        className="sticky top-14 z-10 bg-[color-mix(in_srgb,var(--bg)_92%,transparent)] backdrop-blur-sm md:top-0"
      >
        {header}
      </div>
      <div ref={bodyRef} role="rowgroup" className="relative">
        {virtualItems.map((virtual) => {
          const item = items[virtual.index];
          const index = virtual.index;
          const focusable = index === (activeInView ? current : virtualItems[0]?.index);
          const common = {
            role: 'row',
            'aria-rowindex': index + 2,
            'data-item-index': index,
            'data-roving': 'row',
            tabIndex: focusable ? 0 : -1,
            onFocus: () => setActive(index),
          };
          if (item.type === 'group') {
            return (
              <Positioned
                key={`g:${item.group.key}`}
                start={virtual.start - margin}
                {...common}
                aria-level={1}
                aria-expanded={item.expanded}
                onClick={() => {
                  setActive(index);
                  toggle(item.group);
                }}
                className="absolute inset-x-0 top-0 flex h-14 cursor-pointer items-center gap-3 border-b border-line bg-sunken/70 px-3 transition-colors duration-micro hover:bg-wash sm:px-4"
              >
                <GroupHeader group={item.group} by={groupBy} expanded={item.expanded} />
              </Positioned>
            );
          }
          const v = item.vuln;
          const sev = severityKey(v.severity);
          const open = openId === v.id;
          return (
            <Positioned
              key={`r:${v.id}`}
              start={virtual.start - margin}
              {...common}
              aria-level={tree ? 2 : undefined}
              aria-selected={open || undefined}
              data-severity={sev}
              onClick={() => {
                setActive(index);
                onOpen(v);
              }}
              className={`group/row absolute inset-x-0 top-0 ${ROW_GRID} h-16 cursor-pointer content-center items-center border-b border-line/70 px-3 transition-[background-color,box-shadow] duration-micro hover:bg-wash sm:px-4 ${tree ? 'sm:pl-8' : ''} ${open ? 'bg-wash shadow-[inset_2px_0_0_var(--brand)]' : ''} ${v.suppressed ? 'opacity-70' : ''}`}
            >
              <span role="gridcell" className="flex items-center gap-2">
                <SeverityMark severity={sev} size={13} />
                <span className={`tone-${sev} tone-ink text-small font-medium max-sm:sr-only`}>
                  {SEVERITY_LABEL[sev]}
                </span>
              </span>
              <span role="gridcell" className="min-w-0">
                <span className="flex min-w-0 items-baseline gap-2">
                  <span className="shrink-0 font-mono text-body-s font-medium text-text">
                    {v.vuln_id}
                  </span>
                  <span className="min-w-0 truncate text-body-s text-text-muted" title={v.title}>
                    {v.title || 'No title reported'}
                  </span>
                  {v.suppressed ? (
                    <span className="tag shrink-0" title={v.suppression || undefined}>
                      Suppressed
                    </span>
                  ) : null}
                </span>
                <VersionLine vuln={v} className="mt-1" />
              </span>
              <span role="gridcell" className="hidden min-w-0 md:block">
                <span className="block truncate text-body-s text-text" title={v.artifact_name}>
                  {v.artifact_key}
                </span>
                <span className="mt-1 block truncate text-caption text-text-faint" title={v.target}>
                  {findingTarget(v)}
                </span>
              </span>
              <span role="gridcell" className="hidden text-right md:block">
                <time
                  dateTime={v.first_seen_at}
                  title={formatExactTime(v.first_seen_at)}
                  className="text-body-s text-text-muted"
                >
                  {relativeTime(v.first_seen_at)}
                </time>
              </span>
            </Positioned>
          );
        })}
      </div>
      {tree ? (
        <p className="sr-only" role="status">
          {countOf(groups.length, 'group')}
        </p>
      ) : null}
    </div>
  );
}

const GROUP_NOUN: Record<Exclude<GroupBy, 'none'>, string> = {
  package: 'package',
  vulnerability: 'vulnerability',
  image: 'image',
};

function GroupHeader({
  group,
  by,
  expanded,
}: {
  group: FindingGroup;
  by: GroupBy;
  expanded: boolean;
}) {
  const noun = by === 'none' ? 'group' : GROUP_NOUN[by];
  return (
    <>
      <span role="gridcell" className="contents">
        <ChevronDown
          size={16}
          strokeWidth={1.75}
          aria-hidden="true"
          className={`shrink-0 text-text-muted transition-transform duration-micro ${expanded ? '' : '-rotate-90'}`}
        />
        <span className="flex min-w-0 flex-1 items-baseline gap-2">
          <span className="sr-only">{noun} </span>
          <span
            className={`min-w-0 truncate font-medium text-text ${by === 'image' ? 'text-body-s' : 'font-mono text-body-s'}`}
            title={group.label}
          >
            {group.label}
          </span>
          {group.latest ? (
            <ArtifactDetail
              label="Latest"
              artifact={{ artifact_key: group.key, artifact_name: group.latest, kind: 'image' }}
              className="shrink max-sm:hidden"
            />
          ) : null}
          {group.version ? (
            <span className="shrink-0 font-mono text-small text-text-muted max-sm:hidden">
              {group.version}
              {group.upgradeTo ? (
                <>
                  <span aria-hidden="true" className="px-1 font-sans text-text-faint">
                    →
                  </span>
                  <span className="sr-only"> upgrade to </span>
                  <span className="tone-ok tone-ink">{group.upgradeTo}</span>
                </>
              ) : null}
            </span>
          ) : null}
          {group.title ? (
            <span className="hidden min-w-0 truncate text-body-s text-text-muted sm:inline">
              {group.title}
            </span>
          ) : null}
        </span>
      </span>
      <span role="gridcell" className="flex shrink-0 items-center gap-3 text-small text-text-muted">
        <span className="max-sm:hidden">
          <DistributionMini counts={group.counts} label={`Findings of ${group.label}`} />
        </span>
        <span className="w-24 text-right tabular-nums max-sm:hidden">
          {countOf(group.rows.length, 'finding')}
        </span>
        <span className="w-20 text-right tabular-nums">
          <span className="font-medium text-text">{formatInt(group.risk)}</span> pts
        </span>
      </span>
    </>
  );
}
