// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { FileArchive } from 'lucide-react';
import { useCancelUpload, type OpenUpload } from '../../../api/backups';
import { describeError } from '../../../api/errors';
import { Spinner } from '../../../components/ConfirmDialog';
import { toast } from '../../../components/Toast';
import { formatBytes, relativeTime } from '../../../format';
import { buttonClass } from '../../../ui';
import { Notice } from './notices';

function UploadRow({ upload }: { upload: OpenUpload }) {
  const cancel = useCancelUpload();
  const when = relativeTime(upload.updated_at);
  const state =
    upload.state === 'checking'
      ? `Being checked, since ${when}`
      : `${formatBytes(upload.received)} of ${formatBytes(upload.size)} received, last part ${when}`;
  return (
    <li className="flex flex-col gap-2 py-2.5 first:pt-0 last:pb-0 sm:flex-row sm:items-center sm:gap-4">
      <div className="flex min-w-0 flex-1 items-start gap-2">
        <FileArchive
          size={16}
          strokeWidth={1.75}
          aria-hidden="true"
          className="mt-0.5 shrink-0 text-text-muted"
        />
        <div className="min-w-0">
          <p className="truncate font-mono text-body-s text-text" title={upload.file_name}>
            {upload.file_name}
          </p>
          <p className="text-small text-text-muted tabular-nums">{state}</p>
        </div>
      </div>
      <button
        type="button"
        className={`${buttonClass('secondary', 'sm')} self-start sm:self-center`}
        aria-label={`Cancel the upload of ${upload.file_name}`}
        disabled={cancel.isPending}
        aria-busy={cancel.isPending || undefined}
        onClick={() =>
          cancel.mutate(upload.id, {
            onSuccess: () =>
              toast.info('Upload cancelled', {
                description: `The parts of ${upload.file_name} received so far were deleted.`,
              }),
            onError: (error) =>
              toast.error('The upload could not be cancelled', {
                description: describeError(error),
              }),
          })
        }
      >
        {cancel.isPending ? <Spinner /> : null}
        Cancel upload
      </button>
    </li>
  );
}

/**
 * The uploads the server holds open, such as one whose page was closed while
 * it was sent: at most two can be open, so a left-behind one would block a
 * new upload until it has been idle for ten minutes. Each can be cancelled.
 */
export function UnfinishedUploads({ uploads }: { uploads: OpenUpload[] }) {
  if (uploads.length === 0) return null;
  return (
    <Notice label="Unfinished uploads" status="none" title="Unfinished uploads">
      <p>
        These uploads were started but not finished, for example because the page was closed. At
        most two can be open at once. Cancel one you do not need any more; if it is still being sent
        from another tab, that upload stops.
      </p>
      <ul className="mt-3 divide-y divide-line text-text">
        {uploads.map((upload) => (
          <UploadRow key={upload.id} upload={upload} />
        ))}
      </ul>
    </Notice>
  );
}
