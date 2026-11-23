// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

// Checks the contrast of every colour pair the interface relies on, in both
// themes, straight from the tokens in src/index.css. Run `node
// scripts/contrast.mjs` for the table; src/tokens.test.ts enforces it.

import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

/** @param {string} hex @returns {[number, number, number]} */
export function parseHex(hex) {
  const value = hex.replace('#', '');
  const full = value.length === 3 ? [...value].map((c) => c + c).join('') : value;
  const n = parseInt(full, 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

/** color-mix(in srgb, a p, b): interpolation of the gamma-encoded channels. */
export function mix(a, b, p) {
  return a.map((v, i) => v * p + b[i] * (1 - p));
}

export function luminance(rgb) {
  const [r, g, b] = rgb.map((v) => {
    const c = v / 255;
    return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

export function ratio(a, b) {
  const [x, y] = [luminance(a), luminance(b)].sort((p, q) => q - p);
  return (x + 0.05) / (y + 0.05);
}

/** The custom properties with hex values declared in the first block matching `selector`. */
export function tokensOf(css, selector) {
  const at = css.indexOf(selector);
  if (at < 0) throw new Error(`no block for ${selector}`);
  const body = css.slice(css.indexOf('{', at) + 1, css.indexOf('}', at));
  const tokens = {};
  for (const match of body.matchAll(/--([a-z0-9-]+):\s*(#[0-9a-fA-F]{3,6})\s*;/g)) {
    tokens[match[1]] = parseHex(match[2]);
  }
  return tokens;
}

/** The two theme palettes. */
export function themes(css) {
  return {
    dark: tokensOf(css, ":root,\n[data-theme-scope='dark']"),
    light: tokensOf(css, ":root[data-theme='light'],\n[data-theme-scope='light']"),
  };
}

const SURFACES = ['bg', 'surface', 'surface-raised', 'surface-overlay', 'sunken'];
const PANELS = ['bg', 'surface', 'surface-raised'];
const TONES = ['sev-critical', 'sev-high', 'sev-medium', 'sev-low', 'sev-unknown', 'ok', 'brand'];

/**
 * Every pair the components rely on: [label, foreground, background, minimum].
 * Text needs 4.5:1; faint text, control borders, focus rings and marks 3:1.
 */
export function pairs(t) {
  const list = [];
  const add = (label, fg, bg, min) => list.push({ label, fg, bg, min });
  for (const s of SURFACES) {
    add(`text on ${s}`, t.text, t[s], 4.5);
    add(`text-muted on ${s}`, t['text-muted'], t[s], 4.5);
    add(`text-faint on ${s}`, t['text-faint'], t[s], 3);
    add(`line-strong (control border) on ${s}`, t['line-strong'], t[s], 3);
    add(`brand (focus ring) on ${s}`, t.brand, t[s], 3);
  }
  for (const s of PANELS) add(`brand (link text) on ${s}`, t.brand, t[s], 4.5);
  add('brand-ink on brand (primary button)', t['brand-ink'], t.brand, 4.5);
  add('fail-ink on fail (danger button)', t['fail-ink'], t.fail, 4.5);
  for (const tone of TONES) {
    for (const s of PANELS) add(`${tone} mark on ${s}`, t[tone], t[s], 3);
    // Pills: 14% tone over the surface, text 70% tone with the text colour.
    const fill = mix(t[tone], t.surface, 0.14);
    add(`${tone} pill text on its fill`, mix(t[tone], t.text, 0.7), fill, 4.5);
  }
  add('lens-a on bg', t['lens-a'], t.bg, 3);
  add('lens-b on bg', t['lens-b'], t.bg, 3);
  const ratings = {
    a: t.ok,
    b: mix(t.ok, t['sev-medium'], 0.5),
    c: t['sev-medium'],
    d: mix(t['sev-medium'], t.fail, 0.5),
    e: t.fail,
  };
  for (const [letter, tone] of Object.entries(ratings)) {
    const fill = mix(tone, t.surface, 0.16);
    add(`rating ${letter.toUpperCase()} letter on its tile`, mix(tone, t.text, 0.7), fill, 4.5);
  }
  return list.map((pair) => ({ ...pair, ratio: ratio(pair.fg, pair.bg) }));
}

function main() {
  const css = readFileSync(
    join(dirname(fileURLToPath(import.meta.url)), '..', 'src', 'index.css'),
    'utf8',
  );
  let failed = 0;
  for (const [name, t] of Object.entries(themes(css))) {
    console.log(`\n${name}`);
    for (const pair of pairs(t)) {
      const ok = pair.ratio >= pair.min;
      if (!ok) failed++;
      console.log(
        `  ${ok ? 'ok  ' : 'FAIL'} ${pair.ratio.toFixed(2).padStart(5)} (min ${pair.min})  ${pair.label}`,
      );
    }
  }
  if (failed) {
    console.error(`\n${failed} pair(s) below the minimum`);
    process.exit(1);
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main();
}
