// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Entry of the development-only gallery page (web/gallery.html). The
 * production build only bundles index.html, so this never ships.
 * ?theme=light picks the light palette.
 */

import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import '@fontsource-variable/inter';
import '@fontsource-variable/jetbrains-mono';
import '../index.css';
import Gallery from './Gallery';

const root = document.getElementById('root');
if (import.meta.env.DEV && root) {
  const theme =
    new URLSearchParams(window.location.search).get('theme') === 'light' ? 'light' : 'dark';
  createRoot(root).render(
    <StrictMode>
      <Gallery theme={theme} />
    </StrictMode>,
  );
}
