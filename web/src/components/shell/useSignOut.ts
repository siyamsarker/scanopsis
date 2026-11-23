// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { useNavigate } from 'react-router';
import { api, ApiError, unwrap } from '../../api/client';
import { toast } from '../Toast';

export const SIGN_OUT_FAILED = 'Signing out failed. Check your connection and try again.';

/**
 * Signs out: ends the session on the server, empties the query cache and goes
 * to the sign-in page. A 401 means the session is already gone, which is what
 * signing out wants. Any other failure keeps the session and says so in a toast.
 */
export function useSignOut() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [signingOut, setSigningOut] = useState(false);

  function finish() {
    setSigningOut(true);
    queryClient.clear();
    void navigate('/login', { replace: true });
  }

  async function signOut() {
    try {
      unwrap(await api.POST('/api/v1/auth/logout'));
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        finish();
        return;
      }
      toast.error(SIGN_OUT_FAILED);
      return;
    }
    finish();
  }

  return { signOut: () => void signOut(), signingOut };
}
