// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useEffect } from 'react';

/*
 * A drop-in replacement for react-style-singleton (used by react-remove-scroll,
 * which the dialog, menu, popover and select primitives use to lock page
 * scrolling). The original inserts <style> elements, which the Content Security
 * Policy (style-src 'self') blocks. This version adds the same rules through a
 * constructed stylesheet (CSSOM), which the policy allows. vite.config.ts
 * points the package name here for the application build.
 *
 * Where constructed stylesheets are missing (old browsers, jsdom) it falls back
 * to a <style> element, exactly like the original.
 */

interface Sheet {
  add: (style: string) => void;
  remove: () => void;
}

function canConstruct(): boolean {
  try {
    return (
      typeof CSSStyleSheet !== 'undefined' &&
      typeof CSSStyleSheet.prototype.replaceSync === 'function' &&
      Array.isArray(document.adoptedStyleSheets)
    );
  } catch {
    return false;
  }
}

export function stylesheetSingleton(): Sheet {
  let counter = 0;
  let sheet: CSSStyleSheet | null = null;
  let tag: HTMLStyleElement | null = null;

  return {
    add(style: string) {
      if (counter === 0 && typeof document !== 'undefined') {
        if (canConstruct()) {
          sheet = new CSSStyleSheet();
          sheet.replaceSync(style);
          document.adoptedStyleSheets = [...document.adoptedStyleSheets, sheet];
        } else {
          tag = document.createElement('style');
          tag.appendChild(document.createTextNode(style));
          document.head.appendChild(tag);
        }
      }
      counter++;
    },
    remove() {
      counter--;
      if (counter > 0) return;
      counter = 0;
      if (sheet) {
        const done = sheet;
        document.adoptedStyleSheets = document.adoptedStyleSheets.filter((s) => s !== done);
        sheet = null;
      }
      if (tag) {
        tag.remove();
        tag = null;
      }
    },
  };
}

/** A hook that adds the styles while at least one component using it is mounted. */
export function styleHookSingleton() {
  const sheet = stylesheetSingleton();
  return (styles: string, isDynamic?: boolean) => {
    useEffect(
      () => {
        sheet.add(styles);
        return () => sheet.remove();
      },
      // Same dependency as the original: static styles are added once.
      // eslint-disable-next-line react-hooks/exhaustive-deps
      [styles && isDynamic],
    );
  };
}

/** A component form of styleHookSingleton; renders nothing. */
export function styleSingleton() {
  const useStyle = styleHookSingleton();
  function Sheet({ styles, dynamic }: { styles: string; dynamic?: boolean }) {
    useStyle(styles, dynamic);
    return null;
  }
  return Sheet;
}
