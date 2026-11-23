// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { useVirtualizer } from '@tanstack/react-virtual';
import { useEffect, useRef, useState, type KeyboardEvent } from 'react';
import { useCssom } from './cssom';
import { SkeletonTable } from './states';
import {
  HeaderCell,
  TableToolbar,
  ariaSort,
  cellClass,
  headerCellClass,
  useTableState,
  type Column,
  type Density,
  type TableProps,
} from './tableCore';

/** Row heights in pixels; fixed so scrolling math is exact and nothing shifts. */
const ROW_HEIGHT: Record<Density, number> = { comfortable: 44, compact: 32 };
const ROW_CLASS: Record<Density, string> = { comfortable: 'h-11', compact: 'h-8' };

export interface VirtualTableProps<T> extends TableProps<T> {
  /** Called when a row is activated with Enter or Space, e.g. to open the finding. */
  onRowActivate?: (row: T) => void;
  /** Tailwind height of the scroll area. Required in practice: the table scrolls inside it. */
  heightClass?: string;
  /** Tailwind min-width of the table content; below it the area scrolls sideways. */
  minWidthClass?: string;
}

function VirtualRow<T>({
  row,
  index,
  start,
  density,
  columns,
  active,
  onFocusRow,
}: {
  row: T;
  index: number;
  start: number;
  density: Density;
  columns: Column<T>[];
  active: boolean;
  onFocusRow: (index: number) => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  useCssom(ref, { transform: `translateY(${start}px)` });
  return (
    <div
      ref={ref}
      role="row"
      aria-rowindex={index + 2}
      data-row-index={index}
      data-roving="row"
      tabIndex={active ? 0 : -1}
      onFocus={() => onFocusRow(index)}
      className={`hover-tint absolute inset-x-0 top-0 flex items-center border-b border-border/70 ${ROW_CLASS[density]}`}
    >
      {columns.map((column) => (
        <div
          key={column.key}
          role="cell"
          className={`${cellClass(column, density)} min-w-0 truncate ${column.width ?? 'flex-1'}`}
        >
          {column.cell(row)}
        </div>
      ))}
    </div>
  );
}

/**
 * The same column API as DataTable, for thousands of rows: only the rows in view
 * are in the DOM (@tanstack/react-virtual) and every row has a fixed height.
 * Keyboard: Tab enters the active row; ArrowUp/ArrowDown, PageUp/PageDown, Home
 * and End move it (scrolling as needed); Enter or Space calls `onRowActivate`.
 * Cell content is truncated to one line, so keep cells short or use `title`.
 */
export function VirtualTable<T>(props: VirtualTableProps<T>) {
  const { columns, rowKey, caption, loading, empty, toolbar, densityToggle, onRowActivate } = props;
  const { rows, sort, density, toggleSort, setDensity } = useTableState(props);
  const scrollRef = useRef<HTMLDivElement>(null);
  const bodyRef = useRef<HTMLDivElement>(null);
  const [active, setActive] = useState(0);
  const pendingFocus = useRef<number | null>(null);

  // The virtualizer's functions are not memoised by the React Compiler lint, which only
  // affects this component's own memoisation; nothing here is passed to a memoised child.
  // eslint-disable-next-line react-hooks/incompatible-library
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => ROW_HEIGHT[density],
    overscan: 8,
  });

  // Row height depends on density; tell the virtualizer to measure again.
  useEffect(() => {
    virtualizer.measure();
  }, [density, virtualizer]);

  useCssom(bodyRef, { height: `${virtualizer.getTotalSize()}px` });

  // Focus a row once it is rendered: after a keyboard move it may have been virtualised away.
  useEffect(() => {
    const index = pendingFocus.current;
    if (index === null) return;
    const el = scrollRef.current?.querySelector<HTMLElement>(`[data-row-index="${index}"]`);
    if (el) {
      pendingFocus.current = null;
      el.focus({ preventScroll: true });
    }
  });

  const lastIndex = rows.length - 1;
  const current = Math.min(active, Math.max(lastIndex, 0));
  const items = virtualizer.getVirtualItems();
  // If the active row has scrolled out of the rendered window, the first rendered
  // row takes the tab stop so keyboard users can always enter the table.
  const activeInView = items.some((item) => item.index === current);

  function moveTo(index: number) {
    const next = Math.max(0, Math.min(lastIndex, index));
    setActive(next);
    pendingFocus.current = next;
    virtualizer.scrollToIndex(next);
  }

  function onKeyDown(event: KeyboardEvent) {
    const page = Math.max(
      1,
      Math.floor((scrollRef.current?.clientHeight ?? 400) / ROW_HEIGHT[density]) - 1,
    );
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
        moveTo(lastIndex);
        break;
      case 'Enter':
      case ' ':
        if (onRowActivate && rows[current] !== undefined) onRowActivate(rows[current]);
        break;
      default:
        return;
    }
    event.preventDefault();
  }

  return (
    <div
      className={`overflow-hidden rounded-card border border-border bg-surface ${props.className ?? ''}`}
    >
      <TableToolbar
        toolbar={toolbar}
        densityToggle={densityToggle}
        density={density}
        onDensityChange={setDensity}
      />
      {loading ? (
        <SkeletonTable columns={Math.min(columns.length, 5)} density={density} />
      ) : rows.length === 0 && empty ? (
        empty
      ) : (
        <div
          role="table"
          aria-label={caption}
          aria-rowcount={rows.length + 1}
          aria-colcount={columns.length}
        >
          <div
            ref={scrollRef}
            className={`overflow-auto ${props.heightClass ?? 'h-[70vh]'}`}
            onKeyDown={onKeyDown}
          >
            <div className={props.minWidthClass ?? 'min-w-[48rem]'}>
              <div role="rowgroup" className="sticky top-0 z-10 bg-surface">
                <div
                  role="row"
                  aria-rowindex={1}
                  className="flex items-center border-b border-border"
                >
                  {columns.map((column) => (
                    <div
                      key={column.key}
                      role="columnheader"
                      aria-sort={ariaSort(column, sort)}
                      className={`${headerCellClass} ${column.numeric ? 'text-right' : 'text-left'} ${column.width ?? 'flex-1'} min-w-0`}
                    >
                      <HeaderCell column={column} sort={sort} onSort={toggleSort} />
                    </div>
                  ))}
                </div>
              </div>
              <div ref={bodyRef} role="rowgroup" className="relative">
                {items.map((item) => (
                  <VirtualRow
                    key={rowKey(rows[item.index])}
                    row={rows[item.index]}
                    index={item.index}
                    start={item.start}
                    density={density}
                    columns={columns}
                    active={item.index === (activeInView ? current : items[0]?.index)}
                    onFocusRow={setActive}
                  />
                ))}
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
