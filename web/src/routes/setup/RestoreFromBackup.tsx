// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { useQueryClient } from '@tanstack/react-query';
import { FileArchive, TriangleAlert } from 'lucide-react';
import { useEffect, useId, useRef, useState, type ChangeEvent, type FormEvent } from 'react';
import { Link } from 'react-router';
import { api, ApiError, unwrap } from '../../api/client';
import type { RestorePreview } from '../../api/backups';
import { setupKey } from '../../api/hooks';
import { showRestoreOverlay } from '../../api/restoreMode';
import {
  cancelUpload,
  restUploadEndpoints,
  uploadInChunks,
  UploadInterruptedError,
} from '../../api/upload';
import { AuthShell } from '../../components/AuthShell';
import { Spinner } from '../../components/ConfirmDialog';
import { formatBytes } from '../../format';
import { buttonClass, monoInputClass } from '../../ui';
import { Problem } from '../admin/kit';
import { Progress } from '../admin/backups/UploadBackup';
import { backupDate, CONFIRM_PHRASE, retentionWarning } from '../admin/backups/model';
import { AuthField } from '../auth/fields';
import { setupMessage } from './messages';

const steps = ['Setup token', 'Backup file', 'Restore'];

/** Why a checked backup cannot be restored here, or null when it can. */
export function setupBlocker(preview: RestorePreview): string | null {
  if (!preview.key_matches) {
    return 'This backup was made with another secret key. Copy secrets/secret_key from the old installation and restart Scanopsis.';
  }
  if (!preview.schema_ok) {
    return `This backup was made by a newer version of Scanopsis (${preview.info.app_version}). Install that version first, then restore it.`;
  }
  return null;
}

type Upload =
  | { kind: 'choose' }
  | { kind: 'uploading'; file: File; sent: number }
  | { kind: 'interrupted'; file: File; id: string; sent: number }
  | { kind: 'failed'; file: File; message: string; id?: string };

type Step =
  | { kind: 'token'; error?: string }
  | { kind: 'file' }
  | { kind: 'review'; preview: RestorePreview; uploadId: string }
  | { kind: 'already' };

const labelClass = 'text-body-s font-medium text-text';

/**
 * Restores a backup on a new installation, before any administrator exists:
 * the setup token proves the right to do it, typing RESTORE stands in for the
 * two-factor code of the admin panel, and the accounts come from the backup.
 */
