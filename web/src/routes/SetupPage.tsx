// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useQueryClient } from '@tanstack/react-query';
import { useState, type FormEvent } from 'react';
import { useNavigate } from 'react-router';
import { api, ApiError, unwrap } from '../api/client';
import { describeError } from '../api/errors';
import { setupKey } from '../api/hooks';
import { AuthShell } from '../components/AuthShell';
import { Spinner } from '../components/ConfirmDialog';
import { buttonClass, monoInputClass } from '../ui';
import { Problem } from './admin/kit';
import { AuthField, PasswordField, PasswordRules, passwordChecks } from './auth/fields';

export const passwordRules = 'At least 12 characters, and not your username.';

/** The steps from a new server to the first signed-in administrator. */
export const setupSteps = ['Administrator', 'Two-factor', 'Recovery codes'];

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
      unwrap(
        await api.POST('/api/v1/setup', { body: { token: token.trim(), username, password } }),
      );
      queryClient.clear();
      queryClient.setQueryData(setupKey, { required: false });
      void navigate('/2fa/setup', { replace: true, state: { fromSetup: true } });
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

  const strong = passwordChecks(password, username).every((c) => c.ok);
  return (
    <AuthShell
      title="Set up Scanopsis"
      lead="Create the first administrator. Two-factor sign-in comes next."
      steps={{ labels: setupSteps, current: 0 }}
    >
      <form onSubmit={(e) => void submit(e)} className="flex flex-col gap-4">
        <AuthField
          id="token"
          label="Setup token"
          type="text"
          autoComplete="off"
          spellCheck={false}
          autoCapitalize="off"
          required
          value={token}
          onChange={(e) => setToken(e.target.value)}
          className={monoInputClass}
          hint={
            <>
              Printed in the server log at start-up, for example by{' '}
              <code className="rounded-[4px] bg-sunken px-1 font-mono text-text">
                docker logs scanopsis
              </code>
              .
            </>
          }
        />
        <AuthField
          id="username"
          label="Username"
          type="text"
          autoComplete="username"
          autoCapitalize="off"
          spellCheck={false}
          required
          value={username}
          onChange={(e) => setUsername(e.target.value)}
        />
        <PasswordField
          id="password"
          label="Password"
          autoComplete="new-password"
          required
          minLength={12}
          value={password}
          onChange={setPassword}
          hint={<PasswordRules password={password} username={username} />}
        />
        <PasswordField
          id="confirm"
          label="Confirm password"
          autoComplete="new-password"
          required
          value={confirm}
          onChange={(v) => {
            setConfirm(v);
            setMismatch(false);
          }}
          error={mismatch ? 'The passwords do not match.' : undefined}
        />
        {error ? <Problem>{error}</Problem> : null}
        <button
          type="submit"
          disabled={pending}
          aria-busy={pending || undefined}
          className={`${buttonClass('primary', 'lg')} mt-2 w-full`}
          title={strong ? undefined : passwordRules}
        >
          {pending ? <Spinner /> : null}
          Create administrator
        </button>
      </form>
    </AuthShell>
  );
}
