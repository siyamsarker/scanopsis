// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useLayoutEffect, type RefObject } from 'react';

/**
 * Applies computed styles to an element through the DOM (CSSOM), never through
 * a `style` attribute in markup. The Content Security Policy blocks inline
 * style attributes but allows scripts to set `element.style` properties, which
 * is how data-driven sizes (bar segment widths, virtual row offsets) are done.
 * Runs in a layout effect so the first paint already has the final geometry.
 */
export function useCssom<T extends HTMLElement>(
  ref: RefObject<T | null>,
  styles: Partial<
    Record<'flexGrow' | 'flexBasis' | 'height' | 'width' | 'transform', string | number>
  >,
) {
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    for (const [name, value] of Object.entries(styles)) {
      el.style[name as 'height'] = String(value);
    }
  });
}
