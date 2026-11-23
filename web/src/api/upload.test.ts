// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import type { BackupInfo } from './backups';
import { ApiError } from './client';
import {
  cancelUpload,
  followUpload,
  PAGE_LEFT,
  readyPart,
  restUploadEndpoints,
  uploadInChunks,
  UploadInterruptedError,
  type UploadEndpoints,
  type UploadStatus,
} from './upload';

const fast = { retryDelaysMs: [0, 0, 0], busyDelayMs: 0, pollMs: 0 };

const backupInfo = (name: string): BackupInfo => ({
  name,
  kind: 'uploaded',
  size: 10,
  created_at: '2026-11-01T02:00:00Z',
  created_by: 'scheduler',
  app_version: '0.4.0',
  schema: 12,
  key_matches: true,
  readable: true,
  newer_version: false,
  uploaded_at: '2026-11-23T12:00:00Z',
  original_name: 'backup.tar.gz',
});

/**
 * A server that stores parts in memory and can be told to fail. Once
 * completed, it answers `checks` questions with the state checking, then
 * with `outcome` (a ready upload by default).
 */
function fakeServer(size: number, chunk = 4) {
  const stored: { offset: number; text: string }[] = [];
  const state: UploadStatus = {
    id: 'u1',
    file_name: 'backup.tar.gz',
    size,
    chunk_size: chunk,
    received: 0,
    state: 'uploading',
  };
  const check = {
    left: 1,
    outcome: {
      state: 'ready',
      backup: backupInfo('scanopsis-backup.tar.gz'),
    } as Partial<UploadStatus>,
  };
  const failures: (() => never | void)[] = [];
  const endpoints: UploadEndpoints<{ name: string }> = {
    start: vi.fn(async () => ({ ...state })),
    status: vi.fn(async () => {
      if (state.state === 'checking') {
        if (check.left > 0) check.left -= 1;
        else Object.assign(state, check.outcome);
      }
      return { ...state };
    }),
    put: vi.fn(async (_id: string, offset: number, part: Blob) => {
      failures.shift()?.();
      if (offset !== state.received) {
        const err = new ApiError(409, 'offset_mismatch', 'Wrong offset.');
        err.received = state.received;
        throw err;
      }
      stored.push({ offset, text: await part.text() });
      state.received += part.size;
      return { ...state };
    }),
    complete: vi.fn(async () => {
      if (state.state === 'uploading') state.state = 'checking';
      return { ...state };
    }),
    cancel: vi.fn(async () => {}),
    result: (status) => ({ name: status.backup!.name }),
  };
  return { state, stored, endpoints, failures, check };
}

const file = (text: string) => new File([text], 'backup.tar.gz');

