// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

// @vitest-environment node
import { readFileSync } from 'node:fs';

const css = readFileSync(new URL('./index.css', import.meta.url), 'utf8');

const themed = [
  'brand',
  'bg',
  'surface',
  'surface-raised',
  'border',
  'border-strong',
  'text',
  'text-muted',
];
const shared = [
  'sev-critical',
  'sev-high',
  'sev-medium',
  'sev-low',
  'sev-unknown',
  'pass',
  'fail',
  'warn',
];

// The default (dark) block is the first `:root` rule. Light tokens appear twice:
// for the system preference when no theme is chosen, and for data-theme="light".
const dark = /^:root\s*\{([^}]*)\}/m.exec(css)?.[1] ?? '';
const light =
  /@media \(prefers-color-scheme: light\)\s*\{\s*:root:not\(\[data-theme\]\)\s*\{([^}]*)\}/.exec(
    css,
  )?.[1] ?? '';
const lightExplicit = /:root\[data-theme='light'\]\s*\{([^}]*)\}/.exec(css)?.[1] ?? '';

function declarations(block: string) {
  return block
    .split(';')
    .map((d) => d.trim())
    .filter((d) => d.startsWith('--'))
    .sort();
}

describe('design tokens', () => {
  it.each(themed)('defines %s for dark and light themes', (name) => {
    expect(dark).toContain(`--${name}:`);
    expect(light).toContain(`--${name}:`);
  });

  it.each(shared)('defines %s for both themes', (name) => {
    expect(dark).toContain(`--${name}:`);
  });

  it.each([...themed, ...shared])('exposes %s to Tailwind', (name) => {
    expect(css).toContain(`--color-${name}: var(--${name});`);
  });

  it('gives data-theme="light" the same tokens as the light system preference', () => {
    expect(declarations(lightExplicit)).toEqual(declarations(light));
    expect(declarations(light).length).toBe(themed.length);
  });

  it('applies the system preference only when no theme is chosen', () => {
    expect(css).toMatch(
      /@media \(prefers-color-scheme: light\)\s*\{\s*:root:not\(\[data-theme\]\)/,
    );
  });
});
