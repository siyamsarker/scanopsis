// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { ApiError, toApiError } from './client';

/*
 * Uploads a large file (a backup) in parts the server stores one after the
 * other. Only one part is read from the file at a time, so a file of many
 * gigabytes never sits in memory. A dropped connection does not start over:
 * the server says how much it holds, and the upload continues from there.
 */

/** What the server knows of an open upload. */
export interface UploadStatus {
  id: string;
  file_name: string;
  size: number;
  /** The largest part the server takes. */
  chunk_size: number;
  /** Bytes stored so far: where the next part starts. */
  received: number;
}

/** The requests of one upload API (the admin one or the setup screen's). */
export interface UploadEndpoints<R> {
  start(file: File, signal: AbortSignal): Promise<UploadStatus>;
  status(id: string, signal: AbortSignal): Promise<UploadStatus>;
  put(id: string, offset: number, part: Blob, signal: AbortSignal): Promise<UploadStatus>;
  complete(id: string, signal: AbortSignal): Promise<R>;
  cancel(id: string): Promise<void>;
}

export interface UploadResult<R> {
  /** The upload's id, which the setup screen restores by. */
  id: string;
  /** What the server answered to the completed upload. */
  result: R;
}

export interface UploadOptions {
  /** Continue this upload (after an UploadInterruptedError) instead of starting one. */
  resumeId?: string;
  /** Bytes the server was known to hold when the upload stopped, for a resume that fails again. */
  resumeReceived?: number;
  /** Called with the upload's id once the server has opened it, so a caller can cancel it later. */
  onStarted?: (id: string) => void;
  /** Waits before each retry after a network error; their number is the number of retries. */
  retryDelaysMs?: number[];
  /** Wait before asking a busy server again when it names no wait itself. */
  busyDelayMs?: number;
}

/**
 * The connection failed more often than the upload retries. The server keeps
 * the parts it has (for an hour without a new one), so passing `id` as
 * `resumeId` continues where it stopped.
 */
export class UploadInterruptedError extends Error {
  readonly id: string;
  readonly received: number;
  readonly size: number;

  constructor(id: string, received: number, size: number) {
    super('The connection to the server was lost during the upload.');
    this.name = 'UploadInterruptedError';
    this.id = id;
    this.received = received;
    this.size = size;
  }
}

const RETRY_DELAYS_MS = [1000, 3000, 9000];
export const BUSY_DELAY_MS = 2000;
/** How often a busy server is asked again before the upload gives up. */
const BUSY_TRIES = 15;

function abortError() {
  return new DOMException('The upload was cancelled.', 'AbortError');
}

function wait(ms: number, signal: AbortSignal) {
  return new Promise<void>((resolve, reject) => {
    if (signal.aborted) {
      reject(abortError());
      return;
    }
    const timer = setTimeout(() => {
      signal.removeEventListener('abort', onAbort);
      resolve();
    }, ms);
    function onAbort() {
      clearTimeout(timer);
      reject(abortError());
    }
    signal.addEventListener('abort', onAbort, { once: true });
  });
}

/** 503 busy (the server's request limit) or 409 upload_busy (it is still finishing a part). */
function isBusy(err: unknown): err is ApiError {
  return (
    err instanceof ApiError &&
    ((err.status === 503 && err.code === 'busy') ||
      (err.status === 409 && err.code === 'upload_busy'))
  );
}

/**
 * A failure that a new attempt may get past: no answer, a timeout, a gateway
 * error, or a 503 other than "restoring" (which raises the restore screen).
 */
function isTransient(err: unknown): boolean {
  if (err instanceof ApiError) {
    return (
      err.status === 408 ||
      err.status === 502 ||
      err.status === 504 ||
      (err.status === 503 && err.code !== 'restoring')
    );
  }
  return err instanceof TypeError;
}

/** Runs a request, asking again while the server answers that it is busy. */
export async function patiently<T>(
  request: () => Promise<T>,
  signal: AbortSignal,
  busyDelayMs: number,
): Promise<T> {
  for (let tries = 1; ; tries++) {
    try {
      return await request();
    } catch (err) {
      if (!isBusy(err) || tries >= BUSY_TRIES || signal.aborted) throw err;
      await wait(err.retryAfter !== null ? err.retryAfter * 1000 : busyDelayMs, signal);
    }
  }
}

/**
 * Uploads `file` in parts of the server's chunk size and completes it. Reports
 * the bytes the server holds after each part. After a network error it asks
 * the server how much arrived and continues from there, up to three times in a
 * row with growing waits; then it throws an UploadInterruptedError that can be
 * resumed. A busy server is asked again without counting as a failure.
 * Aborting `signal` cancels the upload on the server and throws an AbortError.
 */
