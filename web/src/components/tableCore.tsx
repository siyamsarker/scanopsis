// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { ArrowDown, ArrowUp, ChevronsUpDown } from 'lucide-react';
import { useMemo, useState, type ReactNode } from 'react';
import { Segmented } from './Segmented';

/**
 * One column definition, shared by DataTable and VirtualTable.
 *
 * `width` is a Tailwind width class ("w-32 shrink-0"); without it the column
 * shares the remaining space. `numeric` right-aligns the column and uses
 * tabular figures, so put counts, sizes and durations there.
 */
export interface Column<T> {
  /** Stable id of the column. */
  key: string;
  /** The header text; keep it short and in sentence case. */
  header: string;
  /** Renders the cell for a row. */
  cell: (row: T) => ReactNode;
  /** Makes the header a sort control. The value is passed to `onSortChange`. */
  sortKey?: string;
  /** Used for built-in sorting by this column; not needed with `manualSort`. */
  sortValue?: (row: T) => string | number | null | undefined;
  numeric?: boolean;
  width?: string;
  /** Extra classes for every cell of the column, such as "font-mono". */
  className?: string;
}

export interface SortState {
  key: string;
  direction: 'asc' | 'desc';
}

export type Density = 'comfortable' | 'compact';

/** Shared props of both tables. */
export interface TableProps<T> {
  columns: Column<T>[];
  rows: T[];
  /** A stable unique key per row (an id, not the index). */
  rowKey: (row: T) => string | number;
  /** The table's accessible name: "Findings", "Users". */
  caption: string;
  /** Controlled sort; with `onSortChange` the parent decides. */
  sort?: SortState | null;
  defaultSort?: SortState | null;
  onSortChange?: (sort: SortState) => void;
  /** The rows are already sorted (by the server): do not sort them again. */
  manualSort?: boolean;
  density?: Density;
  defaultDensity?: Density;
  onDensityChange?: (density: Density) => void;
  /** Shows the comfortable/compact switch in the toolbar. */
  densityToggle?: boolean;
  /** Content for the left of the toolbar: search, chips. */
  toolbar?: ReactNode;
  /** Shown instead of the rows when there are none. */
  empty?: ReactNode;
  /** Shows skeleton rows while the first page loads. */
  loading?: boolean;
  /** Tailwind classes limiting the height so the header can stay in view. */
  scrollClass?: string;
  className?: string;
}

function compare(a: string | number | null | undefined, b: string | number | null | undefined) {
  if (a == null && b == null) return 0;
  if (a == null) return 1;
  if (b == null) return -1;
  if (typeof a === 'number' && typeof b === 'number') return a - b;
  return String(a).localeCompare(String(b), 'en', { numeric: true, sensitivity: 'base' });
}

/** Sort and density state, in controlled or uncontrolled form. */
export function useTableState<T>(props: TableProps<T>) {
  const [innerSort, setInnerSort] = useState<SortState | null>(props.defaultSort ?? null);
  const [innerDensity, setInnerDensity] = useState<Density>(props.defaultDensity ?? 'comfortable');
  const sort = props.sort !== undefined ? props.sort : innerSort;
  const density = props.density ?? innerDensity;

  const rows = useMemo(() => {
    if (props.manualSort || !sort) return props.rows;
    const column = props.columns.find((c) => c.sortKey === sort.key);
    const value = column?.sortValue;
    if (!value) return props.rows;
    const sign = sort.direction === 'asc' ? 1 : -1;
    return [...props.rows].sort((a, b) => {
      const av = value(a);
      const bv = value(b);
      // Missing values stay last in both directions.
      if (av == null || bv == null) return compare(av, bv);
      return sign * compare(av, bv);
    });
  }, [props.rows, props.columns, props.manualSort, sort]);

  function toggleSort(column: Column<T>) {
    if (!column.sortKey) return;
    const next: SortState =
      sort?.key === column.sortKey
        ? { key: column.sortKey, direction: sort.direction === 'asc' ? 'desc' : 'asc' }
        : { key: column.sortKey, direction: column.numeric ? 'desc' : 'asc' };
    setInnerSort(next);
    props.onSortChange?.(next);
  }

  function setDensity(next: Density) {
    setInnerDensity(next);
    props.onDensityChange?.(next);
  }

  return { rows, sort, density, toggleSort, setDensity };
}

export function ariaSort(column: { sortKey?: string }, sort: SortState | null) {
  if (!column.sortKey) return undefined;
  if (sort?.key !== column.sortKey) return 'none' as const;
  return sort.direction === 'asc' ? ('ascending' as const) : ('descending' as const);
}

/** Header content: plain text, or a button that sorts when the column has a sortKey. */
export function HeaderCell<T>({
  column,
  sort,
  onSort,
}: {
  column: Column<T>;
  sort: SortState | null;
  onSort: (column: Column<T>) => void;
}) {
  if (!column.sortKey) return <>{column.header}</>;
  const active = sort?.key === column.sortKey;
  const Icon = !active ? ChevronsUpDown : sort.direction === 'asc' ? ArrowUp : ArrowDown;
  return (
    <button
      type="button"
      onClick={() => onSort(column)}
      className={`-mx-1 inline-flex items-center gap-1 rounded px-1 font-medium transition-colors duration-150 ease-out hover:text-text ${
        column.numeric ? 'flex-row-reverse' : ''
      } ${active ? 'text-text' : ''}`}
    >
      {column.header}
      <Icon
        size={14}
        strokeWidth={1.5}
        aria-hidden="true"
        className={active ? 'text-text' : 'text-text-muted'}
      />
    </button>
  );
}

export const headerCellClass =
  'bg-surface px-4 py-2 text-sm font-medium text-text-muted whitespace-nowrap';

export function cellClass<T>(column: Column<T>, density: Density) {
  return `${density === 'compact' ? 'py-1' : 'py-2.5'} px-4 ${column.numeric ? 'text-right tabular-nums' : 'text-left'} ${column.className ?? ''}`;
}

/** The strip above a table: caller content on the left, density switch on the right. */
export function TableToolbar({
  toolbar,
  densityToggle,
  density,
  onDensityChange,
}: {
  toolbar?: ReactNode;
  densityToggle?: boolean;
  density: Density;
  onDensityChange: (density: Density) => void;
}) {
  if (!toolbar && !densityToggle) return null;
  return (
    <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border px-4 py-3">
      <div className="flex min-w-0 flex-wrap items-center gap-3">{toolbar}</div>
      {densityToggle ? (
        <Segmented
          label="Row density"
          value={density}
          onChange={onDensityChange}
          options={[
            { value: 'comfortable', label: 'Comfortable' },
            { value: 'compact', label: 'Compact' },
          ]}
        />
      ) : null}
    </div>
  );
}
