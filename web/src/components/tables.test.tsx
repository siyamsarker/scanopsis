// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { fireEvent, render, screen, within } from '@testing-library/react';
import { useState } from 'react';
import { DataTable } from './DataTable';
import { VirtualTable } from './VirtualTable';
import type { Column, SortState } from './tableCore';

interface Row {
  id: number;
  name: string;
  findings: number | null;
}

const data: Row[] = [
  { id: 1, name: 'payments-api', findings: 40 },
  { id: 2, name: 'auth', findings: 7 },
  { id: 3, name: 'billing', findings: null },
  { id: 4, name: 'web', findings: 1204 },
];

const columns: Column<Row>[] = [
  {
    key: 'name',
    header: 'Service',
    cell: (r) => r.name,
    sortKey: 'name',
    sortValue: (r) => r.name,
  },
  {
    key: 'findings',
    header: 'Findings',
    numeric: true,
    cell: (r) => r.findings ?? '–',
    sortKey: 'findings',
    sortValue: (r) => r.findings,
  },
  { key: 'plain', header: 'Notes', cell: () => 'note' },
];

const names = () =>
  within(screen.getAllByRole('rowgroup')[1])
    .getAllByRole('row')
    .map((row) => within(row).getAllByRole('cell')[0].textContent);

describe('DataTable', () => {
  it('has an accessible name, column headers and rows', () => {
    render(<DataTable caption="Services" columns={columns} rows={data} rowKey={(r) => r.id} />);
    expect(screen.getByRole('table', { name: 'Services' })).toBeInTheDocument();
    expect(screen.getAllByRole('columnheader').map((h) => h.textContent)).toEqual([
      'Service',
      'Findings',
      'Notes',
    ]);
    expect(names()).toEqual(['payments-api', 'auth', 'billing', 'web']);
  });

  it('aligns numeric columns right in tabular figures and keeps the header sticky', () => {
    render(<DataTable caption="Services" columns={columns} rows={data} rowKey={(r) => r.id} />);
    expect(screen.getByRole('columnheader', { name: /Findings/ })).toHaveClass(
      'text-right',
      'sticky',
    );
    const cell = screen.getByText('1204');
    expect(cell).toHaveClass('text-right', 'tabular-nums');
  });

  it('marks sortable headers with aria-sort and sorts on click', () => {
    render(<DataTable caption="Services" columns={columns} rows={data} rowKey={(r) => r.id} />);
    const service = screen.getByRole('columnheader', { name: /Service/ });
    const findings = screen.getByRole('columnheader', { name: /Findings/ });
    const notes = screen.getByRole('columnheader', { name: 'Notes' });
    expect(service).toHaveAttribute('aria-sort', 'none');
    expect(notes).not.toHaveAttribute('aria-sort');

    fireEvent.click(within(service).getByRole('button'));
    expect(service).toHaveAttribute('aria-sort', 'ascending');
    expect(names()).toEqual(['auth', 'billing', 'payments-api', 'web']);
    fireEvent.click(within(service).getByRole('button'));
    expect(service).toHaveAttribute('aria-sort', 'descending');
    expect(names()).toEqual(['web', 'payments-api', 'billing', 'auth']);

    // numbers start with the largest, and missing values stay last
    fireEvent.click(within(findings).getByRole('button'));
    expect(findings).toHaveAttribute('aria-sort', 'descending');
    expect(service).toHaveAttribute('aria-sort', 'none');
    expect(names()).toEqual(['web', 'payments-api', 'auth', 'billing']);
    fireEvent.click(within(findings).getByRole('button'));
    expect(names()).toEqual(['auth', 'payments-api', 'web', 'billing']);
  });

  it('lets the parent control sorting and skips sorting with manualSort', () => {
    const onSortChange = vi.fn();
    const sort: SortState = { key: 'findings', direction: 'asc' };
    render(
      <DataTable
        caption="Services"
        columns={columns}
        rows={data}
        rowKey={(r) => r.id}
        sort={sort}
        onSortChange={onSortChange}
        manualSort
      />,
    );
    expect(screen.getByRole('columnheader', { name: /Findings/ })).toHaveAttribute(
      'aria-sort',
      'ascending',
    );
    expect(names()).toEqual(['payments-api', 'auth', 'billing', 'web']);
    fireEvent.click(
      within(screen.getByRole('columnheader', { name: /Findings/ })).getByRole('button'),
    );
    expect(onSortChange).toHaveBeenCalledWith({ key: 'findings', direction: 'desc' });
    expect(screen.getByRole('columnheader', { name: /Findings/ })).toHaveAttribute(
      'aria-sort',
      'ascending',
    );
  });

  it('switches density from the toolbar', () => {
    const onDensityChange = vi.fn();
    render(
      <DataTable
        caption="Services"
        columns={columns}
        rows={data}
        rowKey={(r) => r.id}
        densityToggle
        onDensityChange={onDensityChange}
      />,
    );
    const cell = () => screen.getAllByRole('cell')[0];
    expect(cell()).toHaveClass('py-2.5');
    fireEvent.click(screen.getByRole('radio', { name: 'Compact' }));
    expect(cell()).toHaveClass('py-1');
    expect(onDensityChange).toHaveBeenCalledWith('compact');
  });

  it('shows skeletons while loading and the empty state without rows', () => {
    const { rerender } = render(
      <DataTable caption="Services" columns={columns} rows={[]} rowKey={(r) => r.id} loading />,
    );
    expect(screen.getByRole('status', { name: 'Loading' })).toBeInTheDocument();
    expect(screen.queryByRole('table')).toBeNull();
    rerender(
      <DataTable
        caption="Services"
        columns={columns}
        rows={[]}
        rowKey={(r) => r.id}
        empty={<p>No services yet</p>}
      />,
    );
    expect(screen.getByText('No services yet')).toBeInTheDocument();
  });

  it('renders the toolbar slot', () => {
    render(
      <DataTable
        caption="Services"
        columns={columns}
        rows={data}
        rowKey={(r) => r.id}
        toolbar={<input aria-label="Filter" />}
      />,
    );
    expect(screen.getByLabelText('Filter')).toBeInTheDocument();
  });
});

