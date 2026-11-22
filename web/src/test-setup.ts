// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import '@testing-library/jest-dom/vitest';
import { MotionGlobalConfig } from 'motion/react';
import { clearToasts } from './components/Toast';

// Animations jump to their end state, so content is visible at once.
MotionGlobalConfig.skipAnimations = true;

// jsdom lacks a few browser APIs the overlay primitives (Radix) use.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
const hasDom = typeof Element !== 'undefined';
if (hasDom && !('ResizeObserver' in globalThis)) {
  Object.defineProperty(globalThis, 'ResizeObserver', {
    value: ResizeObserverStub,
    writable: true,
  });
}
if (hasDom) {
  const proto = Element.prototype as unknown as Record<string, unknown>;
  proto.hasPointerCapture ??= () => false;
  proto.setPointerCapture ??= () => {};
  proto.releasePointerCapture ??= () => {};
  proto.scrollIntoView ??= () => {};
}
if (hasDom && !window.matchMedia) {
  window.matchMedia = (query: string) =>
    ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }) as MediaQueryList;
}

afterEach(() => {
  vi.unstubAllGlobals();
  clearToasts();
});
