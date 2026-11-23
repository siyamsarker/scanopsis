// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { useQueryClient } from '@tanstack/react-query';
import { TriangleAlert } from 'lucide-react';
import { useId, useRef, useState, type FormEvent, type RefObject } from 'react';
import { useRestoreBackup, useRestorePreview, type BackupInfo } from '../../../api/backups';
import { describeError } from '../../../api/errors';
import { meKey } from '../../../api/hooks';
import { showRestoreOverlay } from '../../../api/restoreMode';
import { Spinner } from '../../../components/ConfirmDialog';
import { Dialog } from '../../../components/Dialog';
import { SkeletonText } from '../../../components/states';
import { buttonClass, monoInputClass } from '../../../ui';
import { Problem } from '../kit';
import {
  backupDate,
  canRestore,
  cleanCode,
  CONFIRM_PHRASE,
  restoreBlocker,
  retentionWarning,
} from './model';

const labelClass = 'text-body-s font-medium text-text';

/**
 * Asks before a restore replaces everything. The server first reads the whole
 * backup (the preview) to say what the restore would do; the restore button
 * waits for the confirmation phrase and a current two-factor code. A started
 * restore hands over to the full-screen restore screen.
 */
export function RestoreDialog({
  backup,
  onClose,
  returnFocusTo,
}: {
  backup: BackupInfo;
  onClose: () => void;
  returnFocusTo?: RefObject<HTMLElement | null>;
}) {
  const preview = useRestorePreview(backup.name);
  const restore = useRestoreBackup();
  const queryClient = useQueryClient();
  const [phrase, setPhrase] = useState('');
  const [code, setCode] = useState('');
  const [skipSafety, setSkipSafety] = useState(false);
  const [codeError, setCodeError] = useState<string>();
  const [error, setError] = useState<string>();
  const cancel = useRef<HTMLButtonElement>(null);
  const id = useId();
  const date = backupDate(backup);
  const blocker = restoreBlocker(backup, preview.data);
  const warning = preview.data ? retentionWarning(preview.data) : null;
  const ready = preview.isSuccess && !blocker && canRestore(phrase, code);
  const pending = restore.isPending;

  function submit(event: FormEvent) {
    event.preventDefault();
    if (!ready || pending) return;
    setError(undefined);
    setCodeError(undefined);
    restore.mutate(
      { name: backup.name, code: cleanCode(code), skipSafetyBackup: skipSafety },
      {
        onSuccess: () => {
          onClose();
          showRestoreOverlay();
        },
        onError: (err) => {
          if (err.status === 401) {
            // Too many wrong codes ended the session: sign in again, as after any 401.
            queryClient.setQueryData(meKey, null);
            return;
          }
          if (err.code === 'invalid_code') {
            setCode('');
            setCodeError(err.message);
            return;
          }
          setError(describeError(err));
        },
      },
    );
  }

  const formId = `${id}-form`;
  return (
    <Dialog
      open
      role="alertdialog"
      hideClose
      modalStrict
      size="lg"
      title={`Restore the backup from ${date}?`}
      onOpenChange={(open) => {
        if (!open && !pending) onClose();
      }}
      onOpenAutoFocus={(event) => {
        event.preventDefault();
        cancel.current?.focus();
      }}
      onCloseAutoFocus={(event) => {
        const target = returnFocusTo?.current;
        if (target) {
          event.preventDefault();
          target.focus();
        }
      }}
      onEscapeKeyDown={(event) => {
        if (pending) event.preventDefault();
      }}
      footer={
        <>
          <button
            ref={cancel}
            type="button"
            className={buttonClass('secondary')}
            disabled={pending}
            onClick={onClose}
          >
            Cancel
          </button>
          <button
            type="submit"
            form={formId}
            className={buttonClass('danger')}
            disabled={!ready || pending}
            aria-busy={pending || undefined}
          >
            {pending ? <Spinner /> : null}
            Restore backup
          </button>
        </>
      }
    >
      {preview.isPending ? (
        <div role="status" aria-live="polite">
          <p className="mb-3 text-body-s text-text-muted">
            Checking the backup. Scanopsis reads the whole file first, which takes a moment for a
            large one.
          </p>
          <SkeletonText lines={4} />
        </div>
      ) : preview.isError ? (
        <Problem>{describeError(preview.error)}</Problem>
      ) : blocker ? (
        <Problem>{blocker}</Problem>
      ) : (
        <form id={formId} onSubmit={submit} noValidate className="flex flex-col gap-5">
          <ul className="flex list-disc flex-col gap-1.5 pl-5 text-body-s text-text-muted marker:text-text-faint">
            <li>
              Everything in Scanopsis is replaced by this backup: users, settings, services, scans
              and the audit log.
            </li>
            <li>Everything that changed after {date} is lost.</li>
            <li>
              Everyone is signed out, including you. Sign in again with an account from the backup.
            </li>
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
          <div className="flex flex-col gap-2">
            <p className="text-body-s text-text-muted">
              Before the restore, Scanopsis backs up the current state, so you can go back.
            </p>
            <label className="flex cursor-pointer items-start gap-3 text-body-s text-text">
              <input
                type="checkbox"
                checked={skipSafety}
                disabled={pending}
                onChange={(e) => setSkipSafety(e.target.checked)}
                className="mt-0.5 size-4 shrink-0 accent-(--brand)"
              />
              Skip the backup of the current state (only if the disk has no room)
            </label>
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
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
            <div className="flex flex-col gap-1.5">
              <label htmlFor={`${id}-code`} className={labelClass}>
                Two-factor code
              </label>
              <input
                id={`${id}-code`}
                type="text"
                inputMode="numeric"
                autoComplete="one-time-code"
                maxLength={7}
                placeholder="000000"
                value={code}
                disabled={pending}
                aria-invalid={codeError ? true : undefined}
                aria-describedby={codeError ? `${id}-code-error` : `${id}-code-hint`}
                onChange={(e) => {
                  setCode(e.target.value.replace(/[^\d ]/g, ''));
                  setCodeError(undefined);
                }}
                className={`${monoInputClass} tracking-[0.2em]`}
              />
              {codeError ? (
                <Problem id={`${id}-code-error`}>{codeError}</Problem>
              ) : (
                <p id={`${id}-code-hint`} className="text-small text-text-muted">
                  From your authenticator app. Recovery codes do not work here.
                </p>
              )}
            </div>
          </div>
          {error ? <Problem>{error}</Problem> : null}
        </form>
      )}
    </Dialog>
  );
}
