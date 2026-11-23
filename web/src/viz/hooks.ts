// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  useSyncExternalStore,
  type RefObject,
} from 'react';

const REDUCED_QUERY = '(prefers-reduced-motion: reduce)';

function subscribeReduced(onChange: () => void) {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return () => {};
  const query = window.matchMedia(REDUCED_QUERY);
  query.addEventListener?.('change', onChange);
  return () => query.removeEventListener?.('change', onChange);
}

function readReduced() {
  return (
    typeof window !== 'undefined' &&
    typeof window.matchMedia === 'function' &&
    window.matchMedia(REDUCED_QUERY).matches
  );
}

/** True when the user asked the system for less motion. Follows changes live. */
export function useReducedMotion(): boolean {
  return useSyncExternalStore(subscribeReduced, readReduced, () => false);
}

/**
 * The content width of an element, kept current with a ResizeObserver. Pass
 * the returned callback as the element's ref. Until the element has been
 * measured (and where there is no layout, such as unit tests) the width is
 * `fallback`.
 */
export function useWidth<T extends HTMLElement>(
  fallback: number,
): [(el: T | null) => void, number] {
  const [el, setEl] = useState<T | null>(null);
  const [width, setWidth] = useState(0);
  useLayoutEffect(() => {
    if (!el) return;
    const measure = () => {
      const next = Math.floor(el.getBoundingClientRect().width);
      setWidth((prev) => (prev === next ? prev : next));
    };
    measure();
    if (typeof ResizeObserver !== 'function') return;
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, [el]);
  return [setEl, width > 0 ? width : fallback];
}

type StyleName = 'left' | 'top' | 'width' | 'height' | 'maxHeight' | 'transform' | 'flexGrow';

/**
 * Applies data-driven geometry through the CSSOM, never as a style attribute in
 * markup: the Content Security Policy blocks inline style attributes but allows
 * scripts to set element.style. Runs before paint.
 */
export function useStyles<T extends HTMLElement>(
  ref: RefObject<T | null>,
  styles: Partial<Record<StyleName, string | number | null | undefined>>,
) {
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    for (const [name, value] of Object.entries(styles)) {
      el.style[name as StyleName] = value == null ? '' : String(value);
    }
  });
}

const EASE_OUT = (t: number) => 1 - (1 - t) ** 3;

/**
 * A number that counts to its new value when it changes (320 ms), and is shown
 * as is on the first render and when motion is reduced.
 */
export function useCountUp(value: number, reduced: boolean, duration = 320): number {
  const [shown, setShown] = useState(value);
  const from = useRef(value);
  useEffect(() => {
    const start = from.current;
    from.current = value;
    if (reduced || start === value || typeof requestAnimationFrame !== 'function') {
      setShown(value);
      return;
    }
    let frame = 0;
    const began = performance.now();
    const step = (now: number) => {
      const t = Math.min(1, (now - began) / duration);
      setShown(Math.round(start + (value - start) * EASE_OUT(t)));
      if (t < 1) frame = requestAnimationFrame(step);
    };
    frame = requestAnimationFrame(step);
    return () => cancelAnimationFrame(frame);
  }, [value, reduced, duration]);
  return shown;
}

/** Index of the active mark for roving focus: arrows move, Home/End jump. */
export function nextIndex(key: string, current: number, count: number): number | null {
  if (count <= 0) return null;
  switch (key) {
    case 'ArrowRight':
    case 'ArrowDown':
      return Math.min(count - 1, current + 1);
    case 'ArrowLeft':
    case 'ArrowUp':
      return Math.max(0, current - 1);
    case 'Home':
      return 0;
    case 'End':
      return count - 1;
    case 'PageDown':
      return Math.min(count - 1, current + 10);
    case 'PageUp':
      return Math.max(0, current - 10);
    default:
      return null;
  }
}
