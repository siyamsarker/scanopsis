// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { ApiError } from './client';
import {
  cancelUpload,
  restUploadEndpoints,
  uploadInChunks,
  UploadInterruptedError,
  type UploadEndpoints,
  type UploadStatus,
} from './upload';

const fast = { retryDelaysMs: [0, 0, 0], busyDelayMs: 0 };

/** A server that stores parts in memory and can be told to fail. */
function fakeServer(size: number, chunk = 4) {
  const stored: { offset: number; text: string }[] = [];
  const state: UploadStatus = {
    id: 'u1',
    file_name: 'backup.tar.gz',
    size,
    chunk_size: chunk,
    received: 0,
  };
  const failures: (() => never | void)[] = [];
  const endpoints: UploadEndpoints<{ name: string }> = {
    start: vi.fn(async () => ({ ...state })),
    status: vi.fn(async () => ({ ...state })),
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
    complete: vi.fn(async () => ({ name: 'scanopsis-backup.tar.gz' })),
    cancel: vi.fn(async () => {}),
  };
  return { state, stored, endpoints, failures };
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
      .mockResolvedValueOnce({ name: 'done' });
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
        const status = { id: 'abc', file_name: 'b.tar.gz', size: 3, chunk_size: 8, received: 3 };
        if (request.method === 'DELETE') return new Response(null, { status: 204 });
        if (request.url.endsWith('/complete')) {
          return new Response(JSON.stringify({ name: 'n' }), { status: 201 });
        }
        return new Response(JSON.stringify(status), { status: 200 });
      }),
    );
    const endpoints = restUploadEndpoints<{ name: string }>({
      base: '/api/v1/setup/restore/uploads',
      headers: { 'X-Setup-Token': 'tok' },
      startBody: (f) => ({ token: 'tok', file_name: f.name, size: f.size }),
      completeBody: { token: 'tok' },
    });
    const signal = new AbortController().signal;
    await endpoints.start(file('abc'), signal);
    await endpoints.put('abc', 0, new Blob(['abc']), signal);
    await endpoints.status('abc', signal);
    expect(await endpoints.complete('abc', signal)).toEqual({ name: 'n' });
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
    const endpoints = restUploadEndpoints({ base: '/api/v1/admin/backup-uploads' });
    const error = await endpoints
      .put('abc', 8, new Blob(['x']), new AbortController().signal)
      .catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ApiError);
    expect(error).toMatchObject({ status: 409, code: 'offset_mismatch', received: 16 });
  });
});
