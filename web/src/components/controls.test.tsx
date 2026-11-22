// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { act, fireEvent, render, screen } from '@testing-library/react';
import { useState } from 'react';
import { FilterChips } from './FilterChips';
import { SearchInput, Select } from './inputs';
import { Pagination } from './Pagination';
import { Segmented } from './Segmented';
import { TabPanel, Tabs } from './Tabs';
import { chooseOption } from '../test-utils';

const tabs = [
  { id: 'overview', label: 'Overview' },
  { id: 'findings', label: 'Findings', count: 1204 },
  { id: 'scans', label: 'Scans' },
];

function TabsDemo({ onChange }: { onChange?: (id: string) => void }) {
  const [value, setValue] = useState('overview');
  return (
    <>
      <Tabs
        tabs={tabs}
        value={value}
        idBase="svc"
        label="Service sections"
        onChange={(id) => {
          setValue(id);
          onChange?.(id);
        }}
      />
      <TabPanel idBase="svc" tab="overview" value={value}>
        overview content
      </TabPanel>
      <TabPanel idBase="svc" tab="findings" value={value}>
        findings content
      </TabPanel>
    </>
  );
}

describe('Tabs', () => {
  it('has a labelled tablist with one tab in the tab order', () => {
    render(<TabsDemo />);
    expect(screen.getByRole('tablist', { name: 'Service sections' })).toBeInTheDocument();
    const [overview, findings, scans] = screen.getAllByRole('tab');
    expect(overview).toHaveAttribute('aria-selected', 'true');
    expect(overview).toHaveAttribute('tabindex', '0');
    expect(findings).toHaveAttribute('tabindex', '-1');
    expect(scans).toHaveAttribute('tabindex', '-1');
    expect(findings).toHaveTextContent('1,204');
  });

  it('moves and selects with arrows, Home and End, wrapping at the ends', () => {
    const onChange = vi.fn();
    render(<TabsDemo onChange={onChange} />);
    const tablist = screen.getByRole('tablist');
    fireEvent.keyDown(tablist, { key: 'ArrowRight' });
    expect(screen.getByRole('tab', { name: /Findings/ })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('tab', { name: /Findings/ })).toHaveFocus();
    fireEvent.keyDown(tablist, { key: 'End' });
    expect(screen.getByRole('tab', { name: 'Scans' })).toHaveFocus();
    fireEvent.keyDown(tablist, { key: 'ArrowRight' });
    expect(screen.getByRole('tab', { name: 'Overview' })).toHaveFocus();
    fireEvent.keyDown(tablist, { key: 'ArrowLeft' });
    expect(screen.getByRole('tab', { name: 'Scans' })).toHaveFocus();
    fireEvent.keyDown(tablist, { key: 'Home' });
    expect(screen.getByRole('tab', { name: 'Overview' })).toHaveFocus();
    expect(onChange).toHaveBeenLastCalledWith('overview');
  });

  it('ignores other keys', () => {
    const onChange = vi.fn();
    render(<TabsDemo onChange={onChange} />);
    fireEvent.keyDown(screen.getByRole('tablist'), { key: 'a' });
    expect(onChange).not.toHaveBeenCalled();
  });

  it('connects each tab to its panel and shows only the selected one', () => {
    render(<TabsDemo />);
    const panel = screen.getByRole('tabpanel');
    expect(panel).toHaveTextContent('overview content');
    expect(panel).toHaveAccessibleName('Overview');
    expect(screen.getByRole('tab', { name: 'Overview' })).toHaveAttribute(
      'aria-controls',
      panel.id,
    );
    expect(screen.queryByText('findings content')).toBeNull();
    fireEvent.click(screen.getByRole('tab', { name: /Findings/ }));
    expect(screen.getByRole('tabpanel')).toHaveTextContent('findings content');
  });
});

function SegmentedDemo({ onChange }: { onChange?: (v: string) => void }) {
  const [value, setValue] = useState('table');
  return (
    <Segmented
      label="View"
      value={value}
      onChange={(v) => {
        setValue(v);
        onChange?.(v);
      }}
      options={[
        { value: 'table', label: 'Table' },
        { value: 'treemap', label: 'Treemap' },
        { value: 'list', label: 'List' },
      ]}
    />
  );
}

describe('Segmented', () => {
  it('is a labelled radio group with a roving tabindex', () => {
    render(<SegmentedDemo />);
    expect(screen.getByRole('radiogroup', { name: 'View' })).toBeInTheDocument();
    const radios = screen.getAllByRole('radio');
    expect(radios.map((r) => r.getAttribute('aria-checked'))).toEqual(['true', 'false', 'false']);
    expect(radios.map((r) => r.getAttribute('tabindex'))).toEqual(['0', '-1', '-1']);
  });

  it('selects with the arrow keys and clicks', () => {
    const onChange = vi.fn();
    render(<SegmentedDemo onChange={onChange} />);
    const group = screen.getByRole('radiogroup');
    fireEvent.keyDown(group, { key: 'ArrowRight' });
    expect(screen.getByRole('radio', { name: 'Treemap' })).toBeChecked();
    expect(screen.getByRole('radio', { name: 'Treemap' })).toHaveFocus();
    fireEvent.keyDown(group, { key: 'ArrowLeft' });
    fireEvent.keyDown(group, { key: 'ArrowLeft' });
    expect(screen.getByRole('radio', { name: 'List' })).toBeChecked();
    fireEvent.click(screen.getByRole('radio', { name: 'Table' }));
    expect(onChange).toHaveBeenLastCalledWith('table');
  });
});

