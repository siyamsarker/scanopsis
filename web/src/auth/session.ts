// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import type { Me } from '../api/client';

export type SessionStep = '/2fa/setup' | '/2fa' | '/password';

/** The step a signed-in user still has to finish, or null when the session is complete. */
export function nextStep(me: Me): SessionStep | null {
  if (!me.mfa_enrolled) return '/2fa/setup';
  if (!me.mfa_verified) return '/2fa';
  if (me.must_change_password) return '/password';
  return null;
}

export function stepUrl(step: SessionStep, returnTo: string): string {
  return returnTo === '/' ? step : `${step}?return_to=${encodeURIComponent(returnTo)}`;
}
