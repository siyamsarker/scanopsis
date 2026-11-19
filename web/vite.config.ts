// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    outDir: 'dist/app',
    emptyOutDir: true,
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test-setup.ts'],
    coverage: {
      provider: 'v8',
      // Logic modules only. Components are covered by the behaviour tests and
      // the end-to-end suite; a percentage there would reward snapshot churn.
      // New modules that match these patterns are measured automatically.
      include: [
        'src/format/**',
        'src/charts/**',
        'src/routes/**/model.ts',
        'src/routes/**/links.ts',
        'src/routes/service/conditions.ts',
        'src/auth/**',
        'src/theme.ts',
        'src/api/client.ts',
      ],
      // ChartCore wraps the lazily loaded ECharts renderer, which jsdom cannot
      // draw; the end-to-end suite exercises it in a browser.
      exclude: ['**/*.test.{ts,tsx}', '**/*.gen.ts', 'src/charts/ChartCore.tsx'],
      reporter: ['text', 'json-summary'],
      // Under node_modules so formatters, linters and git never see it.
      reportsDirectory: 'node_modules/.coverage',
      thresholds: { lines: 75, functions: 75, branches: 75, statements: 75 },
    },
  },
});