describe('VirtualTable', () => {
  const many: Row[] = Array.from({ length: 5000 }, (_, i) => ({
    id: i,
    name: `service-${i}`,
    findings: i,
  }));

  beforeEach(() => {
    // jsdom has no layout: give every element a 400 px viewport so rows are computed.
    vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockReturnValue(400);
    vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockReturnValue(800);
    vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(400);
    Element.prototype.scrollTo = vi.fn();
  });
  afterEach(() => vi.restoreAllMocks());

  it('renders only the rows in view with table semantics', () => {
    render(<VirtualTable caption="Findings" columns={columns} rows={many} rowKey={(r) => r.id} />);
    const table = screen.getByRole('table', { name: 'Findings' });
    expect(table).toHaveAttribute('aria-rowcount', '5001');
    const rows = screen.getAllByRole('row');
    expect(rows.length).toBeGreaterThan(5);
    expect(rows.length).toBeLessThan(40);
    expect(screen.getByText('service-0')).toBeInTheDocument();
    expect(screen.queryByText('service-4999')).toBeNull();
    expect(screen.getAllByRole('columnheader')).toHaveLength(3);
    expect(rows[1]).toHaveAttribute('aria-rowindex', '2');
  });

  it('sorts with the same header controls', () => {
    render(<VirtualTable caption="Findings" columns={columns} rows={many} rowKey={(r) => r.id} />);
    fireEvent.click(
      within(screen.getByRole('columnheader', { name: /Findings/ })).getByRole('button'),
    );
    expect(screen.getByRole('columnheader', { name: /Findings/ })).toHaveAttribute(
      'aria-sort',
      'descending',
    );
    expect(screen.getByText('service-4999')).toBeInTheDocument();
  });

  it('keeps one row in the tab order and moves it with the arrow keys', () => {
    render(<VirtualTable caption="Findings" columns={columns} rows={many} rowKey={(r) => r.id} />);
    const dataRows = () => screen.getAllByRole('row').slice(1);
    expect(dataRows().filter((r) => r.getAttribute('tabindex') === '0')).toHaveLength(1);
    expect(dataRows()[0]).toHaveAttribute('tabindex', '0');

    const first = dataRows()[0];
    first.focus();
    fireEvent.keyDown(first, { key: 'ArrowDown' });
    expect(dataRows()[1]).toHaveFocus();
    expect(dataRows()[1]).toHaveAttribute('tabindex', '0');
    expect(dataRows()[0]).toHaveAttribute('tabindex', '-1');
    fireEvent.keyDown(dataRows()[1], { key: 'ArrowUp' });
    expect(dataRows()[0]).toHaveFocus();
    fireEvent.keyDown(dataRows()[0], { key: 'ArrowUp' });
    expect(dataRows()[0]).toHaveFocus();
  });

  it('jumps with End and Home', () => {
    render(<VirtualTable caption="Findings" columns={columns} rows={many} rowKey={(r) => r.id} />);
    const first = screen.getAllByRole('row')[1];
    first.focus();
    fireEvent.keyDown(first, { key: 'End' });
    // The last row is virtualised away, so the table scrolls to it; the browser then renders and focuses it.
    expect(Element.prototype.scrollTo).toHaveBeenCalled();
    // with the active row out of the rendered window, the first rendered row keeps the tab stop
    expect(first).toHaveAttribute('tabindex', '0');
    fireEvent.keyDown(first, { key: 'Home' });
    expect(first).toHaveFocus();
    expect(first).toHaveAttribute('tabindex', '0');
  });

  it('activates the focused row with Enter and Space', () => {
    const onRowActivate = vi.fn();
    render(
      <VirtualTable
        caption="Findings"
        columns={columns}
        rows={many}
        rowKey={(r) => r.id}
        onRowActivate={onRowActivate}
      />,
    );
    const first = screen.getAllByRole('row')[1];
    first.focus();
    fireEvent.keyDown(first, { key: 'ArrowDown' });
    fireEvent.keyDown(document.activeElement as Element, { key: 'Enter' });
    expect(onRowActivate).toHaveBeenLastCalledWith(many[1]);
    fireEvent.keyDown(document.activeElement as Element, { key: ' ' });
    expect(onRowActivate).toHaveBeenCalledTimes(2);
  });

  it('uses fixed row heights that follow the density', () => {
    function Demo() {
      const [d, setD] = useState<'comfortable' | 'compact'>('comfortable');
      return (
        <VirtualTable
          caption="Findings"
          columns={columns}
          rows={many}
          rowKey={(r) => r.id}
          density={d}
          onDensityChange={setD}
          densityToggle
        />
      );
    }
    render(<Demo />);
    expect(screen.getAllByRole('row')[1]).toHaveClass('h-11');
    fireEvent.click(screen.getByRole('radio', { name: 'Compact' }));
    expect(screen.getAllByRole('row')[1]).toHaveClass('h-8');
  });

  it('shows loading and empty states', () => {
    const { rerender } = render(
      <VirtualTable caption="Findings" columns={columns} rows={[]} rowKey={(r) => r.id} loading />,
    );
    expect(screen.getByRole('status', { name: 'Loading' })).toBeInTheDocument();
    rerender(
      <VirtualTable
        caption="Findings"
        columns={columns}
        rows={[]}
        rowKey={(r) => r.id}
        empty={<p>No findings</p>}
      />,
    );
    expect(screen.getByText('No findings')).toBeInTheDocument();
  });
});
