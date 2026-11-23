// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import type { KeyboardEvent } from 'react';

/**
 * Keyboard handling shared by Tabs and Segmented (roving tabindex). Returns the
 * index to move to for Arrow, Home and End keys, or null for other keys.
 * Movement wraps around at both ends.
 */
export function nextIndex(
  event: Pick<KeyboardEvent, 'key'>,
  current: number,
  count: number,
  orientation: 'horizontal' | 'vertical' = 'horizontal',
): number | null {
  if (count === 0) return null;
  const forward = orientation === 'horizontal' ? 'ArrowRight' : 'ArrowDown';
  const backward = orientation === 'horizontal' ? 'ArrowLeft' : 'ArrowUp';
  switch (event.key) {
    case forward:
      return (current + 1) % count;
    case backward:
      return (current - 1 + count) % count;
    case 'Home':
      return 0;
    case 'End':
      return count - 1;
    default:
      return null;
  }
}
