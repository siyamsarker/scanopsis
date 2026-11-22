// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { PanelLeftClose, PanelLeftOpen, Search } from 'lucide-react';
import { useState } from 'react';
import { Link, NavLink } from 'react-router';
import type { Me } from '../../api/client';
import { iconButtonClass } from '../../ui';
import { ApertureMark, Wordmark } from '../ApertureMark';
import { Kbd } from '../inputs';
import { Tooltip } from '../Tooltip';
import { AccountMenu } from './AccountMenu';
import { navFor, type NavItem } from './nav';
import { isMac } from './shortcuts';

export const sidebarStorageKey = 'scanopsis.sidebar';

function readCollapsed(): boolean {
  try {
    return globalThis.localStorage?.getItem(sidebarStorageKey) === 'collapsed';
  } catch {
    return false;
  }
}

function saveCollapsed(collapsed: boolean) {
  try {
    if (collapsed) globalThis.localStorage?.setItem(sidebarStorageKey, 'collapsed');
    else globalThis.localStorage?.removeItem(sidebarStorageKey);
  } catch {
    // The choice still holds for this page view.
  }
}

/** Whether the sidebar is a narrow icon rail; remembered in this browser. */
export function useSidebarCollapsed(): [boolean, (next: boolean) => void] {
  const [collapsed, setCollapsed] = useState(readCollapsed);
  return [
    collapsed,
    (next: boolean) => {
      setCollapsed(next);
      saveCollapsed(next);
    },
  ];
}

const itemBase =
  'group relative flex min-h-9 items-center gap-3 rounded-inner text-body-s font-medium transition-[background-color,color] duration-micro ease-enter pointer-coarse:min-h-11 ' +
  // The active item: raised surface and a 2px lens-gradient indicator on the left edge.
  'before:absolute before:top-1/2 before:left-0 before:h-5 before:w-0.5 before:-translate-y-1/2 before:rounded-full before:bg-[image:var(--lens)] before:opacity-0 before:transition-opacity before:duration-micro ' +
  'aria-[current=page]:bg-surface-raised aria-[current=page]:text-text aria-[current=page]:shadow-[0_0_0_1px_var(--line),var(--elev-1)] aria-[current=page]:before:opacity-100';

function NavEntry({
  item,
  collapsed,
  onNavigate,
}: {
  item: NavItem;
  collapsed: boolean;
  onNavigate?: () => void;
}) {
  const link = (
    <NavLink
      to={item.to}
      end={item.end}
      onClick={onNavigate}
      className={`${itemBase} ${collapsed ? 'justify-center px-0' : 'px-3'} text-text-muted hover:bg-wash hover:text-text`}
    >
      <item.Icon
        size={18}
        strokeWidth={1.75}
        aria-hidden="true"
        className="shrink-0 text-text-faint transition-colors duration-micro group-hover:text-text-muted group-aria-[current=page]:text-brand"
      />
      <span className={collapsed ? 'sr-only' : 'min-w-0 truncate'}>{item.label}</span>
    </NavLink>
  );
  return (
    <li>
      {collapsed ? (
        <Tooltip content={item.label} side="right">
          {link}
        </Tooltip>
      ) : (
        link
      )}
    </li>
  );
}

/** The page links, grouped: Overview for everyone, Admin for administrators. */
export function NavList({
  isAdmin,
  collapsed = false,
  onNavigate,
}: {
  isAdmin: boolean;
  collapsed?: boolean;
  /** Called after a link is followed (closes the phone menu). */
  onNavigate?: () => void;
}) {
  return (
    <nav aria-label="Main" className="flex flex-col gap-5">
      {navFor(isAdmin).map((group) => (
        <div key={group.label}>
          <p
            className={
              collapsed ? 'sr-only' : 'mb-1.5 px-3 text-caption font-medium text-text-faint'
            }
          >
            {group.label}
          </p>
          {collapsed && group.admin ? (
            <span aria-hidden="true" className="mx-auto mb-2 block h-px w-6 bg-line" />
          ) : null}
          <ul className="flex flex-col gap-0.5">
            {group.items.map((item) => (
              <NavEntry key={item.to} item={item} collapsed={collapsed} onNavigate={onNavigate} />
            ))}
          </ul>
        </div>
      ))}
    </nav>
  );
}

