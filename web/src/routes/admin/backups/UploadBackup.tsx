// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { useQueryClient } from '@tanstack/react-query';
import { Upload } from 'lucide-react';
import { useState } from 'react';
import { adminUploadEndpoints, backupsKey } from '../../../api/backups';
import { useChunkedUpload } from '../../../api/useChunkedUpload';
import { BackupFileDrop } from '../../../components/BackupFileDrop';
import { Dialog } from '../../../components/Dialog';
import { toast } from '../../../components/Toast';
import { buttonClass } from '../../../ui';

/**
 * "Upload a backup": sends a backup file in parts, resuming after a dropped
 * connection, and adds it to the list once the server has checked it.
 */
export function UploadBackup({ disabled = false }: { disabled?: boolean }) {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const refresh = () => void queryClient.invalidateQueries({ queryKey: backupsKey });
  const upload = useChunkedUpload(adminUploadEndpoints, {
    onDone: (_result, _id, file) => {
      setOpen(false);
      toast.success('Backup uploaded', {
        description: `${file.name} was checked and added to the list.`,
      });
      refresh();
    },
    // Cancelled while the server checked the file: it may still have added it.
    onCancelledWhileChecking: refresh,
  });
  const { phase } = upload;

  function close() {
    if (upload.cancel()) {
      toast.info('Upload cancelled', {
        description:
          'The server was already checking the file, so it may still appear in the list.',
      });
    }
    setOpen(false);
  }

  const uploading = phase.kind === 'uploading';

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
              <button type="button" className={buttonClass('secondary')} onClick={close}>
                Cancel upload
              </button>
              {phase.kind === 'interrupted' ? (
                <button type="button" className={buttonClass('primary')} onClick={upload.resume}>
                  Resume upload
                </button>
              ) : null}
            </>
          )
        }
      >
        <BackupFileDrop upload={upload} />
      </Dialog>
    </>
  );
}
