// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { fileURLToPath } from 'node:url';
import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import type { Plugin } from 'vite';
import { defineConfig } from 'vitest/config';

/**
 * Keeps third-party code within the Content Security Policy (style-src 'self'):
 * no <style> elements may be inserted at run time.
 *
 * - react-style-singleton (scroll locking under dialogs, menus and selects) is
 *   replaced by src/csp/styleSingleton.ts, which uses a constructed stylesheet.
 * - Radix Select renders a <style> element that hides the list's scrollbar; it
 *   is removed here and the same two rules live in index.css. The build fails
 *   if the code no longer matches, so an upgrade cannot bring it back silently.
 */
function cspSafeDependencies(): Plugin {
  const selectStyle =
    /jsx\(\s*"style",\s*\{\s*dangerouslySetInnerHTML:\s*\{\s*__html:\s*`\[data-radix-select-viewport\][^`]*`\s*\},\s*nonce\s*\}\s*\)/;
  return {
    name: 'scanopsis:csp-safe-dependencies',
    apply: 'build',
    transform(code, id) {
      if (!/[\\/]@radix-ui[\\/]react-select[\\/]dist[\\/]index\.mjs$/.test(id)) return null;
      if (!selectStyle.test(code)) {
        this.error(
          '@radix-ui/react-select no longer renders the expected <style>; update vite.config.ts',
        );
      }
      return { code: code.replace(selectStyle, 'null'), map: null };
    },
  };
}

export default defineConfig({
  plugins: [react(), tailwindcss(), cspSafeDependencies()],
  resolve: {
    alias: {
      'react-style-singleton': fileURLToPath(
        new URL('./src/csp/styleSingleton.ts', import.meta.url),
      ),
    },
  },
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
        'src/routes/**/model.ts',
        'src/routes/**/links.ts',
        'src/routes/service/conditions.ts',
        'src/auth/**',
        'src/theme.ts',
        'src/api/client.ts',
        'src/viz/**',
      ],
      // The gallery entry only mounts the gallery in the dev server.
      exclude: ['**/*.test.{ts,tsx}', '**/*.gen.ts', 'src/viz/gallery-main.tsx'],
      reporter: ['text', 'json-summary'],
      // Under node_modules so formatters, linters and git never see it.
      reportsDirectory: 'node_modules/.coverage',
      thresholds: { lines: 75, functions: 75, branches: 75, statements: 75 },
    },
  },
});
