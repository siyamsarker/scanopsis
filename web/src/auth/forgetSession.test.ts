// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { QueryClient } from '@tanstack/react-query';
import { forgetSession, scheduleDraftKey } from './forgetSession';

afterEach(() => sessionStorage.clear());

describe('forgetSession', () => {
  it('empties the query cache and every Scanopsis entry of the tab, and nothing else', () => {
    const client = new QueryClient();
    client.setQueryData(['me'], { id: 1 });
    sessionStorage.setItem(scheduleDraftKey(1), '{}');
    sessionStorage.setItem('scanopsis.setup-restore-upload', '{}');
    sessionStorage.setItem('other-app', 'kept');
    forgetSession(client);
    expect(client.getQueryData(['me'])).toBeUndefined();
    expect(sessionStorage.getItem(scheduleDraftKey(1))).toBeNull();
    expect(sessionStorage.getItem('scanopsis.setup-restore-upload')).toBeNull();
    expect(sessionStorage.getItem('other-app')).toBe('kept');
  });

  it('names the schedule draft after the user, so one admin never sees another one', () => {
    expect(scheduleDraftKey(1)).not.toBe(scheduleDraftKey(2));
    expect(scheduleDraftKey(7).startsWith('scanopsis.')).toBe(true);
  });
});
