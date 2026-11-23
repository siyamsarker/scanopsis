// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { useNavigate } from 'react-router';
import { api, ApiError, unwrap } from '../api/client';
import { linkClass } from '../ui';
import { forgetSession } from '../auth/forgetSession';

/** Signs out from a session step, where the application header is not shown. */
export function SignOutButton() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [failed, setFailed] = useState(false);

  async function signOut() {
    setFailed(false);
    try {
      unwrap(await api.POST('/api/v1/auth/logout'));
    } catch (err) {
      // 401 means the session is already gone, which is what signing out wants.
      if (!(err instanceof ApiError && err.status === 401)) {
        setFailed(true);
        return;
      }
    }
    forgetSession(queryClient);
    void navigate('/login', { replace: true });
  }

  return (
    <>
      <button type="button" className={linkClass} onClick={() => void signOut()}>
        Sign out
      </button>
      {failed && (
        <span role="alert" className="ml-3 text-text">
          Signing out failed. Try again.
        </span>
      )}
    </>
  );
}
