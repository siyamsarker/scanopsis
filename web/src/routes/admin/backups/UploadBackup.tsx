// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { useQueryClient } from '@tanstack/react-query';
import { Upload } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { adminUploadEndpoints, backupsKey } from '../../../api/backups';
import { useChunkedUpload } from '../../../api/useChunkedUpload';
import { BackupFileDrop } from '../../../components/BackupFileDrop';
import { Dialog } from '../../../components/Dialog';
import { toast } from '../../../components/Toast';
import { buttonClass } from '../../../ui';

/**
 * "Upload a backup": sends a backup file in parts, resuming after a dropped
 * connection, and adds it to the list once the server has checked it. The
 * dialog can be closed once every byte was sent (the check goes on, and a
 * toast says when the backup is listed) and while the upload is interrupted
 * (opening it again offers to resume); only Cancel upload gives it up.
 */
export function UploadBackup({ disabled = false }: { disabled?: boolean }) {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const openRef = useRef(open);
  useEffect(() => {
    openRef.current = open;
  }, [open]);
  const choose = useRef<HTMLButtonElement>(null);
  const refresh = () => void queryClient.invalidateQueries({ queryKey: backupsKey });
  const upload = useChunkedUpload(adminUploadEndpoints, {
    onDone: (_result, _id, file) => {
      setOpen(false);
      toast.success('Backup uploaded', {
        description: `${file.name} was checked and added to the list.`,
      });
      refresh();
    },
    // Cancelled while the server checked the file: the check may have ended first.
    onCancelledWhileChecking: refresh,
    // Cancelled elsewhere (Unfinished uploads, another tab): whoever did it
    // was told; a closed dialog says nothing more.
    onCancelledElsewhere: () => {
      refresh();
      return !openRef.current;
    },
  });
  const { phase } = upload;

  // A check that ends while the dialog is closed says so.
  useEffect(() => {
    if (phase.kind === 'failed' && !openRef.current) {
      toast.error('The uploaded file was not added', { description: phase.message });
    }
  }, [phase]);

  function cancel() {
    if (upload.cancel()) {
      toast.info('Upload cancelled', {
        description:
          'The check of the file was stopped. If it had already finished, the backup stays in the list.',
      });
    }
    setOpen(false);
  }

  const sending = phase.kind === 'uploading' && phase.sent < phase.file.size;
  const checking = phase.kind === 'uploading' && !sending;

  /** Closing keeps an upload the server checks or one that can resume; a failed one is freed. */
  function close() {
    if (sending) return;
    if (phase.kind === 'failed') upload.cancel();
    setOpen(false);
  }

  return (
    <>
      <button
        type="button"
        className={buttonClass('secondary')}
        disabled={disabled && phase.kind === 'idle'}
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
        hideClose={sending}
        modalStrict
        onOpenAutoFocus={(event) => {
          if (phase.kind !== 'idle' || !choose.current) return;
          event.preventDefault();
          choose.current.focus();
        }}
        onEscapeKeyDown={(event) => {
          if (sending) event.preventDefault();
        }}
        footer={
          phase.kind === 'idle' ? (
            <button type="button" className={buttonClass('secondary')} onClick={close}>
              Cancel
            </button>
          ) : phase.kind === 'failed' ? (
            <button type="button" className={buttonClass('primary')} onClick={upload.chooseAnother}>
              Choose another file
            </button>
          ) : (
            <>
              <button type="button" className={buttonClass('secondary')} onClick={cancel}>
                Cancel upload
              </button>
              {checking ? (
                <button type="button" className={buttonClass('primary')} onClick={close}>
                  Close and keep checking
                </button>
              ) : null}
              {phase.kind === 'interrupted' ? (
                <button type="button" className={buttonClass('primary')} onClick={upload.resume}>
                  Resume upload
                </button>
              ) : null}
            </>
          )
        }
      >
        <BackupFileDrop upload={upload} chooseRef={choose} />
      </Dialog>
    </>
  );
}
