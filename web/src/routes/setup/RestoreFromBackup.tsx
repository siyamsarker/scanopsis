// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { useQueryClient } from '@tanstack/react-query';
import { TriangleAlert } from 'lucide-react';
import { useEffect, useId, useRef, useState, type FormEvent } from 'react';
import { Link } from 'react-router';
import type { RestorePreview } from '../../api/backups';
import { api, ApiError, unwrap } from '../../api/client';
import { setupKey } from '../../api/hooks';
import { showRestoreOverlay } from '../../api/restoreMode';
import { BUSY_DELAY_MS, patiently, readyPart, restUploadEndpoints } from '../../api/upload';
import { useChunkedUpload, type UploadFile } from '../../api/useChunkedUpload';
import { AuthShell } from '../../components/AuthShell';
import { BackupFileDrop } from '../../components/BackupFileDrop';
import { Spinner } from '../../components/ConfirmDialog';
import { toast } from '../../components/Toast';
import { formatBytes } from '../../format';
import { buttonClass, monoInputClass } from '../../ui';
import { backupDate, CONFIRM_PHRASE, retentionWarning } from '../admin/backups/model';
import { Problem } from '../admin/kit';
import { AuthField } from '../auth/fields';
import { setupMessage } from './messages';

const steps = ['Setup token', 'Backup file', 'Restore'];

/** Why the server refused a checked backup; both are fixed by a restart, which prints a new token. */
type Refusal = 'key' | 'schema';

const refusals: Record<Refusal, string> = {
  key: 'This backup was made with another secret key. Copy secrets/secret_key from the old installation and restart Scanopsis.',
  schema:
    'This backup was made by a newer version of Scanopsis. Install that version first, then restore it.',
};

type Step =
  | { kind: 'token'; error?: string }
  | { kind: 'file' }
  | { kind: 'review'; preview: RestorePreview; uploadId: string }
  | { kind: 'blocked'; reason: Refusal }
  | { kind: 'already' };

const labelClass = 'text-body-s font-medium text-text';

/**
 * The upload whose every byte was sent, kept for this browser tab so that a
 * reload during the check picks it up again once the token is entered,
 * instead of asking for a large file once more. The server keeps the outcome
 * of a check for a day.
 */
const SAVED_UPLOAD = 'scanopsis.setup-restore-upload';

interface SavedUpload extends UploadFile {
  id: string;
}

function readSaved(): SavedUpload | null {
  try {
    const saved: unknown = JSON.parse(sessionStorage.getItem(SAVED_UPLOAD) ?? 'null');
    if (
      saved &&
      typeof saved === 'object' &&
      'id' in saved &&
      'name' in saved &&
      'size' in saved &&
      typeof saved.id === 'string' &&
      typeof saved.name === 'string' &&
      typeof saved.size === 'number'
    ) {
      return { id: saved.id, name: saved.name, size: saved.size };
    }
  } catch {
    // Storage that cannot be read or holds something else: start afresh.
  }
  return null;
}

function save(upload: SavedUpload | null) {
  try {
    if (upload) sessionStorage.setItem(SAVED_UPLOAD, JSON.stringify(upload));
    else sessionStorage.removeItem(SAVED_UPLOAD);
  } catch {
    // Without storage, a reload asks for the file again.
  }
}

/**
 * Restores a backup on a new installation, before any administrator exists:
 * the setup token proves the right to do it, typing RESTORE stands in for the
 * two-factor code of the admin panel, and the accounts come from the backup.
 */
