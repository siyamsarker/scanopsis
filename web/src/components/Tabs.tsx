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
      className={`flex gap-1 overflow-x-auto border-b border-line [scrollbar-width:none] ${className}`}
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
            className={`relative -mb-px inline-flex min-h-11 shrink-0 items-center gap-2 rounded-t-inner px-3 text-body-s font-medium transition-colors duration-micro ease-enter after:absolute after:inset-x-2 after:bottom-0 after:h-0.5 after:rounded-full after:transition-colors after:duration-micro ${
              selected
                ? 'text-text after:bg-brand'
                : 'text-text-muted after:bg-transparent hover:bg-wash hover:text-text hover:after:bg-line-strong'
            }`}
          >
            {tab.label}
            {tab.count !== undefined ? (
              <span
                className={`rounded-full px-1.5 text-caption font-medium tabular-nums ${selected ? 'bg-[color-mix(in_srgb,var(--brand)_16%,transparent)] text-text' : 'bg-wash text-text-muted'}`}
              >
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
