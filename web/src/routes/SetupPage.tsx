// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { useQueryClient } from '@tanstack/react-query';
import { ArchiveRestore, UserPlus } from 'lucide-react';
import { useState, type FormEvent } from 'react';
import { useLocation, useNavigate, useSearchParams } from 'react-router';
import { api, ApiError, unwrap } from '../api/client';
import { setupKey } from '../api/hooks';
import { AuthShell } from '../components/AuthShell';
import { Spinner } from '../components/ConfirmDialog';
import { buttonClass, monoInputClass } from '../ui';
import { Problem } from './admin/kit';
import { AuthField, PasswordField, PasswordRules, passwordChecks } from './auth/fields';
import { RestoreFromBackup } from './setup/RestoreFromBackup';
import { setupMessage } from './setup/messages';

export const passwordRules = 'At least 12 characters, and not your username.';

/** The steps from a new server to the first signed-in administrator. */
export const setupSteps = ['Administrator', 'Two-factor', 'Recovery codes'];

function NewInstallation({ onBack }: { onBack: () => void }) {
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
        <button type="button" className={buttonClass('ghost')} onClick={onBack}>
          Back
        </button>
      </form>
    </AuthShell>
  );
}

type Choice = 'choose' | 'new' | 'restore';

/**
 * The choice is part of the address, so the browser's Back button returns to
 * the two choices. The page's own Back button steps back in the history when
 * the entry before is the two choices, so no second entry of them is left
 * behind; a choice opened straight from its address is replaced instead.
 */
function useChoice(): [Choice, (choice: Exclude<Choice, 'choose'>) => void, () => void] {
  const [params, setParams] = useSearchParams();
  const location = useLocation();
  const navigate = useNavigate();
  const start = params.get('start');
  const choice: Choice = start === 'new' || start === 'restore' ? start : 'choose';
  const fromChoice = (location.state as { fromChoice?: boolean } | null)?.fromChoice === true;
  return [
    choice,
    (next) => setParams({ start: next }, { state: { fromChoice: true } }),
    () => {
      if (fromChoice) void navigate(-1);
      else setParams({}, { replace: true });
    },
  ];
}

const choiceClass =
  'flex w-full items-start gap-3 rounded-inner border border-line bg-surface p-4 text-left transition-colors hover:border-line-strong hover:bg-sunken';

/** The start of a new server: a fresh installation, or one moved over from a backup. */
export function SetupPage() {
  const [choice, setChoice, back] = useChoice();
  if (choice === 'new') return <NewInstallation onBack={back} />;
  if (choice === 'restore') return <RestoreFromBackup onBack={back} />;
  return (
    <AuthShell
      title="Welcome to Scanopsis"
      lead="This installation is new. How do you want to start?"
    >
      <div className="flex flex-col gap-3">
        <button type="button" className={choiceClass} onClick={() => setChoice('new')}>
          <UserPlus
            size={20}
            strokeWidth={1.75}
            aria-hidden="true"
            className="mt-0.5 shrink-0 text-brand"
          />
          <span className="flex flex-col gap-0.5">
            <span className="text-body font-medium text-text">Set up a new installation</span>
            <span className="text-body-s text-text-muted">
              Create the first administrator and start empty.
            </span>
          </span>
        </button>
        <button type="button" className={choiceClass} onClick={() => setChoice('restore')}>
          <ArchiveRestore
            size={20}
            strokeWidth={1.75}
            aria-hidden="true"
            className="mt-0.5 shrink-0 text-brand"
          />
          <span className="flex flex-col gap-0.5">
            <span className="text-body font-medium text-text">Restore from a backup</span>
            <span className="text-body-s text-text-muted">
              Moving Scanopsis to a new host? Upload a backup from the old one. Use the same secret
              key file as the old installation.
            </span>
          </span>
        </button>
      </div>
    </AuthShell>
  );
}
