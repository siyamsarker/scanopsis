// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import * as RadixPopover from '@radix-ui/react-popover';
import type { ComponentPropsWithoutRef } from 'react';

/*
 * Popovers: interactive content anchored to a control (a filter form, a legend
 * with links). Rendered in a portal, kept within the viewport; Escape or a
 * click outside closes and focus returns to the trigger.
 */

export const Popover = RadixPopover.Root;
export const PopoverTrigger = RadixPopover.Trigger;
export const PopoverAnchor = RadixPopover.Anchor;
export const PopoverClose = RadixPopover.Close;

export function PopoverContent({
  children,
  align = 'start',
  sideOffset = 8,
  className = '',
  ...rest
}: ComponentPropsWithoutRef<typeof RadixPopover.Content>) {
  return (
    <RadixPopover.Portal>
      <RadixPopover.Content
        align={align}
        sideOffset={sideOffset}
        collisionPadding={12}
        className={`raised anim-pop z-50 max-h-(--radix-popover-content-available-height) w-72 max-w-[calc(100vw-1.5rem)] origin-(--radix-popover-content-transform-origin) overflow-y-auto rounded-panel p-4 text-body-s text-text ${className}`}
        {...rest}
      >
        {children}
      </RadixPopover.Content>
    </RadixPopover.Portal>
  );
}
