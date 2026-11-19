// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

export type Theme = 'system' | 'light' | 'dark';

export const themeStorageKey = 'scanopsis.theme';
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
  try {
    if (theme === 'system') globalThis.localStorage?.removeItem(themeStorageKey);
    else globalThis.localStorage?.setItem(themeStorageKey, theme);
  } catch {
    // The choice still applies for this page view.
  }
}