describe('Select', () => {
  it('has a visible label and reports the chosen value', async () => {
    const onChange = vi.fn();
    render(
      <Select
        label="Branch"
        value="main"
        onChange={onChange}
        options={[
          { value: 'main', label: 'main' },
          { value: 'dev', label: 'develop' },
        ]}
      />,
    );
    const select = screen.getByRole('combobox', { name: 'Branch' });
    expect(select).toHaveTextContent('main');
    expect(screen.getByText('Branch')).toBeVisible();
    await chooseOption('Branch', 'develop');
    expect(onChange).toHaveBeenCalledWith('dev');
  });

  it('can hide the label visually but keep it for screen readers', () => {
    render(
      <Select
        label="Sort by"
        hideLabel
        value="a"
        onChange={() => {}}
        options={[{ value: 'a', label: 'A' }]}
      />,
    );
    expect(screen.getByLabelText('Sort by')).toBeInTheDocument();
    expect(screen.getByText('Sort by')).toHaveClass('sr-only');
  });
});

describe('SearchInput', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('reports once after the pause, not on every key', () => {
    const onChange = vi.fn();
    render(<SearchInput label="Search findings" onChange={onChange} delayMs={300} />);
    const box = screen.getByRole('searchbox', { name: 'Search findings' });
    fireEvent.change(box, { target: { value: 'o' } });
    fireEvent.change(box, { target: { value: 'op' } });
    fireEvent.change(box, { target: { value: 'openssl' } });
    act(() => {
      vi.advanceTimersByTime(299);
    });
    expect(onChange).not.toHaveBeenCalled();
    act(() => {
      vi.advanceTimersByTime(1);
    });
    expect(onChange).toHaveBeenCalledOnce();
    expect(onChange).toHaveBeenCalledWith('openssl');
  });

  it('reports at once on Enter', () => {
    const onChange = vi.fn();
    render(<SearchInput label="Search" onChange={onChange} />);
    const box = screen.getByRole('searchbox');
    fireEvent.change(box, { target: { value: 'CVE-2026-1' } });
    fireEvent.keyDown(box, { key: 'Enter' });
    expect(onChange).toHaveBeenCalledWith('CVE-2026-1');
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(onChange).toHaveBeenCalledOnce();
  });

  it('clears with the button and reports an empty string', () => {
    const onChange = vi.fn();
    render(<SearchInput label="Search" defaultValue="abc" onChange={onChange} />);
    fireEvent.click(screen.getByRole('button', { name: 'Clear search' }));
    expect(screen.getByRole('searchbox')).toHaveValue('');
    expect(onChange).toHaveBeenCalledWith('');
    expect(screen.queryByRole('button', { name: 'Clear search' })).toBeNull();
  });

  it('follows a reset from the parent', () => {
    const { rerender } = render(<SearchInput label="Search" value="abc" onChange={() => {}} />);
    expect(screen.getByRole('searchbox')).toHaveValue('abc');
    rerender(<SearchInput label="Search" value="" onChange={() => {}} />);
    expect(screen.getByRole('searchbox')).toHaveValue('');
  });
});

describe('FilterChips', () => {
  const options = [
    { value: 'critical', label: 'Critical', count: 12, severity: 'critical' as const },
    { value: 'high', label: 'High', count: 1204, severity: 'high' as const },
    { value: 'fixable', label: 'Fixable' },
  ];

  it('exposes toggles with pressed state and counts', () => {
    render(
      <FilterChips
        label="Filter findings"
        options={options}
        selected={['high']}
        onChange={() => {}}
      />,
    );
    expect(screen.getByRole('group', { name: 'Filter findings' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /High/ })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('button', { name: /High/ })).toHaveTextContent('1,204');
    expect(screen.getByRole('button', { name: /Critical/ })).toHaveAttribute(
      'aria-pressed',
      'false',
    );
  });

  it('adds and removes values', () => {
    const onChange = vi.fn();
    const { rerender } = render(
      <FilterChips label="Filter" options={options} selected={['high']} onChange={onChange} />,
    );
    fireEvent.click(screen.getByRole('button', { name: /Critical/ }));
    expect(onChange).toHaveBeenLastCalledWith(['high', 'critical']);
    fireEvent.click(screen.getByRole('button', { name: /High/ }));
    expect(onChange).toHaveBeenLastCalledWith([]);
    rerender(
      <FilterChips
        label="Filter"
        options={options}
        selected={['high']}
        onChange={onChange}
        single
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: /Critical/ }));
    expect(onChange).toHaveBeenLastCalledWith(['critical']);
  });
});

describe('Pagination', () => {
  it('shows progress and loads more', () => {
    const onLoadMore = vi.fn();
    render(<Pagination shown={50} total={1204} hasMore onLoadMore={onLoadMore} />);
    expect(screen.getByRole('status')).toHaveTextContent('Showing 50 of 1,204');
    fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
    expect(onLoadMore).toHaveBeenCalledOnce();
  });

  it('disables the button while loading', () => {
    render(<Pagination shown={50} hasMore loading onLoadMore={() => {}} />);
    expect(screen.getByRole('button', { name: 'Loading' })).toBeDisabled();
    expect(screen.getByRole('status')).toHaveTextContent('Showing 50');
  });

  it('drops the button when everything is shown', () => {
    render(<Pagination shown={1} hasMore={false} noun="finding" onLoadMore={() => {}} />);
    expect(screen.queryByRole('button')).toBeNull();
    expect(screen.getByRole('status')).toHaveTextContent('All 1 finding shown');
  });
});