describe('uploadInChunks', () => {
  it('sends the file in parts of the server chunk size and completes it', async () => {
    const server = fakeServer(10);
    const progress: number[] = [];
    const result = await uploadInChunks(
      file('0123456789'),
      server.endpoints,
      (sent) => progress.push(sent),
      new AbortController().signal,
      fast,
    );
    expect(result).toEqual({ id: 'u1', result: { name: 'scanopsis-backup.tar.gz' } });
    expect(server.stored).toEqual([
      { offset: 0, text: '0123' },
      { offset: 4, text: '4567' },
      { offset: 8, text: '89' },
    ]);
    expect(progress).toEqual([0, 4, 8, 10]);
  });

  it('resumes from what the server received after a network error', async () => {
    const server = fakeServer(10);
    // The second part reaches the server, but the answer is lost.
    server.failures.push(
      () => {},
      () => {
        server.state.received = 8;
        server.stored.push({ offset: 4, text: '4567' });
        throw new TypeError('Failed to fetch');
      },
    );
    const result = await uploadInChunks(
      file('0123456789'),
      server.endpoints,
      () => {},
      new AbortController().signal,
      fast,
    );
    expect(result.id).toBe('u1');
    expect(server.endpoints.status).toHaveBeenCalledWith('u1', expect.anything());
    const offsets = vi.mocked(server.endpoints.put).mock.calls.map((call) => call[1]);
    expect(offsets).toEqual([0, 4, 8]);
    expect(server.stored.map((s) => s.text).join('')).toBe('0123456789');
  });

  it('continues from the received count of an offset mismatch', async () => {
    const server = fakeServer(8);
    server.state.received = 4;
    vi.mocked(server.endpoints.start).mockResolvedValueOnce({ ...server.state, received: 0 });
    await uploadInChunks(
      file('abcdefgh'),
      server.endpoints,
      () => {},
      new AbortController().signal,
      fast,
    );
    expect(vi.mocked(server.endpoints.put).mock.calls.map((call) => call[1])).toEqual([0, 4]);
  });

  it('stops after three failed retries with an error that can resume the upload', async () => {
    const server = fakeServer(8);
    const offline = () => {
      throw new TypeError('Failed to fetch');
    };
    server.failures.push(() => {}, offline, offline, offline, offline);
    const error = await uploadInChunks(
      file('abcdefgh'),
      server.endpoints,
      () => {},
      new AbortController().signal,
      fast,
    ).catch((e: unknown) => e);
    expect(error).toBeInstanceOf(UploadInterruptedError);
    expect(error).toMatchObject({ id: 'u1', received: 4, size: 8 });
    expect(server.endpoints.put).toHaveBeenCalledTimes(5);

    // Resuming asks the server where to continue and does not start again.
    const result = await uploadInChunks(
      file('abcdefgh'),
      server.endpoints,
      () => {},
      new AbortController().signal,
      { ...fast, resumeId: 'u1' },
    );
    expect(result.id).toBe('u1');
    expect(server.endpoints.start).toHaveBeenCalledTimes(1);
    expect(server.stored.map((s) => s.text).join('')).toBe('abcdefgh');
  });

  it('stays resumable when a resume finds the server still unreachable', async () => {
    const server = fakeServer(8);
    for (const offline of [
      new TypeError('Failed to fetch'),
      new ApiError(502, 'unknown', 'Bad gateway.'),
      new ApiError(503, 'unavailable', 'Down.'),
    ]) {
      vi.mocked(server.endpoints.status).mockRejectedValueOnce(offline);
      const error = await uploadInChunks(
        file('abcdefgh'),
        server.endpoints,
        () => {},
        new AbortController().signal,
        { ...fast, resumeId: 'u1', resumeReceived: 4 },
      ).catch((e: unknown) => e);
      expect(error).toBeInstanceOf(UploadInterruptedError);
      expect(error).toMatchObject({ id: 'u1', received: 4, size: 8 });
    }
    expect(server.endpoints.start).not.toHaveBeenCalled();
    expect(server.endpoints.cancel).not.toHaveBeenCalled();
  });

  it('does not treat the restore gate as a passing outage', async () => {
    const server = fakeServer(8);
    vi.mocked(server.endpoints.status).mockRejectedValueOnce(
      new ApiError(503, 'restoring', 'A backup is being restored.'),
    );
    await expect(
      uploadInChunks(file('abcdefgh'), server.endpoints, () => {}, new AbortController().signal, {
        ...fast,
        resumeId: 'u1',
      }),
    ).rejects.toMatchObject({ code: 'restoring' });
  });

  it('asks a busy server again to start, and reports the new id', async () => {
    const server = fakeServer(4);
    vi.mocked(server.endpoints.start).mockRejectedValueOnce(new ApiError(503, 'busy', 'Busy.'));
    const started = vi.fn();
    await uploadInChunks(file('abcd'), server.endpoints, () => {}, new AbortController().signal, {
      ...fast,
      onStarted: started,
    });
    expect(server.endpoints.start).toHaveBeenCalledTimes(2);
    expect(started).toHaveBeenCalledWith('u1');
  });

  it('cancels an upload, asking a busy server again', async () => {
    const server = fakeServer(4);
    vi.mocked(server.endpoints.cancel)
      .mockRejectedValueOnce(new ApiError(503, 'busy', 'Busy.'))
      .mockRejectedValueOnce(new ApiError(409, 'upload_busy', 'Still writing.'));
    await cancelUpload(server.endpoints, 'u1', 0);
    expect(server.endpoints.cancel).toHaveBeenCalledTimes(3);
    vi.mocked(server.endpoints.cancel).mockRejectedValueOnce(new TypeError('Failed to fetch'));
    await expect(cancelUpload(server.endpoints, 'u1', 0)).resolves.toBeUndefined();
  });

  it('retries a busy server, separately from network errors', async () => {
    const server = fakeServer(4);
    const busy = new ApiError(503, 'busy', 'Busy.', 2);
    vi.mocked(server.endpoints.complete)
      .mockRejectedValueOnce(busy)
      .mockRejectedValueOnce(new ApiError(409, 'upload_busy', 'Still writing.'))
      .mockResolvedValueOnce({
        ...server.state,
        received: 4,
        state: 'ready',
        backup: backupInfo('done'),
      });
    const result = await uploadInChunks(
      file('abcd'),
      server.endpoints,
      () => {},
      new AbortController().signal,
      fast,
    );
    expect(result.result).toEqual({ name: 'done' });
    expect(server.endpoints.complete).toHaveBeenCalledTimes(3);
  });

  it('sends again what the server lost before it completes', async () => {
    const server = fakeServer(8);
    const incomplete = new ApiError(409, 'upload_incomplete', 'Not all bytes arrived.');
    incomplete.received = 4;
    vi.mocked(server.endpoints.complete).mockImplementationOnce(async () => {
      server.state.received = 4;
      throw incomplete;
    });
    await uploadInChunks(
      file('abcdefgh'),
      server.endpoints,
      () => {},
      new AbortController().signal,
      fast,
    );
    expect(vi.mocked(server.endpoints.put).mock.calls.map((call) => call[1])).toEqual([0, 4, 4]);
    expect(server.endpoints.complete).toHaveBeenCalledTimes(2);
  });

  it('gives up at once on an answer that retrying cannot fix', async () => {
    const server = fakeServer(4);
    vi.mocked(server.endpoints.complete).mockRejectedValueOnce(
      new ApiError(422, 'invalid_backup', 'This file is not a Scanopsis backup.'),
    );
    await expect(
      uploadInChunks(file('abcd'), server.endpoints, () => {}, new AbortController().signal, fast),
    ).rejects.toMatchObject({ status: 422, code: 'invalid_backup' });
  });

  it('asks for the outcome while the server checks the file', async () => {
    const server = fakeServer(4);
    server.check.left = 3;
    const result = await uploadInChunks(
      file('abcd'),
      server.endpoints,
      () => {},
      new AbortController().signal,
      fast,
    );
    expect(result).toEqual({ id: 'u1', result: { name: 'scanopsis-backup.tar.gz' } });
    expect(server.endpoints.complete).toHaveBeenCalledTimes(1);
    expect(server.endpoints.status).toHaveBeenCalledTimes(4);
  });

  it('turns a refused file into an ApiError with the reason', async () => {
    const server = fakeServer(4);
    server.check.outcome = {
      state: 'refused',
      reason: { code: 'key_mismatch', message: 'Made with another secret key.' },
    };
    await expect(
      uploadInChunks(file('abcd'), server.endpoints, () => {}, new AbortController().signal, fast),
    ).rejects.toMatchObject({
      status: 422,
      code: 'key_mismatch',
      message: 'Made with another secret key.',
    });
  });

  it('stays resumable when the connection drops during the check', async () => {
    const server = fakeServer(4);
    const offline = new TypeError('Failed to fetch');
    vi.mocked(server.endpoints.status)
      .mockRejectedValueOnce(offline)
      .mockRejectedValueOnce(offline)
      .mockRejectedValueOnce(offline)
      .mockRejectedValueOnce(offline);
    const error = await uploadInChunks(
      file('abcd'),
      server.endpoints,
      () => {},
      new AbortController().signal,
      fast,
    ).catch((e: unknown) => e);
    expect(error).toBeInstanceOf(UploadInterruptedError);
    expect(error).toMatchObject({ id: 'u1', received: 4, size: 4 });

    // Resuming asks for the outcome; nothing is sent or completed again.
    const result = await uploadInChunks(
      file('abcd'),
      server.endpoints,
      () => {},
      new AbortController().signal,
      { ...fast, resumeId: 'u1' },
    );
    expect(result.result).toEqual({ name: 'scanopsis-backup.tar.gz' });
    expect(server.endpoints.put).toHaveBeenCalledTimes(1);
    expect(server.endpoints.complete).toHaveBeenCalledTimes(1);
    expect(server.endpoints.start).toHaveBeenCalledTimes(1);
  });

  it('stops the check on the server when it is aborted while checking', async () => {
    const server = fakeServer(4);
    server.check.left = 1000;
    const controller = new AbortController();
    vi.mocked(server.endpoints.status).mockImplementation(async () => {
      controller.abort();
      return { ...server.state };
    });
    const error = await uploadInChunks(
      file('abcd'),
      server.endpoints,
      () => {},
      controller.signal,
      fast,
    ).catch((e: unknown) => e);
    expect((error as DOMException).name).toBe('AbortError');
    expect(server.endpoints.cancel).toHaveBeenCalledWith('u1');
  });

  it('cancels the upload on the server when it is aborted', async () => {
    const server = fakeServer(8);
    const controller = new AbortController();
    vi.mocked(server.endpoints.put).mockImplementationOnce(async () => {
      controller.abort();
      throw new DOMException('Aborted', 'AbortError');
    });
    const error = await uploadInChunks(
      file('abcdefgh'),
      server.endpoints,
      () => {},
      controller.signal,
      fast,
    ).catch((e: unknown) => e);
    expect(error).toBeInstanceOf(DOMException);
    expect((error as DOMException).name).toBe('AbortError');
    expect(server.endpoints.cancel).toHaveBeenCalledWith('u1');
  });

  it('leaves a fully received upload to its check when the page is left', async () => {
    const server = fakeServer(4);
    server.check.left = 1000;
    const controller = new AbortController();
    vi.mocked(server.endpoints.status).mockImplementation(async () => {
      controller.abort(PAGE_LEFT);
      return { ...server.state };
    });
    const error = await uploadInChunks(
      file('abcd'),
      server.endpoints,
      () => {},
      controller.signal,
      fast,
    ).catch((e: unknown) => e);
    expect((error as DOMException).name).toBe('AbortError');
    expect(server.endpoints.cancel).not.toHaveBeenCalled();
  });

  it('cancels a partly sent upload when the page is left', async () => {
    const server = fakeServer(8);
    const controller = new AbortController();
    vi.mocked(server.endpoints.put).mockImplementationOnce(async () => {
      controller.abort(PAGE_LEFT);
      throw new DOMException('Aborted', 'AbortError');
    });
    const error = await uploadInChunks(
      file('abcdefgh'),
      server.endpoints,
      () => {},
      controller.signal,
      fast,
    ).catch((e: unknown) => e);
    expect((error as DOMException).name).toBe('AbortError');
    expect(server.endpoints.cancel).toHaveBeenCalledWith('u1');
  });

  it('stays resumable when the check stopped, as after a restart of the server', async () => {
    const server = fakeServer(4);
    vi.mocked(server.endpoints.status).mockImplementationOnce(async () => {
      // The server stopped the check and holds the file as received.
      server.state.state = 'uploading';
      return { ...server.state };
    });
    const error = await uploadInChunks(
      file('abcd'),
      server.endpoints,
      () => {},
      new AbortController().signal,
      fast,
    ).catch((e: unknown) => e);
    expect(error).toBeInstanceOf(UploadInterruptedError);
    expect(error).toMatchObject({ id: 'u1', received: 4, size: 4, checkStopped: true });
    expect((error as Error).message).toBe(
      'The check of the file stopped before it ended, for example because Scanopsis restarted.',
    );

    // Resuming completes the upload again, which starts a new check.
    const result = await uploadInChunks(
      file('abcd'),
      server.endpoints,
      () => {},
      new AbortController().signal,
      { ...fast, resumeId: 'u1' },
    );
    expect(result.result).toEqual({ name: 'scanopsis-backup.tar.gz' });
    expect(server.endpoints.complete).toHaveBeenCalledTimes(2);
    expect(server.endpoints.put).toHaveBeenCalledTimes(1);
  });
});

