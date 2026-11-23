// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { useSyncExternalStore } from 'react';

export type Theme = 'system' | 'light' | 'dark';

export const themeStorageKey = 'scanopsis.theme';

const listeners = new Set<() => void>();

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

/** The applied choice, read from <html>, which applyTheme keeps current. */
function appliedTheme(): Theme {
  const value = document.documentElement.getAttribute('data-theme');
  return isTheme(value) ? value : 'system';
}

/**
 * The current choice and a setter, shared by every theme control (the account
 * menu, the command palette, the sign-in pages) so they always agree.
 */
export function useTheme(): [Theme, (theme: Theme) => void] {
  const theme = useSyncExternalStore(subscribe, appliedTheme, () => 'system' as Theme);
  return [theme, setTheme];
}
export const themes: readonly Theme[] = ['system', 'light', 'dark'];

function isTheme(value: unknown): value is Theme {
  return themes.some((theme) => theme === value);
}

/** The saved choice, or "system" when nothing valid is saved or storage is blocked. */
export function readStoredTheme(): Theme {
  try {
    const value = globalThis.localStorage?.getItem(themeStorageKey);
    return isTheme(value) ? value : 'system';
  } catch {
    return 'system';
  }
}

/** Puts the choice on <html>. "system" removes the attribute so the media query decides. */
export function applyTheme(theme: Theme): void {
  const root = document.documentElement;
  if (theme === 'system') root.removeAttribute('data-theme');
  else root.setAttribute('data-theme', theme);
}

/** Applies and saves a choice. A blocked or full storage never breaks the switch. */
export function setTheme(theme: Theme): void {
  applyTheme(theme);
  for (const listener of listeners) listener();
  try {
    if (theme === 'system') globalThis.localStorage?.removeItem(themeStorageKey);
    else globalThis.localStorage?.setItem(themeStorageKey, theme);
  } catch {
    // The choice still applies for this page view.
  }
}
