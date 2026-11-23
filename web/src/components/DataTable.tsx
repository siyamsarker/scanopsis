// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { SkeletonTable } from './states';
import {
  HeaderCell,
  TableToolbar,
  ariaSort,
  cellClass,
  headerCellClass,
  useTableState,
  type TableProps,
} from './tableCore';

/**
 * A table for tens to a few hundred rows: sortable headers (aria-sort), a sticky
 * header inside a height-limited scroll area, comfortable or compact rows, numeric
 * columns right-aligned in tabular figures, and horizontal scrolling inside the
 * card on small screens so the page itself never scrolls sideways.
 * For thousands of rows use VirtualTable, which takes the same props.
 */
export function DataTable<T>(props: TableProps<T>) {
  const { columns, rowKey, caption, loading, empty, toolbar, densityToggle } = props;
  const { rows, sort, density, toggleSort, setDensity } = useTableState(props);
  const scrollClass = props.scrollClass ?? 'max-h-[70vh]';

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
        <div className={`overflow-auto ${scrollClass}`}>
          <table className="w-full border-separate border-spacing-0">
            <caption className="sr-only">{caption}</caption>
            <thead>
              <tr>
                {columns.map((column) => (
                  <th
                    key={column.key}
                    scope="col"
                    aria-sort={ariaSort(column, sort)}
                    className={`${headerCellClass} sticky top-0 z-10 border-b border-border ${
                      column.numeric ? 'text-right' : 'text-left'
                    } ${column.width ?? ''}`}
                  >
                    <HeaderCell column={column} sort={sort} onSort={toggleSort} />
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <tr key={rowKey(row)} className="hover-tint">
                  {columns.map((column) => (
                    <td
                      key={column.key}
                      className={`${cellClass(column, density)} border-b border-border/70 align-middle`}
                    >
                      {column.cell(row)}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