export function RestoreFromBackup({ onBack }: { onBack: () => void }) {
  const queryClient = useQueryClient();
  const [token, setToken] = useState('');
  const [step, setStep] = useState<Step>({ kind: 'token' });
  const [checking, setChecking] = useState(false);
  // A file was chosen and checked earlier; choosing another leaves it in the backup folder.
  const [replaced, setReplaced] = useState(false);
  const tokenCheck = useRef<AbortController | null>(null);

  useEffect(() => () => tokenCheck.current?.abort(), []);

  const cleanToken = token.trim();
  const endpoints = restUploadEndpoints<RestorePreview>({
    base: '/api/v1/setup/restore/uploads',
    headers: { 'X-Setup-Token': cleanToken },
    startBody: (file) => ({ token: cleanToken, file_name: file.name, size: file.size }),
    completeBody: { token: cleanToken },
    result: readyPart('preview'),
  });

  /** An answer that ends the restore path: a wrong token, or a setup finished meanwhile. */
  function handleCommon(err: unknown): boolean {
    if (err instanceof ApiError && err.code === 'setup_done') {
      save(null);
      setStep({ kind: 'already' });
      return true;
    }
    if (err instanceof ApiError && err.code === 'invalid_setup_token') {
      setStep({ kind: 'token', error: setupMessage(err) });
      return true;
    }
    return false;
  }

  // The upload followed is one sent before a reload.
  const following = useRef(false);
  const upload = useChunkedUpload(endpoints, {
    onChecking: (id, file) => save({ id, name: file.name, size: file.size }),
    onDone: (preview, uploadId) => {
      following.current = false;
      setReplaced(false);
      setStep({ kind: 'review', preview, uploadId });
    },
    onError: (err) => {
      const followed = following.current;
      following.current = false;
      if (err instanceof ApiError && err.status === 422) {
        save(null);
        const reason: Refusal | null =
          err.code === 'key_mismatch' ? 'key' : err.code === 'newer_schema' ? 'schema' : null;
        if (reason) {
          setStep({ kind: 'blocked', reason });
          return true;
        }
      }
      // A reload went past the check: the server no longer knows the upload.
      if (err instanceof ApiError && err.status === 404 && followed) {
        save(null);
        return true;
      }
      return handleCommon(err);
    },
    describe: setupMessage,
  });
  const { phase } = upload;

  /** Stops the upload; while the server checks the file, says what that means. */
  function cancelUpload() {
    save(null);
    following.current = false;
    if (upload.cancel()) {
      toast.info('Upload cancelled', {
        description:
          'The check of the file was stopped. If it had already finished, the file stays in the backup folder.',
      });
    }
  }

  async function checkToken(event: FormEvent) {
    event.preventDefault();
    if (!cleanToken || checking) return;
    const abort = new AbortController();
    tokenCheck.current = abort;
    setChecking(true);
    setStep({ kind: 'token' });
    try {
      await patiently(
        async () =>
          unwrap(
            await api.POST('/api/v1/setup/restore/token', {
              body: { token: cleanToken },
              signal: abort.signal,
            }),
          ),
        abort.signal,
        BUSY_DELAY_MS,
      );
      setStep({ kind: 'file' });
      // A file sent before a reload: follow its check instead of asking again.
      const saved = readSaved();
      following.current = saved !== null;
      if (saved && upload.phase.kind === 'idle') {
        upload.follow(saved.id, { name: saved.name, size: saved.size });
      }
    } catch (err) {
      if (abort.signal.aborted) return;
      if (!handleCommon(err)) setStep({ kind: 'token', error: setupMessage(err) });
    } finally {
      tokenCheck.current = null;
      setChecking(false);
    }
  }

  if (step.kind === 'already') {
    return (
      <AuthShell title="Already set up">
        <Problem>This installation is already set up. Sign in instead.</Problem>
        <Link
          to="/login"
          replace
          className={buttonClass('primary', 'lg')}
          onClick={() => queryClient.setQueryData(setupKey, { required: false })}
        >
          Go to sign-in
        </Link>
      </AuthShell>
    );
  }

  if (step.kind === 'blocked') {
    return (
      <AuthShell title="This backup cannot be restored" steps={{ labels: steps, current: 1 }}>
        <Problem>{refusals[step.reason]}</Problem>
        <p className="text-body-s text-text-muted">
          After the restart, Scanopsis prints a new setup token in its log.
        </p>
        <div className="flex flex-wrap gap-2">
          <button
            type="button"
            className={buttonClass('primary')}
            onClick={() => {
              setToken('');
              setStep({ kind: 'token' });
            }}
          >
            Enter the new setup token
          </button>
          <button
            type="button"
            className={buttonClass('ghost')}
            onClick={() => {
              save(null);
              setStep({ kind: 'file' });
            }}
          >
            Choose another file
          </button>
        </div>
      </AuthShell>
    );
  }

  if (step.kind === 'review') {
    return (
      <Review
        preview={step.preview}
        token={cleanToken}
        uploadId={step.uploadId}
        onRefused={handleCommon}
        onBack={() => {
          save(null);
          setReplaced(true);
          setStep({ kind: 'file' });
        }}
      />
    );
  }

  if (step.kind === 'file') {
    return (
      <AuthShell
        title="Choose the backup file"
        lead="A backup file from the old installation. It is checked in full before anything is restored."
        steps={{ labels: steps, current: 1 }}
      >
        <BackupFileDrop
          upload={upload}
          note={replaced ? 'The earlier file stays in the backup folder.' : undefined}
        />
        <div className="flex flex-wrap gap-2">
          {phase.kind === 'interrupted' ? (
            <button type="button" className={buttonClass('primary')} onClick={upload.resume}>
              Resume upload
            </button>
          ) : null}
          {phase.kind === 'failed' ? (
            <button
              type="button"
              className={buttonClass('primary')}
              onClick={() => {
                save(null);
                upload.chooseAnother();
              }}
            >
              Choose another file
            </button>
          ) : null}
          {phase.kind === 'idle' ? (
            <button
              type="button"
              className={buttonClass('ghost')}
              onClick={() => setStep({ kind: 'token' })}
            >
              Back
            </button>
          ) : (
            <button type="button" className={buttonClass('secondary')} onClick={cancelUpload}>
              {phase.kind === 'failed' ? 'Start over' : 'Cancel upload'}
            </button>
          )}
        </div>
      </AuthShell>
    );
  }

  return (
    <AuthShell
      title="Restore from a backup"
      lead="Move Scanopsis to this host with a backup from the old one. The accounts come from the backup."
      steps={{ labels: steps, current: 0 }}
    >
      <form onSubmit={(e) => void checkToken(e)} className="flex flex-col gap-4">
        <AuthField
          id="restore-token"
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
        {step.error ? <Problem>{step.error}</Problem> : null}
        <div className="flex flex-wrap gap-2">
          <button
            type="submit"
            disabled={checking}
            aria-busy={checking || undefined}
            className={buttonClass('primary', 'lg')}
          >
            {checking ? <Spinner /> : null}
            Continue
          </button>
          <button type="button" className={buttonClass('ghost', 'lg')} onClick={onBack}>
            Back
          </button>
        </div>
      </form>
    </AuthShell>
  );
}