/** The search-or-jump button that opens the command palette. */
export function PaletteButton({
  onOpen,
  collapsed = false,
}: {
  onOpen: () => void;
  collapsed?: boolean;
}) {
  const mod = isMac() ? '⌘' : 'Ctrl';
  if (collapsed) {
    return (
      <Tooltip content={`Search or jump to (${mod} K)`} side="right">
        <button
          type="button"
          aria-label="Search or jump to"
          aria-keyshortcuts="Meta+K Control+K"
          onClick={onOpen}
          className={`${iconButtonClass('ghost', 'md')} mx-auto`}
        >
          <Search size={18} strokeWidth={1.75} aria-hidden="true" />
        </button>
      </Tooltip>
    );
  }
  return (
    <button
      type="button"
      onClick={onOpen}
      aria-keyshortcuts="Meta+K Control+K"
      className="flex min-h-10 w-full items-center gap-2.5 rounded-inner border border-line bg-sunken px-3 text-left text-body-s text-text-faint transition-[border-color,color] duration-micro hover:border-line-strong hover:text-text-muted"
    >
      <Search size={16} strokeWidth={1.75} aria-hidden="true" />
      <span className="flex-1">Search or jump to</span>
      <span className="flex gap-0.5" aria-hidden="true">
        <Kbd>{mod}</Kbd>
        <Kbd>K</Kbd>
      </span>
    </button>
  );
}

/**
 * The desktop sidebar (from the md breakpoint): logo, the palette button, the
 * page links and the account menu. Collapses to a 64px icon rail.
 */
export function Sidebar({
  user,
  collapsed,
  onCollapsedChange,
  onPalette,
  onShortcuts,
  onSignOut,
}: {
  user: Me;
  collapsed: boolean;
  onCollapsedChange: (collapsed: boolean) => void;
  onPalette: () => void;
  onShortcuts: () => void;
  onSignOut: () => void;
}) {
  const toggleLabel = collapsed ? 'Expand sidebar' : 'Collapse sidebar';
  const ToggleIcon = collapsed ? PanelLeftOpen : PanelLeftClose;
  return (
    <aside
      aria-label="Sidebar"
      data-collapsed={collapsed || undefined}
      className={`sticky top-0 hidden h-dvh shrink-0 flex-col border-r border-line bg-surface transition-[width] duration-layout ease-enter md:flex ${collapsed ? 'w-16' : 'w-[232px]'}`}
    >
      <div
        className={`flex h-16 shrink-0 items-center ${collapsed ? 'flex-col justify-center gap-1 pt-3' : 'justify-between px-4'}`}
      >
        <Link to="/" aria-label="scanopsis" className="rounded-inner py-1 text-[1.0625rem]">
          {collapsed ? <ApertureMark size={24} className="text-brand" /> : <Wordmark size={24} />}
        </Link>
        {collapsed ? null : (
          <Tooltip content={toggleLabel} side="right">
            <button
              type="button"
              aria-label={toggleLabel}
              onClick={() => onCollapsedChange(!collapsed)}
              className={iconButtonClass('ghost', 'sm')}
            >
              <ToggleIcon size={18} strokeWidth={1.75} aria-hidden="true" />
            </button>
          </Tooltip>
        )}
      </div>
      <div className={collapsed ? 'mt-3 px-3' : 'px-3'}>
        <PaletteButton onOpen={onPalette} collapsed={collapsed} />
      </div>
      <div className={`mt-6 min-h-0 flex-1 overflow-y-auto pb-4 ${collapsed ? 'px-3' : 'px-3'}`}>
        <NavList isAdmin={user.role === 'admin'} collapsed={collapsed} />
      </div>
      <div className="flex shrink-0 flex-col gap-1 border-t border-line p-3">
        {collapsed ? (
          <Tooltip content={toggleLabel} side="right">
            <button
              type="button"
              aria-label={toggleLabel}
              onClick={() => onCollapsedChange(!collapsed)}
              className={`${iconButtonClass('ghost', 'md')} mx-auto`}
            >
              <ToggleIcon size={18} strokeWidth={1.75} aria-hidden="true" />
            </button>
          </Tooltip>
        ) : null}
        <AccountMenu
          user={user}
          collapsed={collapsed}
          onShortcuts={onShortcuts}
          onSignOut={onSignOut}
        />
      </div>
    </aside>
  );
}
