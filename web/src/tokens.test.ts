// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

// @vitest-environment node
import { readFileSync } from 'node:fs';
import { pairs, themes } from '../scripts/contrast.mjs';

const css = readFileSync(new URL('./index.css', import.meta.url), 'utf8');

const themed = [
  'bg',
  'surface',
  'surface-raised',
  'surface-overlay',
  'sunken',
  'line',
  'line-strong',
  'text',
  'text-muted',
  'text-faint',
  'lens-a',
  'lens-b',
  'brand',
  'brand-ink',
  'sev-critical',
  'sev-high',
  'sev-medium',
  'sev-low',
  'sev-unknown',
  'ok',
  'fail',
  'warn',
  'fail-ink',
];

// The default (dark) block comes first and also themes data-theme-scope="dark".
// Light tokens appear twice: for the system preference when no theme is
// chosen, and for data-theme="light" (which also themes data-theme-scope="light").
const dark = /:root,\s*\[data-theme-scope='dark'\]\s*\{([^}]*)\}/.exec(css)?.[1] ?? '';
const light =
  /@media \(prefers-color-scheme: light\)\s*\{\s*:root:not\(\[data-theme\]\)\s*\{([^}]*)\}/.exec(
    css,
  )?.[1] ?? '';
const lightExplicit =
  /:root\[data-theme='light'\],\s*\[data-theme-scope='light'\]\s*\{([^}]*)\}/.exec(css)?.[1] ?? '';

function declarations(block: string) {
  return block
    .split(';')
    .map((d) => d.trim())
    .filter((d) => d.startsWith('--'))
    .sort();
}

interface Pair {
  label: string;
  ratio: number;
  min: number;
}

describe('design tokens', () => {
  it.each(themed)('defines %s for dark and light themes', (name) => {
    expect(dark).toContain(`--${name}:`);
    expect(light).toContain(`--${name}:`);
  });

  it.each(themed)('exposes %s to Tailwind', (name) => {
    expect(css).toContain(`--color-${name}: var(--${name});`);
  });

  it('gives data-theme="light" the same tokens as the light system preference', () => {
    expect(declarations(lightExplicit)).toEqual(declarations(light));
    expect(declarations(dark).map((d) => d.split(':')[0])).toEqual(
      declarations(light).map((d) => d.split(':')[0]),
    );
  });

  it('applies the system preference only when no theme is chosen', () => {
    expect(css).toMatch(
      /@media \(prefers-color-scheme: light\)\s*\{\s*:root:not\(\[data-theme\]\)/,
    );
  });

  it('sets the root font size to 16px and turns motion off on request', () => {
    expect(css).toMatch(/html\s*\{[^}]*font-size:\s*16px/);
    expect(css).toMatch(/@media \(prefers-reduced-motion: reduce\)/);
  });

  describe.each(['dark', 'light'])('contrast in the %s theme', (theme) => {
    const palette = (themes(css) as Record<string, unknown>)[theme];
    it.each((pairs(palette) as Pair[]).map((p) => [p.label, p] as const))('%s', (_label, pair) => {
      expect(pair.ratio).toBeGreaterThanOrEqual(pair.min);
    });
  });
});