describe('followUpload', () => {
  it('asks for the outcome of an upload the server checks', async () => {
    const server = fakeServer(4);
    server.state.received = 4;
    server.state.state = 'checking';
    server.check.left = 2;
    const result = await followUpload(
      'u1',
      4,
      server.endpoints,
      new AbortController().signal,
      fast,
    );
    expect(result).toEqual({ id: 'u1', result: { name: 'scanopsis-backup.tar.gz' } });
    expect(server.endpoints.status).toHaveBeenCalledTimes(3);
    expect(server.endpoints.put).not.toHaveBeenCalled();
    expect(server.endpoints.complete).not.toHaveBeenCalled();
  });

  it('gives the outcome of an upload that was checked meanwhile', async () => {
    const server = fakeServer(4);
    Object.assign(server.state, {
      received: 4,
      state: 'refused',
      reason: { code: 'invalid_backup', message: 'Damaged.' },
    });
    await expect(
      followUpload('u1', 4, server.endpoints, new AbortController().signal, fast),
    ).rejects.toMatchObject({ status: 422, code: 'invalid_backup' });
  });

  it('cannot continue an upload the server holds only in part', async () => {
    const server = fakeServer(8);
    server.state.received = 4;
    const error = await followUpload(
      'u1',
      8,
      server.endpoints,
      new AbortController().signal,
      fast,
    ).catch((e: unknown) => e);
    expect(error).toBeInstanceOf(UploadInterruptedError);
    expect(error).toMatchObject({ id: 'u1', received: 4, size: 8 });
  });
});