export function RestoreFromBackup({ onBack }: { onBack: () => void }) {
  const queryClient = useQueryClient();
  const [token, setToken] = useState('');
  const [step, setStep] = useState<Step>({ kind: 'token' });
  const [upload, setUpload] = useState<Upload>({ kind: 'choose' });
  const controller = useRef<AbortController | null>(null);
  const input = useRef<HTMLInputElement>(null);

  // Leaving the screen cancels an upload in flight.
  useEffect(() => () => controller.current?.abort(), []);

  const cleanToken = token.trim();
  const endpoints = restUploadEndpoints<RestorePreview>({
    base: '/api/v1/setup/restore/uploads',
    headers: { 'X-Setup-Token': cleanToken },
    startBody: (file) => ({ token: cleanToken, file_name: file.name, size: file.size }),
    completeBody: { token: cleanToken },
  });

  /** An answer that ends the restore path: a wrong token, or a setup finished meanwhile. */
  function handleCommon(err: unknown): boolean {
    if (err instanceof ApiError && err.code === 'setup_done') {
      setStep({ kind: 'already' });
      return true;
    }
    if (err instanceof ApiError && err.code === 'invalid_setup_token') {
      setUpload({ kind: 'choose' });
      setStep({ kind: 'token', error: setupMessage(err) });
      return true;
    }
    return false;
  }

  async function run(file: File, resume?: { id: string; sent: number }) {
    const abort = new AbortController();
    controller.current = abort;
    let id = resume?.id;
    setUpload({ kind: 'uploading', file, sent: resume?.sent ?? 0 });
    try {
      const done = await uploadInChunks(
        file,
        endpoints,
        (sent) => setUpload({ kind: 'uploading', file, sent }),
        abort.signal,
        {
          resumeId: resume?.id,
          resumeReceived: resume?.sent,
          onStarted: (started) => {
            id = started;
          },
        },
      );
      controller.current = null;
      setUpload({ kind: 'choose' });
      setStep({ kind: 'review', preview: done.result, uploadId: done.id });
    } catch (err) {
      controller.current = null;
      if (abort.signal.aborted) return;
      if (err instanceof UploadInterruptedError) {
        setUpload({ kind: 'interrupted', file, id: err.id, sent: err.received });
        return;
      }
      if (handleCommon(err)) return;
      setUpload({ kind: 'failed', file, message: setupMessage(err), id });
    }
  }

  function choose(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    event.target.value = '';
    if (file) void run(file);
  }

  /** Frees an upload the server still holds; it would drop it after an hour anyway. */
  function release() {
    if ((upload.kind === 'interrupted' || upload.kind === 'failed') && upload.id) {
      void cancelUpload(endpoints, upload.id);
    }
  }

  function stopUpload() {
    controller.current?.abort();
    controller.current = null;
    release();
    setUpload({ kind: 'choose' });
  }

  if (step.kind === 'already') {
    return (
      <AuthShell title="Already set up" focusHeading>
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

  if (step.kind === 'review') {
    return (
      <Review
        preview={step.preview}
        token={cleanToken}
        uploadId={step.uploadId}
        onFailure={(err) => {
          if (handleCommon(err)) return true;
          return false;
        }}
        onBack={() => {
          setUpload({ kind: 'choose' });
          setStep({ kind: 'file' });
        }}
      />
    );
  }

  if (step.kind === 'file') {
    const uploading = upload.kind === 'uploading';
    const checking = uploading && upload.sent >= upload.file.size;
    return (
      <AuthShell
        title="Choose the backup file"
        lead="A backup file from the old installation. It is checked in full before anything is restored."
        steps={{ labels: steps, current: 1 }}
      >
        <input
          ref={input}
          type="file"
          accept=".gz,.tgz,application/gzip"
          aria-label="Backup file"
          onChange={choose}
          className="sr-only"
          tabIndex={-1}
        />
        {upload.kind === 'choose' ? (
          <div
            onDragOver={(event) => event.preventDefault()}
            onDrop={(event) => {
              event.preventDefault();
              const file = event.dataTransfer.files[0];
              if (file) void run(file);
            }}
            className="flex flex-col items-center gap-2 rounded-inner border border-dashed border-line-strong bg-sunken px-4 py-8 text-center"
          >
            <FileArchive
              size={28}
              strokeWidth={1.5}
              aria-hidden="true"
              className="text-text-muted"
            />
            <p className="text-body-s text-text">Drop the backup file here, or</p>
            <button
              type="button"
              className={buttonClass('secondary', 'sm')}
              onClick={() => input.current?.click()}
            >
              Choose a file
            </button>
            <p className="mt-1 text-small text-text-muted">
              A scanopsis-backup file ending in .tar.gz
            </p>
          </div>
        ) : (
          <div className="flex flex-col gap-4">
            <p className="flex min-w-0 items-center gap-2 text-body-s text-text">
              <FileArchive
                size={16}
                strokeWidth={1.75}
                aria-hidden="true"
                className="shrink-0 text-text-muted"
              />
              <span className="truncate font-mono">{upload.file.name}</span>
            </p>
            {upload.kind !== 'failed' ? (
              <Progress sent={upload.sent} total={upload.file.size} />
            ) : null}
            <div aria-live="polite">
              {checking ? (
                <p className="flex items-center gap-2 text-body-s text-text-muted">
                  <Spinner /> Checking the backup. This reads the whole file.
                </p>
              ) : null}
              {upload.kind === 'interrupted' ? (
                <Problem>
                  The connection was lost at {formatBytes(upload.sent)} of{' '}
                  {formatBytes(upload.file.size)}. Check your connection, then resume the upload.
                  The parts already sent are kept for an hour.
                </Problem>
              ) : null}
              {upload.kind === 'failed' ? <Problem>{upload.message}</Problem> : null}
            </div>
          </div>
        )}
        <div className="flex flex-wrap gap-2">
          {upload.kind === 'interrupted' ? (
            <button
              type="button"
              className={buttonClass('primary')}
              onClick={() => void run(upload.file, { id: upload.id, sent: upload.sent })}
            >
              Resume upload
            </button>
          ) : null}
          {upload.kind === 'failed' ? (
            <button
              type="button"
              className={buttonClass('primary')}
              onClick={() => {
                release();
                setUpload({ kind: 'choose' });
                input.current?.click();
              }}
            >
              Choose another file
            </button>
          ) : null}
          {upload.kind === 'choose' ? (
            <button
              type="button"
              className={buttonClass('ghost')}
              onClick={() => setStep({ kind: 'token' })}
            >
              Back
            </button>
          ) : (
            <button type="button" className={buttonClass('secondary')} onClick={stopUpload}>
              {upload.kind === 'failed' ? 'Start over' : 'Cancel upload'}
            </button>
          )}
        </div>
      </AuthShell>
    );
  }

  function next(event: FormEvent) {
    event.preventDefault();
    if (cleanToken) setStep({ kind: 'file' });
  }

  return (
    <AuthShell
      title="Restore from a backup"
      lead="Move Scanopsis to this host with a backup from the old one. The accounts come from the backup."
      steps={{ labels: steps, current: 0 }}
    >
      <form onSubmit={next} className="flex flex-col gap-4">
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
          <button type="submit" className={buttonClass('primary', 'lg')}>
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
  onFailure,
}: {
  preview: RestorePreview;
  token: string;
  uploadId: string;
  onBack: () => void;
  /** Handles a wrong token or a finished setup; true when it did. */
  onFailure: (err: unknown) => boolean;
}) {
  const [phrase, setPhrase] = useState('');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string>();
  const id = useId();
  const blocker = setupBlocker(preview);
  const warning = retentionWarning(preview);
  const date = backupDate(preview.info);
  const ready = !blocker && phrase === CONFIRM_PHRASE;

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
      showRestoreOverlay();
    } catch (err) {
      setPending(false);
      if (!onFailure(err)) setError(setupMessage(err));
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
        <Fact term="Secret key">
          {preview.key_matches
            ? 'The same secret key as this installation'
            : 'Another secret key than this installation'}
        </Fact>
        <Fact term="File">
          <span className="font-mono break-all">
            {preview.info.original_name || preview.info.name}
          </span>{' '}
          ({formatBytes(preview.info.size)})
        </Fact>
      </dl>
      {blocker ? (
        <>
          <Problem>{blocker}</Problem>
          <div>
            <button type="button" className={buttonClass('secondary')} onClick={onBack}>
              Choose another file
            </button>
          </div>
        </>
      ) : (
        <form onSubmit={(e) => void submit(e)} noValidate className="flex flex-col gap-5">
          <ul className="flex list-disc flex-col gap-1.5 pl-5 text-body-s text-text-muted marker:text-text-faint">
            <li>
              Scanopsis takes over everything in this backup: users, settings, services, scans and
              the audit log.
            </li>
            <li>Everything that changed after {date} is not in it.</li>
            <li>Sign in afterwards with an account from the backup.</li>
            <li>
              Pipeline uploads are turned away while the restore runs. Scans uploaded in that time
              are not kept.
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
      )}
    </AuthShell>
  );
}
