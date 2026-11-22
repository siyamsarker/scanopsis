// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useEffect, useRef, type RefObject } from 'react';

/*
 * Keyboard shortcuts of the application shell:
 *   Cmd/Ctrl+K  open the command palette
 *   /           focus the page's search field
 *   ?           show the shortcuts
 * Single-key shortcuts never fire while typing in a field.
 */

export interface Shortcut {
  keys: string[];
  description: string;
}

/** The list the shortcuts dialog shows; the platform's modifier key is filled in. */
export function shortcutList(mac = isMac()): Shortcut[] {
  const mod = mac ? '⌘' : 'Ctrl';
  return [
    { keys: [mod, 'K'], description: 'Open the command palette' },
    { keys: ['/'], description: 'Search on this page' },
    { keys: ['?'], description: 'Show keyboard shortcuts' },
    { keys: ['Esc'], description: 'Close a dialog, menu or panel' },
  ];
}

export function isMac(): boolean {
  if (typeof navigator === 'undefined') return false;
  return /Mac|iPhone|iPad|iPod/i.test(navigator.platform || navigator.userAgent);
}

let registered: HTMLElement | null = null;

/**
 * Marks an input as the page's main search, the one `/` focuses. Without one,
 * `/` focuses the first search field (any element with data-search-field) in
 * the main content. SearchInput calls this for its `primary` prop.
 */
export function usePrimarySearch(ref: RefObject<HTMLElement | null>, enabled = true) {
  useEffect(() => {
    const el = ref.current;
    if (!enabled || !el) return;
    registered = el;
    return () => {
      if (registered === el) registered = null;
    };
  }, [ref, enabled]);
}

/** Focuses the page's search field. Returns false when the page has none. */
export function focusPageSearch(root: ParentNode = document): boolean {
  const candidates: (HTMLElement | null)[] = [
    registered?.isConnected ? registered : null,
    root.querySelector<HTMLElement>('main [data-search-field]'),
    root.querySelector<HTMLElement>('[data-search-field]'),
  ];
  const target = candidates.find((el) => el && !el.closest('[hidden], [inert]'));
  if (!target) return false;
  target.focus();
  if (target instanceof HTMLInputElement) target.select();
  return true;
}

/** True while the person types in a field, where single-key shortcuts must not fire. */
export function isTyping(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  if (target.isContentEditable) return true;
  const tag = target.tagName;
  if (tag === 'TEXTAREA' || tag === 'SELECT') return true;
  if (tag === 'INPUT') {
    const type = (target as HTMLInputElement).type;
    return !['checkbox', 'radio', 'button', 'submit', 'reset', 'range', 'color'].includes(type);
  }
  return target.getAttribute('role') === 'combobox';
}

/** True while a modal overlay (dialog, menu, palette) is open. */
function overlayOpen(): boolean {
  return document.querySelector('[role="dialog"], [role="alertdialog"], [role="menu"]') !== null;
}

export interface ShortcutHandlers {
  onPalette: () => void;
  onShortcuts: () => void;
}

/** Listens for the shell's shortcuts on the window while mounted. */
export function useGlobalShortcuts(handlers: ShortcutHandlers) {
  const latest = useRef(handlers);
  useEffect(() => {
    latest.current = handlers;
  });

  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      if (event.defaultPrevented || event.isComposing) return;
      const key = event.key;
      if ((event.metaKey || event.ctrlKey) && !event.altKey && key.toLowerCase() === 'k') {
        event.preventDefault();
        latest.current.onPalette();
        return;
      }
      if (event.metaKey || event.ctrlKey || event.altKey) return;
      if (isTyping(event.target) || overlayOpen()) return;
      if (key === '/') {
        if (focusPageSearch()) event.preventDefault();
      } else if (key === '?') {
        event.preventDefault();
        latest.current.onShortcuts();
      }
    }
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, []);
}
