// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { BUDGET_KB, checkBudget, extractScriptPaths, resolveAsset } from './check-size.mjs';

const html = `<!doctype html>
<html><head>
<script type="module" crossorigin src="/assets/index-abc.js"></script>
<link rel="modulepreload" crossorigin href="/assets/vendor-def.js">
<link rel="stylesheet" crossorigin href="/assets/index-abc.css">
<link rel="preload" href="/assets/font.woff2" as="font">
<script src="https://cdn.example.com/lib.js"></script>
<script src="//cdn.example.com/other.js"></script>
</head><body></body></html>`;

describe('extractScriptPaths', () => {
  it('returns script and modulepreload paths only', () => {
    expect(extractScriptPaths(html)).toEqual(['/assets/index-abc.js', '/assets/vendor-def.js']);
  });

  it('returns nothing for a page without scripts', () => {
    expect(extractScriptPaths('<html></html>')).toEqual([]);
  });
});

describe('checkBudget', () => {
  it('passes under budget', () => {
    expect(checkBudget((BUDGET_KB - 1) * 1024).ok).toBe(true);
  });

  it('passes exactly at budget', () => {
    expect(checkBudget(BUDGET_KB * 1024).ok).toBe(true);
  });

  it('fails over budget', () => {
    const result = checkBudget((BUDGET_KB + 1) * 1024);
    expect(result.ok).toBe(false);
    expect(result.totalKb).toBeCloseTo(BUDGET_KB + 1);
  });
});

describe('resolveAsset', () => {
  const dist = '/srv/build/dist/app';

  it('resolves root-relative asset paths inside the build directory', () => {
    expect(resolveAsset(dist, '/assets/index-abc.js')).toBe(
      '/srv/build/dist/app/assets/index-abc.js',
    );
  });

  it.each(['/../../etc/passwd', '../secret.js', '/assets/../../app.js', '/'])(
    'refuses %s',
    (path) => {
      expect(resolveAsset(dist, path)).toBeNull();
    },
  );
});
