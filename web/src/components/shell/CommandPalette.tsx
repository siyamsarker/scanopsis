// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import * as RadixDialog from '@radix-ui/react-dialog';
import { Command } from 'cmdk';
import {
  Boxes,
  CornerDownLeft,
  Keyboard,
  LogOut,
  Monitor,
  Moon,
  Search,
  ShieldAlert,
  Sun,
  UserRound,
  type LucideIcon,
} from 'lucide-react';
import { useState, type ReactNode } from 'react';
import { useNavigate } from 'react-router';
import { usePortfolio } from '../../api/portfolio';
import { useTheme, type Theme } from '../../theme';
import { useReturnFocus } from '../Dialog';
import { Kbd } from '../inputs';
import { navFor, vulnerabilityId } from './nav';
import { paletteFilter } from './paletteFilter';

const itemClass =
  'flex min-h-10 cursor-default items-center gap-3 rounded-[8px] px-3 text-body-s text-text select-none ' +
  'data-[selected=true]:bg-wash data-[disabled=true]:opacity-50 pointer-coarse:min-h-11 [&_svg]:shrink-0';

const groupClass =
  '[&_[cmdk-group-heading]]:px-3 [&_[cmdk-group-heading]]:pt-3 [&_[cmdk-group-heading]]:pb-1.5 [&_[cmdk-group-heading]]:text-caption [&_[cmdk-group-heading]]:font-medium [&_[cmdk-group-heading]]:text-text-faint';

function Item({
  value,
  keywords,
  icon: Icon,
  children,
  hint,
  onSelect,
  forceMount,
}: {
  value: string;
  keywords?: string[];
  icon: LucideIcon;
  children: ReactNode;
  hint?: ReactNode;
  onSelect: () => void;
  forceMount?: boolean;
}) {
  return (
    <Command.Item
      value={value}
      keywords={keywords}
      onSelect={onSelect}
      forceMount={forceMount}
      className={itemClass}
    >
      <Icon size={16} strokeWidth={1.75} aria-hidden="true" className="text-text-muted" />
      <span className="min-w-0 flex-1 truncate">{children}</span>
      {hint ? <span className="truncate text-caption text-text-faint">{hint}</span> : null}
    </Command.Item>
  );
}

/** Services from the portfolio, loaded when the palette first opens. */
function ServiceItems({ run }: { run: (to: string) => void }) {
  const portfolio = usePortfolio();
  const items = portfolio.data?.items ?? [];
  if (portfolio.isPending) {
    return <Command.Loading>Loading services…</Command.Loading>;
  }
  if (items.length === 0) return null;
  return (
    <Command.Group heading="Services" className={groupClass}>
      {items.map(({ service }) => (
        <Item
          key={service.id}
          value={`service:${service.id}`}
          keywords={[service.name, service.gitlab_project_path]}
          icon={Boxes}
          hint={<span className="font-mono">{service.gitlab_project_path}</span>}
          onSelect={() => run(`/services/${service.id}`)}
        >
          {service.name}
        </Item>
      ))}
    </Command.Group>
  );
}

const themeItems: { value: Theme; label: string; Icon: LucideIcon }[] = [
  { value: 'dark', label: 'Use the dark theme', Icon: Moon },
  { value: 'light', label: 'Use the light theme', Icon: Sun },
  { value: 'system', label: 'Use the system theme', Icon: Monitor },
];

/**
 * The command palette (Cmd/Ctrl+K): jump to a page or a service, search a
 * vulnerability by its identifier, switch the theme, sign out. Typing filters;
 * arrow keys move; Enter runs; Escape closes and returns focus.
 */
