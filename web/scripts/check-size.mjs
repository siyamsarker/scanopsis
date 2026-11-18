// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { readFileSync } from 'node:fs';
import { dirname, join, resolve, sep } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { gzipSync } from 'node:zlib';

export const BUDGET_KB = 300;

/**
 * Returns the local JavaScript paths that an HTML document loads directly,
 * through script tags and modulepreload links. External URLs are ignored.
 * @param {string} html
 * @returns {string[]}
 */
export function extractScriptPaths(html) {
  const paths = new Set();
  for (const tag of html.match(/<(?:script|link)\b[^>]*>/gi) ?? []) {
    const isScript = /^<script\b/i.test(tag);
    const isPreload = /\brel=["']modulepreload["']/i.test(tag);
    if (!isScript && !isPreload) continue;
    const attr = isScript ? 'src' : 'href';
    const value = new RegExp(`\\b${attr}=["']([^"']+)["']`, 'i').exec(tag)?.[1];
    if (!value || /^(?:[a-z][a-z0-9+.-]*:)?\/\//i.test(value)) continue;
    if (!/\.m?js(?:[?#].*)?$/i.test(value)) continue;
    paths.add(value);
  }
  return [...paths];
}

/**
 * Resolves an asset path from index.html inside distDir. Returns null when the
 * path would escape distDir, so the check never reads files outside the build.
 * @param {string} distDir absolute path of the build directory
 * @param {string} assetPath
 * @returns {string | null}
 */
export function resolveAsset(distDir, assetPath) {
  const root = resolve(distDir);
  const abs = resolve(root, assetPath.replace(/^\/+/, ''));
  return abs.startsWith(root + sep) ? abs : null;
}

/**
 * @param {number} totalBytes
 * @param {number} [budgetKb]
 * @returns {{ ok: boolean, totalKb: number, budgetKb: number }}
 */
export function checkBudget(totalBytes, budgetKb = BUDGET_KB) {
  const totalKb = totalBytes / 1024;
  return { ok: totalKb <= budgetKb, totalKb, budgetKb };
}

function main() {
  const distDir = join(dirname(fileURLToPath(import.meta.url)), '..', 'dist', 'app');
  const html = readFileSync(join(distDir, 'index.html'), 'utf8');
  const paths = extractScriptPaths(html);
  if (paths.length === 0) {
    console.error('check-size: no JavaScript found in dist/app/index.html');
    process.exit(1);
  }
  let total = 0;
  for (const path of paths) {
    const file = resolveAsset(distDir, path);
    if (!file) {
      console.error(`check-size: refusing to read a path outside dist/app: ${path}`);
      process.exit(1);
    }
    total += gzipSync(readFileSync(file)).length;
  }
  const { ok, totalKb, budgetKb } = checkBudget(total);
  console.log(`Initial JavaScript: ${totalKb.toFixed(1)} KB gzipped (budget ${budgetKb} KB)`);
  if (!ok) {
    console.error('check-size: initial JavaScript exceeds the budget');
    process.exit(1);
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main();
}