export async function uploadInChunks<R>(
  file: File,
  endpoints: UploadEndpoints<R>,
  onProgress: (sent: number, total: number) => void,
  signal: AbortSignal,
  options: UploadOptions = {},
): Promise<UploadResult<R>> {
  const delays = options.retryDelaysMs ?? RETRY_DELAYS_MS;
  const busyDelay = options.busyDelayMs ?? BUSY_DELAY_MS;
  let id = options.resumeId;
  let received = 0;

  try {
    let first: UploadStatus;
    if (id) {
      try {
        first = await patiently(() => endpoints.status(id!, signal), signal, busyDelay);
      } catch (err) {
        // Still offline: the upload stays resumable where it was.
        if (!signal.aborted && isTransient(err)) {
          throw new UploadInterruptedError(id, options.resumeReceived ?? 0, file.size);
        }
        throw err;
      }
    } else {
      first = await patiently(() => endpoints.start(file, signal), signal, busyDelay);
      options.onStarted?.(first.id);
    }
    id = first.id;
    received = first.received;
    const chunk = first.chunk_size;
    onProgress(received, file.size);

    let failures = 0;
    let resends = 0;
    for (;;) {
      while (received < file.size) {
        const part = file.slice(received, received + chunk);
        try {
          const status = await patiently(
            () => endpoints.put(id!, received, part, signal),
            signal,
            busyDelay,
          );
          received = status.received;
          failures = 0;
          onProgress(received, file.size);
        } catch (err) {
          if (signal.aborted) throw abortError();
          if (
            err instanceof ApiError &&
            err.code === 'offset_mismatch' &&
            err.received !== undefined &&
            err.received !== received
          ) {
            received = err.received;
            onProgress(received, file.size);
            continue;
          }
          if (!isTransient(err)) throw err;
          failures += 1;
          if (failures > delays.length) throw new UploadInterruptedError(id!, received, file.size);
          await wait(delays[failures - 1], signal);
          try {
            received = (await patiently(() => endpoints.status(id!, signal), signal, busyDelay))
              .received;
            onProgress(received, file.size);
          } catch (statusErr) {
            if (signal.aborted) throw abortError();
            if (!isTransient(statusErr)) throw statusErr;
            // Still offline: the next part fails too and counts as the next retry.
          }
        }
      }
      try {
        const result = await patiently(() => endpoints.complete(id!, signal), signal, busyDelay);
        return { id: id!, result };
      } catch (err) {
        // The server is missing bytes (a part was lost after it answered): send them.
        if (
          err instanceof ApiError &&
          err.code === 'upload_incomplete' &&
          err.received !== undefined &&
          err.received < received &&
          resends < delays.length
        ) {
          resends += 1;
          received = err.received;
          continue;
        }
        if (isTransient(err) && !signal.aborted) {
          throw new UploadInterruptedError(id!, received, file.size);
        }
        throw err;
      }
    }
  } catch (err) {
    if (signal.aborted && id) await cancelUpload(endpoints, id, busyDelay);
    if (signal.aborted) throw abortError();
    throw err;
  }
}

/**
 * Cancels an open upload, asking a busy server again. Best effort: an upload
 * nobody completes is dropped after an hour anyway, so a failure is ignored.
 */
export async function cancelUpload<R>(
  endpoints: UploadEndpoints<R>,
  id: string,
  busyDelayMs = BUSY_DELAY_MS,
): Promise<void> {
  const never = new AbortController().signal;
  await patiently(() => endpoints.cancel(id), never, busyDelayMs).catch(() => undefined);
}

export interface RestUploadOptions {
  /** The collection path, such as /api/v1/admin/backup-uploads. */
  base: string;
  /** Sent with every request (the setup screen's X-Setup-Token). */
  headers?: Record<string, string>;
  /** The JSON body that starts an upload; file name and size by default. */
  startBody?: (file: File) => Record<string, unknown>;
  /** The JSON body of the completing request; none by default. */
  completeBody?: Record<string, unknown>;
}

/** The endpoints of an upload API that follows the backup upload routes. */
export function restUploadEndpoints<R>(options: RestUploadOptions): UploadEndpoints<R> {
  const origin = globalThis.location?.origin ?? '';
  const headers = options.headers ?? {};

  async function send<T>(
    method: string,
    path: string,
    body?: BodyInit,
    signal?: AbortSignal,
    type?: string,
  ): Promise<T> {
    const response = await globalThis.fetch(
      new Request(`${origin}${path}`, {
        method,
        body,
        signal,
        credentials: 'same-origin',
        headers: { ...headers, ...(type ? { 'Content-Type': type } : {}) },
      }),
    );
    const text = response.status === 204 ? '' : await response.text();
    let parsed: unknown;
    try {
      parsed = text ? JSON.parse(text) : undefined;
    } catch {
      // Not JSON (a proxy's error page): toApiError still gives a readable message.
    }
    if (!response.ok) throw toApiError(response, parsed);
    return parsed as T;
  }

  const one = (id: string) => `${options.base}/${encodeURIComponent(id)}`;
  const json = 'application/json';

  return {
    start: (file, signal) =>
      send(
        'POST',
        options.base,
        JSON.stringify(options.startBody?.(file) ?? { file_name: file.name, size: file.size }),
        signal,
        json,
      ),
    status: (id, signal) => send('GET', one(id), undefined, signal),
    put: (id, offset, part, signal) =>
      send('PUT', `${one(id)}?offset=${offset}`, part, signal, 'application/octet-stream'),
    complete: (id, signal) =>
      send(
        'POST',
        `${one(id)}/complete`,
        options.completeBody ? JSON.stringify(options.completeBody) : undefined,
        signal,
        options.completeBody ? json : undefined,
      ),
    cancel: async (id) => {
      await send('DELETE', one(id));
    },
  };
}
