// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { useSyncExternalStore } from 'react';

/*
 * Whether the full-screen restore screen is up. While a backup is restored the
 * server answers every API request except the restore status with 503
 * "restoring"; the first such answer, or a restore started from the Backups
 * page, raises the screen. Raising it again while it is up does nothing, so
 * the requests that keep failing meanwhile cannot open it twice.
 */

let shown = false;
const listeners = new Set<() => void>();

function set(next: boolean) {
  if (shown === next) return;
  shown = next;
  for (const listener of listeners) listener();
}

export function showRestoreOverlay() {
  set(true);
}

export function hideRestoreOverlay() {
  set(false);
}

export function isRestoreOverlayShown() {
  return shown;
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** True while the restore screen is up. */
export function useRestoreOverlay() {
  return useSyncExternalStore(subscribe, isRestoreOverlayShown, isRestoreOverlayShown);
}