describe('restUploadEndpoints', () => {
  it('talks to the upload routes with the given headers and bodies', async () => {
    const seen: { method: string; url: string; body: string; token: string | null }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (request: Request) => {
        seen.push({
          method: request.method,
          url: new URL(request.url).pathname + new URL(request.url).search,
          body: await request.text(),
          token: request.headers.get('X-Setup-Token'),
        });
        const status = {
          id: 'abc',
          file_name: 'b.tar.gz',
          size: 3,
          chunk_size: 8,
          received: 3,
          state: 'uploading',
        };
        if (request.method === 'DELETE') return new Response(null, { status: 204 });
        if (request.url.endsWith('/complete')) {
          return new Response(JSON.stringify({ ...status, state: 'checking' }), { status: 202 });
        }
        return new Response(JSON.stringify(status), { status: 200 });
      }),
    );
    const endpoints = restUploadEndpoints({
      base: '/api/v1/setup/restore/uploads',
      headers: { 'X-Setup-Token': 'tok' },
      startBody: (f) => ({ token: 'tok', file_name: f.name, size: f.size }),
      completeBody: { token: 'tok' },
      result: readyPart('preview'),
    });
    const signal = new AbortController().signal;
    await endpoints.start(file('abc'), signal);
    await endpoints.put('abc', 0, new Blob(['abc']), signal);
    await endpoints.status('abc', signal);
    expect(await endpoints.complete('abc', signal)).toMatchObject({ id: 'abc', state: 'checking' });
    await endpoints.cancel('abc');
    expect(seen).toEqual([
      {
        method: 'POST',
        url: '/api/v1/setup/restore/uploads',
        body: JSON.stringify({ token: 'tok', file_name: 'backup.tar.gz', size: 3 }),
        token: 'tok',
      },
      {
        method: 'PUT',
        url: '/api/v1/setup/restore/uploads/abc?offset=0',
        body: 'abc',
        token: 'tok',
      },
      { method: 'GET', url: '/api/v1/setup/restore/uploads/abc', body: '', token: 'tok' },
      {
        method: 'POST',
        url: '/api/v1/setup/restore/uploads/abc/complete',
        body: JSON.stringify({ token: 'tok' }),
        token: 'tok',
      },
      { method: 'DELETE', url: '/api/v1/setup/restore/uploads/abc', body: '', token: 'tok' },
    ]);
  });

  it('turns an error answer into an ApiError with the received count', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () =>
          new Response(
            JSON.stringify({
              error: { code: 'offset_mismatch', message: 'Wrong offset.', received: 16 },
            }),
            { status: 409 },
          ),
      ),
    );
    const endpoints = restUploadEndpoints({
      base: '/api/v1/admin/backup-uploads',
      result: readyPart('backup'),
    });
    const error = await endpoints
      .put('abc', 8, new Blob(['x']), new AbortController().signal)
      .catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ApiError);
    expect(error).toMatchObject({ status: 409, code: 'offset_mismatch', received: 16 });
  });
});

describe('readyPart', () => {
  it('reads the backup or the preview of a ready upload', () => {
    const status: UploadStatus = {
      id: 'u1',
      file_name: 'b.tar.gz',
      size: 3,
      chunk_size: 8,
      received: 3,
      state: 'ready',
      backup: backupInfo('n'),
    };
    expect(readyPart('backup')(status).name).toBe('n');
    expect(() => readyPart('preview')(status)).toThrow(/no preview/);
  });
});
