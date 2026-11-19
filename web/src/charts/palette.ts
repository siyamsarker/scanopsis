// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useEffect, useState } from 'react';
import type { SeverityKey } from '../components/severityModel';

/**
 * The design tokens resolved to concrete colours. Canvas charts cannot use CSS
 * variables, so the values are read from the document at run time and the chart
 * is redrawn when the theme changes.
 */
export interface Palette {
  brand: string;
  bg: string;
  surface: string;
  surfaceRaised: string;
  border: string;
  borderStrong: string;
  text: string;
  textMuted: string;
  sev: Record<SeverityKey, string>;
  pass: string;
  fail: string;
  warn: string;
}

/** Dark defaults, used where a token cannot be read (tests, detached documents). */
const fallback: Palette = {
  brand: '#7c83ff',
  bg: '#0b1020',
  surface: '#131a2e',
  surfaceRaised: '#1b2440',
  border: '#273152',
  borderStrong: '#6b769c',
  text: '#e6eaf2',
  textMuted: '#9aa4bf',
  sev: {
    critical: '#e5484d',
    high: '#f76b15',
    medium: '#f5b700',
    low: '#3e9bff',
    unknown: '#8b93a7',
  },
  pass: '#30a46c',
  fail: '#e5484d',
  warn: '#f5b700',
};

/** Reads the palette from the computed custom properties of `root` (the page by default). */
export function readPalette(root: Element = document.documentElement): Palette {
  const style = getComputedStyle(root);
  const token = (name: string, fb: string) => style.getPropertyValue(`--${name}`).trim() || fb;
  return {
    brand: token('brand', fallback.brand),
    bg: token('bg', fallback.bg),
    surface: token('surface', fallback.surface),
    surfaceRaised: token('surface-raised', fallback.surfaceRaised),
    border: token('border', fallback.border),
    borderStrong: token('border-strong', fallback.borderStrong),
    text: token('text', fallback.text),
    textMuted: token('text-muted', fallback.textMuted),
    sev: {
      critical: token('sev-critical', fallback.sev.critical),
      high: token('sev-high', fallback.sev.high),
      medium: token('sev-medium', fallback.sev.medium),
      low: token('sev-low', fallback.sev.low),
      unknown: token('sev-unknown', fallback.sev.unknown),
    },
    pass: token('pass', fallback.pass),
    fail: token('fail', fallback.fail),
    warn: token('warn', fallback.warn),
  };
}

function parseHex(color: string): [number, number, number] | null {
  const match = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(color.trim());
  if (!match) return null;
  const hex =
    match[1].length === 3
      ? match[1]
          .split('')
          .map((c) => c + c)
          .join('')
      : match[1];
  return [0, 2, 4].map((i) => parseInt(hex.slice(i, i + 2), 16)) as [number, number, number];
}

/**
 * Mixes `color` over `base` with `amount` of `color` (0 to 1), in sRGB, like CSS
 * color-mix. Used for treemap tile fills so label text keeps its contrast.
 * Returns `color` unchanged when either value is not a hex colour.
 */
export function mix(color: string, base: string, amount: number): string {
  const a = parseHex(color);
  const b = parseHex(base);
  if (!a || !b) return color;
  const channel = (i: number) => Math.round(a[i] * amount + b[i] * (1 - amount));
  return `#${[0, 1, 2].map((i) => channel(i).toString(16).padStart(2, '0')).join('')}`;
}

/**
 * The current palette, re-read when the theme changes: the `data-theme`
 * attribute on <html> (the Light/Dark/System switch) or the system colour scheme.
 */
export function usePalette(): Palette {
  const [palette, setPalette] = useState(() => readPalette());
  useEffect(() => {
    const update = () => setPalette(readPalette());
    const observer = new MutationObserver(update);
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['data-theme'],
    });
    const query =
      typeof window.matchMedia === 'function'
        ? window.matchMedia('(prefers-color-scheme: light)')
        : null;
    query?.addEventListener('change', update);
    update();
    return () => {
      observer.disconnect();
      query?.removeEventListener('change', update);
    };
  }, []);
  return palette;
}
