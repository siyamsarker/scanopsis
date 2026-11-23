// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { ApiError, toApiError } from './client';
import type { components } from './schema.gen';

/*
 * Uploads a large file (a backup) in parts the server stores one after the
 * other. Only one part is read from the file at a time, so a file of many
 * gigabytes never sits in memory. A dropped connection does not start over:
 * the server says how much it holds, and the upload continues from there.
 * Once every part has arrived, the server checks the whole file in the
 * background, which can take minutes for a large one; the upload asks for the
 * outcome until the check has ended.
 */

/**
 * What the server knows of an upload: how much it holds (`received`, where
 * the next part starts; `chunk_size` is the largest part it takes) and, once
 * completed, the check of the file (`state`, with `reason`, `backup` or
 * `preview` once it has ended).
 */
export type UploadStatus = components['schemas']['UploadStatus'];

/** The requests of one upload API (the admin one or the setup screen's). */
export interface UploadEndpoints<R> {
  start(file: File, signal: AbortSignal): Promise<UploadStatus>;
  status(id: string, signal: AbortSignal): Promise<UploadStatus>;
  put(id: string, offset: number, part: Blob, signal: AbortSignal): Promise<UploadStatus>;
  /** Starts the check of a fully received upload; answers while it runs. */
  complete(id: string, signal: AbortSignal): Promise<UploadStatus>;
  cancel(id: string): Promise<void>;
  /** What a ready upload gives the caller (the new backup, or a preview). */
  result(status: UploadStatus): R;
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
  /** Wait between two questions about a file being checked. */
  pollMs?: number;
}

/**
 * The connection failed more often than the upload retries, or the server
 * stopped the check of the complete file before it ended (`checkStopped`,
 * for example when it restarted). The server keeps the parts it has (for an
 * hour without a new one), so passing `id` as `resumeId` continues where it
 * stopped, and completes the upload again, which starts a new check.
 */
export class UploadInterruptedError extends Error {
  readonly id: string;
  readonly received: number;
  readonly size: number;
  readonly checkStopped: boolean;

  constructor(id: string, received: number, size: number, checkStopped = false) {
    super(
      checkStopped
        ? 'The check of the file stopped before it ended, for example because Scanopsis restarted.'
        : 'The connection to the server was lost during the upload.',
    );
    this.name = 'UploadInterruptedError';
    this.id = id;
    this.received = received;
    this.size = size;
    this.checkStopped = checkStopped;
  }
}

/**
 * The reason to abort an upload with when its page goes away rather than
 * when someone cancels it: an upload whose every byte was sent is then left
 * to its check on the server, which keeps the outcome. Any other abort
 * cancels the upload on the server.
 */
export const PAGE_LEFT = 'page-left';

/** Why an upload is gone while the server checked it: the server forgot it in a restart. */
export const UPLOAD_LOST = 'Scanopsis restarted while the file was being checked. Upload it again.';

const RETRY_DELAYS_MS = [1000, 3000, 9000];
export const BUSY_DELAY_MS = 2000;
/** How often the server is asked about a file it checks, and the first time. */
export const CHECK_POLL_MS = 2000;
const FIRST_POLL_MS = 500;
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

/** A check that ended: the result of a ready upload, or the refusal as an ApiError. */
function outcome<R>(endpoints: UploadEndpoints<R>, status: UploadStatus): R {
  if (status.state === 'refused') {
    throw new ApiError(
      422,
      status.reason?.code ?? 'invalid_backup',
      status.reason?.message ?? 'The server refused the file.',
    );
  }
  return endpoints.result(status);
}

interface CheckOptions {
  delays: number[];
  busyDelay: number;
  pollMs: number;
}

function checkOptions(options: UploadOptions): CheckOptions {
  return {
    delays: options.retryDelaysMs ?? RETRY_DELAYS_MS,
    busyDelay: options.busyDelayMs ?? BUSY_DELAY_MS,
    pollMs: options.pollMs ?? CHECK_POLL_MS,
  };
}

/**
 * Asks for the outcome of the check of the upload `id` of `size` bytes until
 * it has ended, starting from `status`. A server that holds the file again
 * as received (its check stopped) throws a resumable UploadInterruptedError.
 */
