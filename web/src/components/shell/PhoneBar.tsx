// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { Keyboard, LogOut, Menu as MenuIcon, Search, UserRound } from 'lucide-react';
import { useState } from 'react';
import { Link } from 'react-router';
import type { Me } from '../../api/client';
import { buttonClass, iconButtonClass } from '../../ui';
import { Wordmark } from '../ApertureMark';
import { Sheet } from '../Dialog';
import { ThemeSwitch } from '../ThemeSwitch';
import { Avatar, displayName, roleLabel } from './AccountMenu';
import { NavList } from './Sidebar';

/**
 * The top bar on phones (below the md breakpoint): logo, a button for the
 * command palette and one for the menu, which opens a full-height sheet with
 * the page links, the theme and the account.
 */
export function PhoneBar({
  user,
  onPalette,
  onShortcuts,
  onSignOut,
}: {
  user: Me;
  onPalette: () => void;
  onShortcuts: () => void;
  onSignOut: () => void;
}) {
  const [open, setOpen] = useState(false);
  const close = () => setOpen(false);

  return (
    <header className="sticky top-0 z-30 flex h-14 items-center gap-2 border-b border-line bg-[color-mix(in_srgb,var(--surface)_88%,transparent)] px-3 pt-[env(safe-area-inset-top)] backdrop-blur-md md:hidden">
      <Link
        to="/"
        aria-label="scanopsis"
        className="mr-auto rounded-inner px-1 py-1 text-[1.0625rem]"
      >
        <Wordmark size={22} />
      </Link>
      <button
        type="button"
        aria-label="Search or jump to"
        onClick={onPalette}
        className={iconButtonClass('ghost', 'md')}
      >
        <Search size={20} strokeWidth={1.75} aria-hidden="true" />
      </button>
      <button
        type="button"
        aria-label="Open menu"
        aria-expanded={open}
        onClick={() => setOpen(true)}
        className={iconButtonClass('ghost', 'md')}
      >
        <MenuIcon size={20} strokeWidth={1.75} aria-hidden="true" />
      </button>

      <Sheet
        open={open}
        onOpenChange={setOpen}
        side="left"
        size="sm"
        title="Menu"
        hideTitle
        closeLabel="Close menu"
        header={<Wordmark size={22} className="text-[1.0625rem]" />}
        footer={
          <div className="flex w-full flex-col gap-3">
            <div className="flex items-center gap-3">
              <Avatar user={user} />
              <span className="min-w-0 flex-1">
                <span className="block truncate text-body-s font-medium text-text">
                  {displayName(user)}
                </span>
                <span className="block text-caption text-text-faint">{roleLabel(user)}</span>
              </span>
              <ThemeSwitch />
            </div>
            <div className="grid grid-cols-2 gap-2">
              <Link to="/account" onClick={close} className={buttonClass('secondary')}>
                <UserRound size={16} strokeWidth={1.75} aria-hidden="true" />
                Your account
              </Link>
              <button
                type="button"
                className={buttonClass('secondary')}
                onClick={() => {
                  close();
                  onSignOut();
                }}
              >
                <LogOut size={16} strokeWidth={1.75} aria-hidden="true" />
                Sign out
              </button>
            </div>
          </div>
        }
      >
        <NavList isAdmin={user.role === 'admin'} onNavigate={close} />
        <button
          type="button"
          onClick={() => {
            close();
            onShortcuts();
          }}
          className={`${buttonClass('ghost', 'sm')} mt-6 hidden pointer-fine:inline-flex`}
        >
          <Keyboard size={16} strokeWidth={1.75} aria-hidden="true" />
          Keyboard shortcuts
        </button>
      </Sheet>
    </header>
  );
}