export default function CommandPalette({
  open,
  onOpenChange,
  isAdmin,
  onShortcuts,
  onSignOut,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  isAdmin: boolean;
  onShortcuts: () => void;
  onSignOut: () => void;
}) {
  const navigate = useNavigate();
  const [, setTheme] = useTheme();
  const [query, setQuery] = useState('');
  const vulnId = vulnerabilityId(query);
  const focus = useReturnFocus();

  function close() {
    onOpenChange(false);
    setQuery('');
  }

  function run(to: string) {
    close();
    void navigate(to);
  }

  return (
    <RadixDialog.Root
      open={open}
      onOpenChange={(next) => {
        if (!next) setQuery('');
        onOpenChange(next);
      }}
    >
      <RadixDialog.Portal>
        <RadixDialog.Overlay className="anim-fade fixed inset-0 z-40 bg-(--scrim)" />
        <RadixDialog.Content
          aria-describedby={undefined}
          onOpenAutoFocus={focus.onOpenAutoFocus}
          onCloseAutoFocus={focus.onCloseAutoFocus}
          className="overlay anim-pop fixed inset-x-3 top-3 z-50 mx-auto flex max-h-[min(34rem,calc(100dvh-1.5rem))] max-w-[640px] flex-col overflow-hidden rounded-panel sm:top-[12vh]"
        >
          <RadixDialog.Title className="sr-only">Command palette</RadixDialog.Title>
          <Command
            label="Command palette"
            loop
            filter={paletteFilter}
            className="flex min-h-0 flex-col"
          >
            <div className="flex items-center gap-3 border-b border-line px-4">
              <Search size={18} strokeWidth={1.75} aria-hidden="true" className="text-text-muted" />
              <Command.Input
                value={query}
                onValueChange={setQuery}
                placeholder="Go to a service, page or CVE"
                className="min-h-14 w-full bg-transparent text-body text-text outline-none placeholder:text-text-faint focus-visible:outline-none"
              />
              <Kbd className="hidden sm:inline-flex">Esc</Kbd>
            </div>
            <Command.List className="min-h-0 overflow-y-auto overscroll-contain p-2 [&_[cmdk-list-sizer]]:flex [&_[cmdk-list-sizer]]:flex-col">
              {/* A vulnerability ID always offers its search, so it never reads as no match. */}
              {vulnId ? null : (
                <Command.Empty className="px-3 py-8 text-center text-body-s text-text-muted">
                  Nothing matches “{query}”.
                </Command.Empty>
              )}
              {vulnId ? (
                <Command.Group heading="Vulnerability" forceMount className={groupClass}>
                  <Item
                    value={`vulnerability:${vulnId}`}
                    icon={ShieldAlert}
                    forceMount
                    hint={<CornerDownLeft size={14} strokeWidth={1.75} aria-hidden="true" />}
                    onSelect={() => run(`/search?id=${encodeURIComponent(vulnId)}`)}
                  >
                    Search vulnerability <span className="font-mono">{vulnId}</span>
                  </Item>
                </Command.Group>
              ) : null}
              {navFor(isAdmin).map((group) => (
                <Command.Group
                  key={group.label}
                  heading={group.admin ? 'Admin' : 'Pages'}
                  className={groupClass}
                >
                  {group.items.map((item) => (
                    <Item
                      key={item.to}
                      value={`page:${item.to}`}
                      keywords={[item.label, ...(item.keywords ?? [])]}
                      icon={item.Icon}
                      onSelect={() => run(item.to)}
                    >
                      {item.label}
                    </Item>
                  ))}
                </Command.Group>
              ))}
              {open ? <ServiceItems run={run} /> : null}
              <Command.Group heading="Theme" className={groupClass}>
                {themeItems.map(({ value, label, Icon }) => (
                  <Item
                    key={value}
                    value={label}
                    keywords={['theme', 'appearance', value]}
                    icon={Icon}
                    onSelect={() => {
                      setTheme(value);
                      close();
                    }}
                  >
                    {label}
                  </Item>
                ))}
              </Command.Group>
              <Command.Group heading="Account" className={groupClass}>
                <Item
                  value="Your account"
                  keywords={['profile', 'sessions', 'password']}
                  icon={UserRound}
                  onSelect={() => run('/account')}
                >
                  Your account
                </Item>
                <Item
                  value="Keyboard shortcuts"
                  keywords={['keys', 'help']}
                  icon={Keyboard}
                  onSelect={() => {
                    close();
                    onShortcuts();
                  }}
                >
                  Keyboard shortcuts
                </Item>
                <Item
                  value="Sign out"
                  keywords={['log out', 'logout']}
                  icon={LogOut}
                  onSelect={() => {
                    close();
                    onSignOut();
                  }}
                >
                  Sign out
                </Item>
              </Command.Group>
            </Command.List>
          </Command>
        </RadixDialog.Content>
      </RadixDialog.Portal>
    </RadixDialog.Root>
  );
}
