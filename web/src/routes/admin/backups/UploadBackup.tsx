// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { useQueryClient } from '@tanstack/react-query';
import { FileArchive, Upload } from 'lucide-react';
import { useEffect, useRef, useState, type ChangeEvent } from 'react';
import { adminUploadEndpoints, backupsKey } from '../../../api/backups';
import { describeError } from '../../../api/errors';
import { cancelUpload, uploadInChunks, UploadInterruptedError } from '../../../api/upload';
import { Spinner } from '../../../components/ConfirmDialog';
import { Dialog } from '../../../components/Dialog';
import { toast } from '../../../components/Toast';
import { formatBytes } from '../../../format';
import { buttonClass } from '../../../ui';
import { Problem } from '../kit';

type Phase =
  | { kind: 'choose' }
  | { kind: 'uploading'; file: File; sent: number }
  | { kind: 'interrupted'; file: File; id: string; sent: number }
  | { kind: 'failed'; file: File; message: string; id?: string };

export function Progress({ sent, total }: { sent: number; total: number }) {
  const percent = total > 0 ? Math.round((sent / total) * 100) : 0;
  return (
    <div>
      <div
        role="progressbar"
        aria-label="Upload progress"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={percent}
        className="h-1.5 w-full overflow-hidden rounded-full bg-sunken shadow-[inset_0_0_0_1px_var(--line)]"
      >
        <div
          className="h-full rounded-full bg-brand transition-[width] duration-layout ease-enter"
          style={{ width: `${percent}%` }}
        />
      </div>
      <p className="mt-2 flex justify-between gap-4 text-small text-text-muted tabular-nums">
        <span>
          {formatBytes(sent)} of {formatBytes(total)}
        </span>
        <span>{percent}%</span>
      </p>
    </div>
  );
}

/**
 * "Upload a backup": sends a backup file in parts, resuming after a dropped
 * connection, and adds it to the list once the server has checked it.
 */
export function UploadBackup({ disabled = false }: { disabled?: boolean }) {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [phase, setPhase] = useState<Phase>({ kind: 'choose' });
  const controller = useRef<AbortController | null>(null);
  const input = useRef<HTMLInputElement>(null);

  // Leaving the page cancels an upload in flight.
  useEffect(() => () => controller.current?.abort(), []);

  async function run(file: File, resume?: { id: string; sent: number }) {
    const abort = new AbortController();
    controller.current = abort;
    let id = resume?.id;
    let sentAll = false;
    setPhase({ kind: 'uploading', file, sent: resume?.sent ?? 0 });
    try {
      await uploadInChunks(
        file,
        adminUploadEndpoints,
        (sent) => {
          sentAll = sent >= file.size;
          setPhase({ kind: 'uploading', file, sent });
        },
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
      setOpen(false);
      setPhase({ kind: 'choose' });
      toast.success('Backup uploaded', {
        description: `${file.name} was checked and added to the list.`,
      });
      void queryClient.invalidateQueries({ queryKey: backupsKey });
    } catch (err) {
      controller.current = null;
      if (abort.signal.aborted) {
        // Cancelled while the server checked the file: it may still have added it.
        if (sentAll) void queryClient.invalidateQueries({ queryKey: backupsKey });
        return;
      }
      if (err instanceof UploadInterruptedError) {
        setPhase({ kind: 'interrupted', file, id: err.id, sent: err.received });
        return;
      }
      setPhase({ kind: 'failed', file, message: describeError(err), id });
    }
  }

  function choose(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    event.target.value = '';
    if (file) void run(file);
  }

  /** Frees an upload the server still holds; it would drop it after an hour anyway. */
  function release() {
    if ((phase.kind === 'interrupted' || phase.kind === 'failed') && phase.id) {
      void cancelUpload(adminUploadEndpoints, phase.id);
    }
  }

  function close() {
    if (phase.kind === 'uploading' && phase.sent >= phase.file.size) {
      toast.info('Upload cancelled', {
        description:
          'The server was already checking the file, so it may still appear in the list.',
      });
    }
    controller.current?.abort();
    controller.current = null;
    release();
    setOpen(false);
    setPhase({ kind: 'choose' });
  }

  const uploading = phase.kind === 'uploading';
  const checking = uploading && phase.sent >= phase.file.size;

  return (
    <>
      <button
        type="button"
        className={buttonClass('secondary')}
        disabled={disabled}
        onClick={() => setOpen(true)}
      >
        <Upload size={16} strokeWidth={1.75} aria-hidden="true" />
        Upload a backup
      </button>
      <Dialog
        open={open}
        onOpenChange={(next) => {
          if (!next) close();
        }}
        title="Upload a backup"
        description="A backup file from this Scanopsis, or from another one with the same secret key. It is checked in full, then added to the list."
        hideClose={uploading}
        modalStrict
        onEscapeKeyDown={(event) => {
          if (uploading) event.preventDefault();
        }}
        footer={
          phase.kind === 'choose' ? (
            <button type="button" className={buttonClass('secondary')} onClick={close}>
              Cancel
            </button>
          ) : phase.kind === 'failed' ? (
            <>
              <button
                type="button"
                className={buttonClass('primary')}
                onClick={() => {
                  release();
                  setPhase({ kind: 'choose' });
                  input.current?.click();
                }}
              >
                Choose another file
              </button>
            </>
          ) : (
            <>
              <button type="button" className={buttonClass('secondary')} onClick={close}>
                Cancel upload
              </button>
              {phase.kind === 'interrupted' ? (
                <button
                  type="button"
                  className={buttonClass('primary')}
                  onClick={() => void run(phase.file, { id: phase.id, sent: phase.sent })}
                >
                  Resume upload
                </button>
              ) : null}
            </>
          )
        }
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
        {phase.kind === 'choose' ? (
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
              <span className="truncate font-mono">{phase.file.name}</span>
            </p>
            {phase.kind !== 'failed' ? (
              <Progress sent={phase.sent} total={phase.file.size} />
            ) : null}
            <div aria-live="polite">
              {checking ? (
                <p className="flex items-center gap-2 text-body-s text-text-muted">
                  <Spinner /> Checking the backup. This reads the whole file.
                </p>
              ) : null}
              {phase.kind === 'interrupted' ? (
                <Problem>
                  The connection was lost at {formatBytes(phase.sent)} of{' '}
                  {formatBytes(phase.file.size)}. Check your connection, then resume the upload. The
                  parts already sent are kept for an hour.
                </Problem>
              ) : null}
              {phase.kind === 'failed' ? <Problem>{phase.message}</Problem> : null}
            </div>
          </div>
        )}
      </Dialog>
    </>
  );
}
