// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { FileArchive } from 'lucide-react';
import { useEffect, useRef, useState, type ChangeEvent } from 'react';
import type { ChunkedUpload } from '../api/useChunkedUpload';
import { formatBytes } from '../format';
import { Problem } from '../routes/admin/kit';
import { buttonClass } from '../ui';
import { Spinner } from './ConfirmDialog';

function Progress({ sent, total }: { sent: number; total: number }) {
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
 * The body of a backup upload: the drop zone while no file is chosen, then
 * the file name with its progress, the check of the file and any problem. The
 * buttons that act on it (resume, cancel) belong to the caller, since a dialog
 * and a page place them differently.
 */
export function BackupFileDrop({
  upload,
  note,
}: {
  upload: ChunkedUpload;
  /** A line under the drop zone, for a file chosen earlier. */
  note?: string;
}) {
  const { phase, input, start, pick } = upload;
  const [over, setOver] = useState(false);
  const status = useRef<HTMLDivElement>(null);
  const uploading = phase.kind === 'uploading';
  const started = useRef(false);

  // The progress takes the focus when an upload starts, so the drop zone's
  // button does not leave focus on nothing.
  useEffect(() => {
    if (uploading && !started.current) status.current?.focus();
    started.current = uploading;
  }, [uploading]);

  function choose(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    event.target.value = '';
    if (file) start(file);
  }

  const checking = phase.kind === 'uploading' && phase.sent >= phase.file.size;
  return (
    <>
      <input
        ref={input}
        type="file"
        accept=".gz,.tgz,application/gzip"
        aria-label="Backup file"
        onChange={choose}
        className="sr-only"
        tabIndex={-1}
      />
      {phase.kind === 'idle' ? (
        <div
          onDragOver={(event) => {
            event.preventDefault();
            setOver(true);
          }}
          onDragLeave={() => setOver(false)}
          onDrop={(event) => {
            event.preventDefault();
            setOver(false);
            const file = event.dataTransfer.files[0];
            if (file) start(file);
          }}
          data-over={over || undefined}
          className="flex flex-col items-center gap-2 rounded-inner border border-dashed border-line-strong bg-sunken px-4 py-8 text-center transition-colors data-[over]:border-brand data-[over]:bg-wash"
        >
          <FileArchive size={28} strokeWidth={1.5} aria-hidden="true" className="text-text-muted" />
          <p className="text-body-s text-text">Drop the backup file here, or</p>
          <button type="button" className={buttonClass('secondary', 'sm')} onClick={pick}>
            Choose a file
          </button>
          <p className="mt-1 text-small text-text-muted">
            A scanopsis-backup file ending in .tar.gz
          </p>
          {note ? <p className="text-small text-text-muted">{note}</p> : null}
        </div>
      ) : (
        <div ref={status} tabIndex={-1} className="flex flex-col gap-4 outline-none">
          <p className="flex min-w-0 items-center gap-2 text-body-s text-text">
            <FileArchive
              size={16}
              strokeWidth={1.75}
              aria-hidden="true"
              className="shrink-0 text-text-muted"
            />
            <span className="truncate font-mono">{phase.file.name}</span>
          </p>
          {phase.kind !== 'failed' ? <Progress sent={phase.sent} total={phase.file.size} /> : null}
          <div aria-live="polite">
            {checking ? (
              <p className="flex items-center gap-2 text-body-s text-text-muted">
                <Spinner /> Checking the backup. This reads the whole file, which takes several
                minutes for a large one.
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
    </>
  );
}
