// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

/*
 * Ranking for the command palette. cmdk calls the filter with an item's value,
 * the typed text and the item's keywords, and sorts groups and items by the
 * score (0 hides the item). Items say what they are by the start of their
 * value, and carry their visible words as keywords:
 *
 *   service:<id>   keywords [name, project path]
 *   page:<path>    keywords [label, ...synonyms]
 *   anything else  value is the label, keywords are synonyms
 *
 * Services come first: someone typing "pay" is looking for payments-api, not
 * for the Search page whose synonym "vulnerability" happens to contain p, a
 * and y. An exact name or path beats everything; a prefix beats a substring;
 * scattered letters only count within the label itself, and low.
 */

function norm(text: string) {
  return text.trim().toLowerCase();
}

/** True when every character of `q` appears in `text` in order. */
export function isSubsequence(q: string, text: string): boolean {
  let at = 0;
  for (const ch of text) {
    if (ch === q[at]) at++;
    if (at === q.length) return true;
  }
  return q.length === 0;
}

function wordStarts(text: string, q: string) {
  return text.split(/[\s/._-]+/).some((word) => word.startsWith(q));
}

function serviceScore(q: string, name: string, path: string): number {
  if (name === q || path === q) return 1;
  if (name.startsWith(q)) return 0.96;
  if (path.startsWith(q) || wordStarts(path, q) || wordStarts(name, q)) return 0.92;
  if (name.includes(q)) return 0.88;
  if (path.includes(q)) return 0.84;
  if (isSubsequence(q, name)) return 0.3;
  return 0;
}

function labelScore(q: string, label: string, synonyms: string[], weight: number): number {
  if (label === q) return 0.9;
  if (label.startsWith(q)) return 0.8 * weight;
  if (wordStarts(label, q)) return 0.7 * weight;
  if (synonyms.some((s) => s.startsWith(q))) return 0.6 * weight;
  if (label.includes(q)) return 0.5 * weight;
  if (synonyms.some((s) => s.includes(q))) return 0.4 * weight;
  if (isSubsequence(q, label)) return 0.1 * weight;
  return 0;
}

export function paletteFilter(value: string, search: string, keywords: string[] = []): number {
  const q = norm(search);
  if (!q) return 1;
  const words = keywords.map(norm);
  if (value.startsWith('service:')) return serviceScore(q, words[0] ?? '', words[1] ?? '');
  if (value.startsWith('vulnerability:')) return 1;
  if (value.startsWith('page:')) return labelScore(q, words[0] ?? '', words.slice(1), 1);
  return labelScore(q, norm(value), words, 0.9);
}
