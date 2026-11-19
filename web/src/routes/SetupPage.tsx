// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useQueryClient } from '@tanstack/react-query';
import { useState, type FormEvent } from 'react';
import { useNavigate } from 'react-router';
import { api, ApiError, unwrap } from '../api/client';
import { describeError } from '../api/errors';
import { setupKey } from '../api/hooks';
import { AuthShell } from '../components/AuthShell';
import { ErrorNote } from '../components/ErrorNote';
import { Field } from '../components/Field';
import { monoInputClass, primaryButtonClass } from '../ui';

export const passwordRules = 'At least 12 characters, and not your username.';

function setupMessage(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.code === 'invalid_setup_token') {
      return 'The setup token is not correct. Copy it again from the server log.';
    }
    if (err.code === 'setup_complete') {
      return 'Setup is already complete. Sign in with the administrator account.';
    }
  }
  return describeError(err);
}

export function SetupPage() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [token, setToken] = useState('');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [mismatch, setMismatch] = useState(false);

  async function submit(event: FormEvent) {
    event.preventDefault();
    setError(null);
    if (password !== confirm) {
      setMismatch(true);
      return;
    }
    setMismatch(false);
    setPending(true);
    try {
      unwrap(await api.POST('/api/v1/setup', { body: { token, username, password } }));
      queryClient.clear();
      queryClient.setQueryData(setupKey, { required: false });
      void navigate('/2fa/setup', { replace: true });
    } catch (err) {
      if (err instanceof ApiError && err.code === 'setup_complete') {
        queryClient.setQueryData(setupKey, { required: false });
        void navigate('/login', { replace: true });
        return;
      }
      setError(setupMessage(err));
      setPending(false);
    }
  }

  return (
    <AuthShell
      title="Set up Scanopsis"
      lead={
        <>
          Create the first administrator. Find the setup token in the server log, for example with{' '}
          <code className="font-mono text-text">docker logs &lt;container&gt;</code>.
        </>
      }
    >
      <form onSubmit={(e) => void submit(e)} className="flex flex-col gap-4">
        <Field
          id="token"
          label="Setup token"
          type="text"
          autoComplete="off"
          spellCheck={false}
          required
          value={token}
          onChange={(e) => setToken(e.target.value)}
          className={monoInputClass}
        />
        <Field
          id="username"
          label="Username"
          type="text"
          autoComplete="username"
          required
          value={username}
          onChange={(e) => setUsername(e.target.value)}
        />
        <Field
          id="password"
          label="Password"
          type="password"
          autoComplete="new-password"
          required
          minLength={12}
          hint={passwordRules}
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        <Field
          id="confirm"
          label="Confirm password"
          type="password"
          autoComplete="new-password"
          required
          value={confirm}
          error={mismatch ? 'The passwords do not match.' : undefined}
          onChange={(e) => setConfirm(e.target.value)}
        />
        {error && <ErrorNote>{error}</ErrorNote>}
        <button type="submit" disabled={pending} className={primaryButtonClass}>
          Create administrator
        </button>
      </form>
    </AuthShell>
  );
}
