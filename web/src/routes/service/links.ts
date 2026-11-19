// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { safeExternalUrl } from '../../format';

/** The findings explorer of a service, keeping the selected ref and adding filters. */
export function findingsHref(
  serviceId: number,
  ref: string | null | undefined,
  filter: Record<string, string> = {},
): string {
  const params = new URLSearchParams();
  if (ref) params.set('ref', ref);
  for (const [key, value] of Object.entries(filter)) params.set(key, value);
  const query = params.toString();
  return `/services/${serviceId}/findings${query ? `?${query}` : ''}`;
}

/**
 * The GitLab project URL, derived from a pipeline or commit URL (everything
 * before "/-/"). Undefined when no usable http(s) URL is known.
 */
export function projectUrlFrom(url: string | null | undefined): string | undefined {
  const safe = safeExternalUrl(url);
  if (!safe) return undefined;
  const cut = safe.indexOf('/-/');
  return cut > 0 ? safe.slice(0, cut) : undefined;
}
