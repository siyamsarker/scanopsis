// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { useEffect, useRef, useState } from 'react';
import { describeError } from './errors';
import {
  cancelUpload,
  uploadInChunks,
  UploadInterruptedError,
  type UploadEndpoints,
} from './upload';

export type UploadPhase =
  | { kind: 'idle' }
  | { kind: 'uploading'; file: File; sent: number }
  | { kind: 'interrupted'; file: File; id: string; sent: number }
  | { kind: 'failed'; file: File; message: string; id?: string };

export interface ChunkedUploadOptions<R> {
  /** The upload finished and the server checked the file. */
  onDone: (result: R, id: string, file: File) => void;
  /** Handles an error itself (returns true), or leaves it to be shown as a failed upload. */
  onError?: (err: unknown) => boolean;
  /** The words for a failed upload; the generic error message by default. */
  describe?: (err: unknown) => string;
  /** The upload was cancelled after every byte was sent: the server may still have accepted it. */
  onCancelledWhileChecking?: () => void;
}

/**
 * The state of one file upload in parts: starting it, resuming after a lost
 * connection, cancelling it and freeing what the server still holds. The
 * upload is cancelled when the component goes away.
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

  useEffect(() => () => controller.current?.abort(), []);

  async function run(file: File, resume?: { id: string; sent: number }) {
    const abort = new AbortController();
    controller.current = abort;
    let id = resume?.id;
    let sentAll = false;
    setPhase({ kind: 'uploading', file, sent: resume?.sent ?? 0 });
    try {
      const done = await uploadInChunks(
        file,
        latest.current.endpoints,
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
      setPhase({ kind: 'idle' });
      latest.current.options.onDone(done.result, done.id, file);
    } catch (err) {
      controller.current = null;
      if (abort.signal.aborted) {
        if (sentAll) latest.current.options.onCancelledWhileChecking?.();
        return;
      }
      if (err instanceof UploadInterruptedError) {
        setPhase({ kind: 'interrupted', file, id: err.id, sent: err.received });
        return;
      }
      if (latest.current.options.onError?.(err)) {
        setPhase({ kind: 'idle' });
        return;
      }
      const describe = latest.current.options.describe ?? describeError;
      setPhase({ kind: 'failed', file, message: describe(err), id });
    }
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