function Fact({ term, children }: { term: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5 sm:flex-row sm:gap-4">
      <dt className="text-body-s text-text-muted sm:w-40 sm:shrink-0">{term}</dt>
      <dd className="min-w-0 text-body-s text-text">{children}</dd>
    </div>
  );
}

/** What the backup holds, what the restore does, and the typed confirmation. */
function Review({
  preview,
  token,
  uploadId,
  onBack,
  onRefused,
}: {
  preview: RestorePreview;
  token: string;
  uploadId: string;
  onBack: () => void;
  /** Handles a wrong token or a finished setup; true when it did. */
  onRefused: (err: unknown) => boolean;
}) {
  const [phrase, setPhrase] = useState('');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string>();
  const id = useId();
  const warning = retentionWarning(preview);
  const date = backupDate(preview.info);
  const ready = phrase === CONFIRM_PHRASE;

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!ready || pending) return;
    setError(undefined);
    setPending(true);
    try {
      unwrap(
        await api.POST('/api/v1/setup/restore', {
          body: { token, upload_id: uploadId, confirm: CONFIRM_PHRASE },
        }),
      );
      save(null);
      showRestoreOverlay();
    } catch (err) {
      setPending(false);
      if (!onRefused(err)) setError(setupMessage(err));
    }
  }

  return (
    <AuthShell
      title="Review the backup"
      lead="Check what the backup holds before Scanopsis takes it over."
      steps={{ labels: steps, current: 2 }}
      wide
    >
      <dl className="flex flex-col gap-2.5 rounded-inner border border-line bg-surface p-4">
        <Fact term="Made on">{date}</Fact>
        <Fact term="Scanopsis version">{preview.info.app_version}</Fact>
        <Fact term="Secret key">The same secret key as this installation</Fact>
        <Fact term="File">
          <span className="font-mono break-all">
            {preview.info.original_name || preview.info.name}
          </span>{' '}
          ({formatBytes(preview.info.size)})
        </Fact>
      </dl>
      <form onSubmit={(e) => void submit(e)} noValidate className="flex flex-col gap-5">
        <ul className="flex list-disc flex-col gap-1.5 pl-5 text-body-s text-text-muted marker:text-text-faint">
          <li>
            Scanopsis takes over everything in this backup: users, settings, services, scans and the
            audit log.
          </li>
          <li>Everything that changed after {date} is not in it.</li>
          <li>Sign in afterwards with an account from the backup.</li>
          <li>
            Pipeline uploads are turned away while the restore runs. Scans uploaded in that time are
            not kept.
          </li>
        </ul>
        {warning ? (
          <p className="tone-warn flex items-start gap-2.5 rounded-inner border border-[color-mix(in_srgb,var(--tone)_35%,var(--line))] bg-[color-mix(in_srgb,var(--tone)_8%,var(--surface-overlay))] px-3.5 py-3 text-body-s text-text">
            <TriangleAlert
              size={16}
              strokeWidth={1.75}
              aria-hidden="true"
              className="tone-fg mt-0.5 shrink-0"
            />
            <span>{warning}</span>
          </p>
        ) : null}
        <div className="flex flex-col gap-1.5">
          <label htmlFor={`${id}-phrase`} className={labelClass}>
            Type {CONFIRM_PHRASE} to confirm
          </label>
          <input
            id={`${id}-phrase`}
            type="text"
            autoComplete="off"
            autoCapitalize="characters"
            spellCheck={false}
            value={phrase}
            disabled={pending}
            onChange={(e) => setPhrase(e.target.value)}
            className={monoInputClass}
          />
        </div>
        {error ? <Problem>{error}</Problem> : null}
        <div className="flex flex-wrap gap-2">
          <button
            type="submit"
            className={buttonClass('danger')}
            disabled={!ready || pending}
            aria-busy={pending || undefined}
          >
            {pending ? <Spinner /> : null}
            Restore backup
          </button>
          <button
            type="button"
            className={buttonClass('ghost')}
            disabled={pending}
            onClick={onBack}
          >
            Choose another file
          </button>
        </div>
      </form>
    </AuthShell>
  );
}
