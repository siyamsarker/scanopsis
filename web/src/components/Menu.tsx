// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import * as RadixMenu from '@radix-ui/react-dropdown-menu';
import { Check, MoreHorizontal } from 'lucide-react';
import type { ComponentPropsWithoutRef, ReactNode } from 'react';
import { iconButtonClass } from '../ui';

/*
 * Dropdown menus for row actions and the account menu. Arrow keys move, typing
 * jumps to an item, Enter or Space selects, Escape closes and returns focus to
 * the trigger. Rendered in a portal.
 */

export const Menu = RadixMenu.Root;
export const MenuTrigger = RadixMenu.Trigger;
export const MenuGroup = RadixMenu.Group;
export const MenuRadioGroup = RadixMenu.RadioGroup;

export function MenuContent({
  children,
  align = 'end',
  side = 'bottom',
  sideOffset = 6,
  className = '',
  ...rest
}: ComponentPropsWithoutRef<typeof RadixMenu.Content>) {
  return (
    <RadixMenu.Portal>
      <RadixMenu.Content
        align={align}
        side={side}
        sideOffset={sideOffset}
        collisionPadding={8}
        className={`raised anim-pop z-50 max-h-(--radix-dropdown-menu-content-available-height) min-w-48 origin-(--radix-dropdown-menu-content-transform-origin) overflow-y-auto p-1 ${className}`}
        {...rest}
      >
        {children}
      </RadixMenu.Content>
    </RadixMenu.Portal>
  );
}

const itemBase =
  'relative flex min-h-9 cursor-default items-center gap-2.5 rounded-[7px] px-2.5 text-body-s outline-none select-none ' +
  'data-highlighted:bg-wash data-disabled:pointer-events-none data-disabled:opacity-50 ' +
  'pointer-coarse:min-h-11 [&_svg]:shrink-0';
const itemClass = `${itemBase} text-text [&_svg]:text-text-muted`;
const dangerItemClass = `${itemBase} tone-fail tone-ink [&_svg]:text-fail`;

export function MenuItem({
  children,
  icon,
  tone = 'default',
  shortcut,
  className = '',
  ...rest
}: ComponentPropsWithoutRef<typeof RadixMenu.Item> & {
  /** A 16px icon before the label. */
  icon?: ReactNode;
  /** "danger" for destructive items: critical text and icon. */
  tone?: 'default' | 'danger';
  /** A keyboard hint at the end of the row ("⌘K"). */
  shortcut?: string;
}) {
  return (
    <RadixMenu.Item
      className={`${tone === 'danger' ? dangerItemClass : itemClass} ${className}`}
      {...rest}
    >
      {icon}
      <span className="min-w-0 flex-1 truncate">{children}</span>
      {shortcut ? <kbd className="font-sans text-caption text-text-faint">{shortcut}</kbd> : null}
    </RadixMenu.Item>
  );
}

export function MenuRadioItem({
  children,
  icon,
  className = '',
  ...rest
}: ComponentPropsWithoutRef<typeof RadixMenu.RadioItem> & { icon?: ReactNode }) {
  return (
    <RadixMenu.RadioItem className={`${itemClass} ${className}`} {...rest}>
      {icon}
      <span className="min-w-0 flex-1 truncate">{children}</span>
      <RadixMenu.ItemIndicator>
        <Check size={16} strokeWidth={2} aria-hidden="true" className="text-brand!" />
      </RadixMenu.ItemIndicator>
    </RadixMenu.RadioItem>
  );
}

export function MenuCheckboxItem({
  children,
  className = '',
  ...rest
}: ComponentPropsWithoutRef<typeof RadixMenu.CheckboxItem>) {
  return (
    <RadixMenu.CheckboxItem className={`${itemClass} ${className}`} {...rest}>
      <span className="min-w-0 flex-1 truncate">{children}</span>
      <RadixMenu.ItemIndicator>
        <Check size={16} strokeWidth={2} aria-hidden="true" className="text-brand!" />
      </RadixMenu.ItemIndicator>
    </RadixMenu.CheckboxItem>
  );
}

export function MenuLabel({ children }: { children: ReactNode }) {
  return (
    <RadixMenu.Label className="px-2.5 pt-2 pb-1 text-caption font-medium text-text-faint">
      {children}
    </RadixMenu.Label>
  );
}

export function MenuSeparator() {
  return <RadixMenu.Separator className="mx-1 my-1 h-px bg-line" />;
}

/**
 * The common case: a "more" button with a list of actions for one row.
 * `label` names the trigger for screen readers ("Actions for payments-api").
 */
export function RowActions({ label, children }: { label: string; children: ReactNode }) {
  return (
    <Menu>
      <MenuTrigger className={iconButtonClass('ghost', 'sm')} aria-label={label}>
        <MoreHorizontal size={18} strokeWidth={1.75} aria-hidden="true" />
      </MenuTrigger>
      <MenuContent>{children}</MenuContent>
    </Menu>
  );
}