async function awaitCheck<R>(
  endpoints: UploadEndpoints<R>,
  id: string,
  size: number,
  status: UploadStatus,
  signal: AbortSignal,
  { delays, busyDelay, pollMs }: CheckOptions,
): Promise<UploadResult<R>> {
  let failures = 0;
  // A small file is checked at once: the first question comes sooner.
  let next = Math.min(FIRST_POLL_MS, pollMs);
  while (status.state === 'checking') {
    await wait(failures > 0 ? delays[failures - 1] : next, signal);
    next = pollMs;
    try {
      status = await patiently(() => endpoints.status(id, signal), signal, busyDelay);
      failures = 0;
    } catch (err) {
      if (signal.aborted) throw abortError();
      if (err instanceof ApiError && err.status === 404) {
        // The server keeps uploads in memory: a restart forgot this one.
        throw new ApiError(404, err.code, UPLOAD_LOST);
      }
      if (!isTransient(err)) throw err;
      failures += 1;
      if (failures > delays.length) throw new UploadInterruptedError(id, size, size);
    }
  }
  if (status.state === 'uploading') {
    throw new UploadInterruptedError(id, status.received, size, status.received >= size);
  }
  return { id, result: outcome(endpoints, status) };
}

/** Whether an aborted upload is to be cancelled on the server: not when its page went away after every byte was sent. */
function cancelOnAbort(signal: AbortSignal, received: number, size: number) {
  return !(signal.reason === PAGE_LEFT && received >= size);
}

/**
 * Follows an upload of `size` bytes that was sent in full earlier, such as
 * before the page was reloaded: asks for the outcome of its check until it
 * has ended. An upload the server holds only in part, or whose check stopped,
 * throws an UploadInterruptedError; resuming it needs the file. Aborting
 * `signal` stops the check on the server, unless the reason is PAGE_LEFT.
 */
export async function followUpload<R>(
  id: string,
  size: number,
  endpoints: UploadEndpoints<R>,
  signal: AbortSignal,
  options: UploadOptions = {},
): Promise<UploadResult<R>> {
  const check = checkOptions(options);
  try {
    let first: UploadStatus;
    try {
      first = await patiently(() => endpoints.status(id, signal), signal, check.busyDelay);
    } catch (err) {
      if (!signal.aborted && isTransient(err)) throw new UploadInterruptedError(id, size, size);
      throw err;
    }
    if (first.state === 'uploading') {
      throw new UploadInterruptedError(id, first.received, size, first.received >= size);
    }
    return await awaitCheck(endpoints, id, size, first, signal, check);
  } catch (err) {
    if (signal.aborted && cancelOnAbort(signal, size, size)) {
      await cancelUpload(endpoints, id, check.busyDelay);
    }
    if (signal.aborted) throw abortError();
    throw err;
  }
}

/**
 * Uploads `file` in parts of the server's chunk size and completes it. Reports
 * the bytes the server holds after each part. After a network error it asks
 * the server how much arrived and continues from there, up to three times in a
 * row with growing waits; then it throws an UploadInterruptedError that can be
 * resumed. A busy server is asked again without counting as a failure. Once
 * the server checks the file, it asks for the outcome every few seconds, with
 * the same retries; a refused file throws an ApiError with the reason's code.
 * Aborting `signal` cancels the upload on the server (which stops a check)
 * and throws an AbortError; aborting it with the reason PAGE_LEFT once every
 * byte was sent leaves the upload to its check.
 */
export async function uploadInChunks<R>(
  file: File,
  endpoints: UploadEndpoints<R>,
  onProgress: (sent: number, total: number) => void,
  signal: AbortSignal,
  options: UploadOptions = {},
): Promise<UploadResult<R>> {
  const check = checkOptions(options);
  const { delays, busyDelay } = check;
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
    // A resumed upload may have been completed already.
    if (first.state !== 'uploading') {
      return await awaitCheck(endpoints, id, file.size, first, signal, check);
    }

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
      let checking: UploadStatus;
      try {
        checking = await patiently(() => endpoints.complete(id!, signal), signal, busyDelay);
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
      return await awaitCheck(endpoints, id!, file.size, checking, signal, check);
    }
  } catch (err) {
    if (signal.aborted && id && cancelOnAbort(signal, received, file.size)) {
      await cancelUpload(endpoints, id, busyDelay);
    }
    if (signal.aborted) throw abortError();
    throw err;
  }
}

/**
 * Cancels an open upload, or stops the check of a completed one, asking a
 * busy server again. Best effort: an upload nobody completes is dropped after
 * an hour anyway, so a failure is ignored.
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

/** Reads what a ready upload gives the caller; throws when the server sent none. */
export function readyPart<K extends 'backup' | 'preview'>(
  key: K,
): (status: UploadStatus) => NonNullable<UploadStatus[K]> {
  return (status) => {
    const part = status[key];
    if (!part) throw new Error(`The server did not describe the checked file (no ${key}).`);
    return part as NonNullable<UploadStatus[K]>;
  };
}

/** The endpoints of an upload API that follows the backup upload routes. */
export function restUploadEndpoints<R>(
  options: RestUploadOptions & { result: (status: UploadStatus) => R },
): UploadEndpoints<R> {
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
    result: options.result,
  };
}
