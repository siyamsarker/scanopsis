// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useRef, type KeyboardEvent, type ReactNode } from 'react';
import { formatInt } from '../format';
import { nextIndex } from './roving';

export interface TabItem {
  id: string;
  label: ReactNode;
  /** A number after the label, such as the findings in that tab. */
  count?: number;
}

const tabId = (base: string, id: string) => `${base}-tab-${id}`;
const panelId = (base: string, id: string) => `${base}-panel-${id}`;

/**
 * A tab list with a roving tabindex: Tab enters the selected tab, the arrow
 * keys, Home and End move and select (automatic activation). Give the same
 * `idBase` to the TabPanels so tabs and panels reference each other.
 */
export function Tabs({
  tabs,
  value,
  onChange,
  label,
  idBase,
  className = '',
}: {
  tabs: TabItem[];
  value: string;
  onChange: (id: string) => void;
  /** Names the tab list for screen readers: "Service sections". */
  label: string;
  idBase: string;
  className?: string;
}) {
  const refs = useRef<(HTMLButtonElement | null)[]>([]);
  const current = Math.max(
    0,
    tabs.findIndex((tab) => tab.id === value),
  );

  function onKeyDown(event: KeyboardEvent) {
    const next = nextIndex(event, current, tabs.length);
    if (next === null) return;
    event.preventDefault();
    onChange(tabs[next].id);
    refs.current[next]?.focus();
  }

  return (
    <div
      role="tablist"
      aria-label={label}
      onKeyDown={onKeyDown}
      className={`flex gap-1 overflow-x-auto border-b border-border ${className}`}
    >
      {tabs.map((tab, index) => {
        const selected = index === current;
        return (
          <button
            key={tab.id}
            ref={(el) => {
              refs.current[index] = el;
            }}
            id={tabId(idBase, tab.id)}
            type="button"
            role="tab"
            data-roving
            aria-selected={selected}
            aria-controls={panelId(idBase, tab.id)}
            tabIndex={selected ? 0 : -1}
            onClick={() => onChange(tab.id)}
            className={`-mb-px inline-flex min-h-10 shrink-0 items-center gap-2 border-b-2 px-3 font-medium transition-colors duration-150 ease-out ${
              selected
                ? 'border-brand text-text'
                : 'border-transparent text-text-muted hover:border-border-strong hover:text-text'
            }`}
          >
            {tab.label}
            {tab.count !== undefined ? (
              <span className="rounded-full bg-border px-1.5 text-sm tabular-nums text-text">
                {formatInt(tab.count)}
              </span>
            ) : null}
          </button>
        );
      })}
    </div>
  );
}

/** The content of one tab. Renders only while its tab is selected. */
export function TabPanel({
  idBase,
  tab,
  value,
  children,
  className = '',
}: {
  idBase: string;
  tab: string;
  /** The selected tab id, the same value given to Tabs. */
  value: string;
  children: ReactNode;
  className?: string;
}) {
  if (tab !== value) return null;
  return (
    <div
      role="tabpanel"
      id={panelId(idBase, tab)}
      aria-labelledby={tabId(idBase, tab)}
      tabIndex={0}
      className={`pt-6 ${className}`}
    >
      {children}
    </div>
  );
}
