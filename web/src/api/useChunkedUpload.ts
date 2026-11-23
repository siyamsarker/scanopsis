// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { useEffect, useRef, useState } from 'react';
import { describeError } from './errors';
import {
  cancelUpload,
  followUpload,
  PAGE_LEFT,
  uploadInChunks,
  UploadInterruptedError,
  type UploadEndpoints,
  type UploadResult,
} from './upload';

/** The name and size of the file being uploaded, which is all a followed upload knows of it. */
export interface UploadFile {
  name: string;
  size: number;
}

/** Why an empty file is not sent: no backup is empty. */
export const EMPTY_FILE = 'The file is empty, so it is not a backup. Choose another file.';

export type UploadPhase =
  | { kind: 'idle' }
  | { kind: 'uploading'; file: UploadFile; sent: number }
  | { kind: 'interrupted'; file: File; id: string; sent: number; checkStopped: boolean }
  | { kind: 'failed'; file: UploadFile; message: string; id?: string };

export interface ChunkedUploadOptions<R> {
  /** The upload finished and the server checked the file. */
  onDone: (result: R, id: string, file: UploadFile) => void;
  /** Handles an error itself (returns true), or leaves it to be shown as a failed upload. */
  onError?: (err: unknown) => boolean;
  /** The words for a failed upload; the generic error message by default. */
  describe?: (err: unknown) => string;
  /** Every byte was sent and the server checks the file; called once per upload. */
  onChecking?: (id: string, file: UploadFile) => void;
  /** The upload was cancelled after every byte was sent: the server may still have accepted it. */
  onCancelledWhileChecking?: () => void;
}

/**
 * The state of one file upload in parts: starting it, resuming after a lost
 * connection, following the check of an upload sent earlier, cancelling it
 * and freeing what the server still holds. When the component goes away, an
 * upload still sending is cancelled; one whose every byte was sent is left
 * to its check on the server, which keeps the outcome.
 */
export function useChunkedUpload<R>(
  endpoints: UploadEndpoints<R>,
  options: ChunkedUploadOptions<R>,
) {
  const [phase, setPhase] = useState<UploadPhase>({ kind: 'idle' });
  const controller = useRef<AbortController | null>(null);
  const input = useRef<HTMLInputElement>(null);
  // The latest endpoints and callbacks, which may change while an upload runs.
  const latest = useRef({ endpoints, options });
  useEffect(() => {
    latest.current = { endpoints, options };
  });

  useEffect(() => () => controller.current?.abort(PAGE_LEFT), []);

  // Leaving the page while parts are being sent, or with an upload that can
  // still be resumed, gives that upload up: the browser asks first. Once
  // every byte was sent, the server checks the file without the page.
  const unsent =
    (phase.kind === 'uploading' && phase.sent < phase.file.size) || phase.kind === 'interrupted';
  useEffect(() => {
    if (!unsent) return;
    function ask(event: BeforeUnloadEvent) {
      event.preventDefault();
      // Older browsers ask only when returnValue is set.
      event.returnValue = '';
    }
    window.addEventListener('beforeunload', ask);
    return () => window.removeEventListener('beforeunload', ask);
  }, [unsent]);

  /** Runs one upload or follow and turns its end into a phase. */
  async function settle(
    file: UploadFile,
    work: (signal: AbortSignal, started: (id: string) => void) => Promise<UploadResult<R>>,
    progress: { sentAll: boolean; id?: string },
  ) {
    const abort = new AbortController();
    controller.current = abort;
    try {
      const done = await work(abort.signal, (started) => {
        progress.id = started;
      });
      controller.current = null;
      setPhase({ kind: 'idle' });
      latest.current.options.onDone(done.result, done.id, file);
    } catch (err) {
      if (controller.current === abort) controller.current = null;
      if (abort.signal.aborted) {
        if (abort.signal.reason === PAGE_LEFT) return;
        if (progress.sentAll) latest.current.options.onCancelledWhileChecking?.();
        return;
      }
      if (err instanceof UploadInterruptedError && file instanceof File) {
        setPhase({
          kind: 'interrupted',
          file,
          id: err.id,
          sent: err.received,
          checkStopped: err.checkStopped,
        });
        return;
      }
      if (err instanceof UploadInterruptedError) {
        // A followed upload: without the file, only a new upload can go on.
        setPhase({
          kind: 'failed',
          file,
          id: err.id,
          message: err.checkStopped
            ? 'The check of the file stopped before it ended, for example because Scanopsis restarted. Choose the file again to upload it again.'
            : 'The connection to the server was lost while the file was checked. Check your connection, then reload the page to ask again.',
        });
        return;
      }
      if (latest.current.options.onError?.(err)) {
        setPhase({ kind: 'idle' });
        return;
      }
      const describe = latest.current.options.describe ?? describeError;
      setPhase({ kind: 'failed', file, message: describe(err), id: progress.id });
    }
  }

  function run(file: File, resume?: { id: string; sent: number }) {
    if (file.size === 0) {
      setPhase({ kind: 'failed', file, message: EMPTY_FILE });
      return;
    }
    const progress: { sentAll: boolean; id?: string } = { sentAll: false, id: resume?.id };
    setPhase({ kind: 'uploading', file, sent: resume?.sent ?? 0 });
    return settle(
      file,
      (signal, started) =>
        uploadInChunks(
          file,
          latest.current.endpoints,
          (sent) => {
            setPhase({ kind: 'uploading', file, sent });
            if (sent >= file.size && !progress.sentAll && progress.id) {
              progress.sentAll = true;
              latest.current.options.onChecking?.(progress.id, file);
            }
          },
          signal,
          { resumeId: resume?.id, resumeReceived: resume?.sent, onStarted: started },
        ),
      progress,
    );
  }

  /** Frees an upload the server still holds; it would drop it after an hour anyway. */
  function release() {
    if ((phase.kind === 'interrupted' || phase.kind === 'failed') && phase.id) {
      void cancelUpload(latest.current.endpoints, phase.id);
    }
  }

  return {
    phase,
    input,
    start: (file: File) => void run(file),
    resume: () => {
      if (phase.kind === 'interrupted') void run(phase.file, { id: phase.id, sent: phase.sent });
    },
    /**
     * Follows the check of an upload whose every byte was sent earlier, such
     * as before the page was reloaded, until it has ended.
     */
    follow: (id: string, file: UploadFile) => {
      setPhase({ kind: 'uploading', file, sent: file.size });
      void settle(file, (signal) => followUpload(id, file.size, latest.current.endpoints, signal), {
        sentAll: true,
        id,
      });
    },
    /** Opens the file chooser. */
    pick: () => input.current?.click(),
    /** Gives up the file and opens the chooser for another one. */
    chooseAnother: () => {
      release();
      setPhase({ kind: 'idle' });
      input.current?.click();
    },
    /** Stops the upload and frees it on the server. True when the server was already checking the file. */
    cancel: (): boolean => {
      const checking = phase.kind === 'uploading' && phase.sent >= phase.file.size;
      controller.current?.abort();
      controller.current = null;
      release();
      setPhase({ kind: 'idle' });
      return checking;
    },
  };
}

export type ChunkedUpload = ReturnType<typeof useChunkedUpload>;
