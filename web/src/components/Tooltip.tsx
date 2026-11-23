// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import * as RadixTooltip from '@radix-ui/react-tooltip';
import type { ReactElement, ReactNode } from 'react';

/**
 * A short label shown on hover (after a moment) and at once on keyboard focus.
 * The child must be one focusable element that forwards refs (a button, a
 * link). Tooltips repeat information; never put the only copy of something
 * important in one, since touch screens cannot hover.
 */
export function Tooltip({
  content,
  children,
  side = 'top',
  align = 'center',
  delayMs = 300,
  open,
  onOpenChange,
}: {
  content: ReactNode;
  children: ReactElement;
  side?: 'top' | 'right' | 'bottom' | 'left';
  align?: 'start' | 'center' | 'end';
  delayMs?: number;
  /** Controlled open state, for showing a tooltip from code. */
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
}) {
  return (
    <RadixTooltip.Provider delayDuration={delayMs} skipDelayDuration={200}>
      <RadixTooltip.Root open={open} onOpenChange={onOpenChange}>
        <RadixTooltip.Trigger asChild>{children}</RadixTooltip.Trigger>
        <RadixTooltip.Portal>
          <RadixTooltip.Content
            side={side}
            align={align}
            sideOffset={6}
            collisionPadding={8}
            className="anim-pop z-50 max-w-64 origin-(--radix-tooltip-content-transform-origin) rounded-[8px] border border-line bg-surface-overlay px-2.5 py-1.5 text-caption text-text shadow-2"
          >
            {content}
          </RadixTooltip.Content>
        </RadixTooltip.Portal>
      </RadixTooltip.Root>
    </RadixTooltip.Provider>
  );
}
