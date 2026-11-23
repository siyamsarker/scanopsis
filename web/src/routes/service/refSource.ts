// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import type { components } from '../../api/schema.gen';

export type RefSource = components['schemas']['RefSource'];

/**
 * The refs to offer in a selector: every scanned ref, plus the ref being shown
 * when it has no scans yet (a dashboard branch chosen before its first upload).
 */
export function refChoices(refs: readonly string[], current: string | undefined): string[] {
  return current && !refs.includes(current) ? [...refs, current] : [...refs];
}

/**
 * Where the shown ref comes from, as the end of "Showing main, …": the
 * dashboard branch an administrator chose, the latest scan when none was
 * chosen, or the viewer's own selection.
 */
export function describeRefSource(
  source: RefSource,
  ref: string,
  defaultRef: string | null | undefined,
): string {
  if (defaultRef && ref === defaultRef) return 'the dashboard branch';
  if (source === 'default') return 'the dashboard branch';
  if (source === 'latest') return 'the latest scanned branch; no dashboard branch is chosen';
  return defaultRef ? `chosen here; the dashboard branch is ${defaultRef}` : 'chosen here';
}
