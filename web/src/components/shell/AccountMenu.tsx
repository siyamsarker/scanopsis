// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { ChevronsUpDown, Keyboard, LogOut, UserRound } from 'lucide-react';
import { useNavigate } from 'react-router';
import type { Me } from '../../api/client';
import { useTheme, type Theme } from '../../theme';
import {
  Menu,
  MenuContent,
  MenuItem,
  MenuLabel,
  MenuRadioGroup,
  MenuRadioItem,
  MenuSeparator,
  MenuTrigger,
} from '../Menu';
import { themeOptions } from '../ThemeSwitch';

export function displayName(user: Me): string {
  return user.name || user.username;
}

export function roleLabel(user: Me): string {
  return user.role === 'admin' ? 'Admin' : 'Viewer';
}

/** One or two letters for the avatar disc. */
export function initials(user: Me): string {
  const words = displayName(user).trim().split(/\s+/).filter(Boolean);
  const letters =
    words.length > 1 ? words[0][0] + words[words.length - 1][0] : words[0]?.slice(0, 2);
  return (letters || '?').toUpperCase();
}

export function Avatar({ user, className = '' }: { user: Me; className?: string }) {
  return (
    <span
      aria-hidden="true"
      className={`inline-flex size-8 shrink-0 items-center justify-center rounded-full border border-line bg-sunken text-caption font-semibold text-text ${className}`}
    >
      {initials(user)}
    </span>
  );
}

/**
 * The account menu at the foot of the sidebar: who is signed in, the account
 * page, the theme, the keyboard shortcuts and signing out.
 */
export function AccountMenu({
  user,
  collapsed = false,
  onShortcuts,
  onSignOut,
}: {
  user: Me;
  /** Rail mode: only the avatar shows; the name stays available to screen readers. */
  collapsed?: boolean;
  onShortcuts: () => void;
  onSignOut: () => void;
}) {
  const navigate = useNavigate();
  const [theme, setTheme] = useTheme();
  const name = displayName(user);

  return (
    <Menu>
      <MenuTrigger
        aria-label={`Account: ${name}, ${roleLabel(user)}`}
        className={`group flex min-h-12 w-full items-center gap-2.5 rounded-inner text-left transition-colors duration-micro hover:bg-wash data-[state=open]:bg-wash ${collapsed ? 'justify-center px-0' : 'px-2'}`}
      >
        <Avatar user={user} />
        {collapsed ? null : (
          <>
            <span className="min-w-0 flex-1">
              <span className="block truncate text-body-s font-medium text-text">{name}</span>
              <span className="block text-caption text-text-faint">{roleLabel(user)}</span>
            </span>
            <ChevronsUpDown
              size={16}
              strokeWidth={1.75}
              aria-hidden="true"
              className="shrink-0 text-text-faint"
            />
          </>
        )}
      </MenuTrigger>
      <MenuContent
        side={collapsed ? 'right' : 'top'}
        align={collapsed ? 'end' : 'start'}
        className="w-60"
      >
        <MenuLabel>
          Signed in as <span className="font-medium text-text-muted">{user.username}</span>
        </MenuLabel>
        <MenuItem
          icon={<UserRound size={16} strokeWidth={1.75} aria-hidden="true" />}
          onSelect={() => void navigate('/account')}
        >
          Your account
        </MenuItem>
        <MenuSeparator />
        <MenuLabel>Theme</MenuLabel>
        <MenuRadioGroup value={theme} onValueChange={(value) => setTheme(value as Theme)}>
          {themeOptions.map(({ value, label, Icon }) => (
            <MenuRadioItem
              key={value}
              value={value}
              icon={<Icon size={16} strokeWidth={1.75} aria-hidden="true" />}
            >
              {label}
            </MenuRadioItem>
          ))}
        </MenuRadioGroup>
        <MenuSeparator />
        <MenuItem
          icon={<Keyboard size={16} strokeWidth={1.75} aria-hidden="true" />}
          shortcut="?"
          onSelect={onShortcuts}
        >
          Keyboard shortcuts
        </MenuItem>
        <MenuItem
          icon={<LogOut size={16} strokeWidth={1.75} aria-hidden="true" />}
          onSelect={onSignOut}
        >
          Sign out
        </MenuItem>
      </MenuContent>
    </Menu>
  );
}
