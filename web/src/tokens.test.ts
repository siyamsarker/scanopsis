// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

// @vitest-environment node
import { readFileSync } from 'node:fs';

const css = readFileSync(new URL('./index.css', import.meta.url), 'utf8');

const themed = ['brand', 'bg', 'surface', 'surface-raised', 'border', 'text', 'text-muted'];
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

// The default (dark) block is the first `:root` rule; the light block is the
// `:root` rule inside the prefers-color-scheme: light media query.
const dark = /^:root\s*\{([^}]*)\}/m.exec(css)?.[1] ?? '';
const light =
  /@media \(prefers-color-scheme: light\)\s*\{\s*:root\s*\{([^}]*)\}/.exec(css)?.[1] ?? '';

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
});
