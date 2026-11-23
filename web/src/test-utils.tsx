// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { QueryClientProvider, type QueryClient } from '@tanstack/react-query';
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router';
import { App } from './App';
import type { Me } from './api/client';
import { createQueryClient } from './api/queryClient';

export interface RecordedCall {
  method: string;
  /** Path and query string, without the origin. */
  path: string;
  body: unknown;
}

export type Handler = (call: RecordedCall) => Response | undefined | Promise<Response | undefined>;

export function json(status: number, body: unknown, headers: Record<string, string> = {}) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  });
}

export const noContent = () => new Response(null, { status: 204 });

export function apiError(status: number, code: string, message: string, headers = {}) {
  return json(status, { error: { code, message } }, headers);
}

/** Replaces fetch with a mock that records each request and answers via handler. */
export function mockFetch(handler: Handler) {
  const calls: RecordedCall[] = [];
  const fn = vi.fn(async (input: Request | string | URL, init?: RequestInit) => {
    const request = input instanceof Request ? input : new Request(input, init);
    const url = new URL(request.url);
    const text = await request.clone().text();
    const call: RecordedCall = {
      method: request.method,
      path: url.pathname + url.search,
      body: text ? JSON.parse(text) : undefined,
    };
    calls.push(call);
    const response = await handler(call);
    return response ?? apiError(404, 'not_found', `No mock for ${call.method} ${call.path}.`);
  });
  vi.stubGlobal('fetch', fn);
  return { calls, fn };
}

function LocationProbe() {
  const location = useLocation();
  return <output data-testid="location">{location.pathname + location.search}</output>;
}

export function renderApp(path: string, client: QueryClient = createQueryClient()) {
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        <App />
        <LocationProbe />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

/**
 * Chooses an option of a Select (a combobox named by its label): opens the
 * list from the keyboard and clicks the option, as a person would.
 */
export async function chooseOption(label: string | RegExp, option: string | RegExp) {
  const trigger = screen.getByRole('combobox', { name: label });
  fireEvent.keyDown(trigger, { key: 'Enter' });
  fireEvent.click(await screen.findByRole('option', { name: option }));
}

/** Opens a menu (dropdown) from its trigger button. */
export async function openMenu(name: string | RegExp) {
  const [trigger] = await screen.findAllByRole('button', { name });
  fireEvent.keyDown(trigger, { key: 'Enter' });
  return screen.findByRole('menu');
}

export function makeUser(overrides: Partial<Me> = {}): Me {
  return {
    id: 1,
    source: 'gitlab',
    username: 'ada',
    name: 'Ada Lovelace',
    email: 'ada@example.com',
    role: 'admin',
    created_at: '2026-01-01T00:00:00Z',
    last_login_at: '2026-11-18T10:00:00Z',
    disabled_at: null,
    mfa_enrolled: true,
    mfa_verified: true,
    must_change_password: false,
    ...overrides,
  };
}
