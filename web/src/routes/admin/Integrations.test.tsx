// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { QueryClient } from '@tanstack/react-query';
import { fireEvent, screen, within } from '@testing-library/react';
import { INTEGRATION_REFRESH_MS } from '../../api/admin';
import { createQueryClient } from '../../api/queryClient';
import { apiError, json, makeUser, mockFetch, renderApp, type Handler } from '../../test-utils';
import { rejectionReasons } from './labels';

const trivy = {
  configured: true,
  last_upload_at: '2026-11-19T10:00:00Z',
  uploads_24h: 1234,
  rejections_24h: 7,
  recent_rejections: [
    { at: '2026-11-19T09:00:00Z', reason: 'invalid_token', project_path: 'platform/payments' },
    { at: '2026-11-19T08:00:00Z', reason: 'ref_not_protected', project_path: null },
    { at: '2026-11-19T07:00:00Z', reason: 'brand_new_code', project_path: 'x/y' },
  ],
};
const sonar = {
  configured: true,
  server_version: '10.7.0',
  last_webhook_at: '2026-11-19T10:00:00Z',
  last_sync_at: null,
  last_sync_error: 'connection refused',
  recent_deliveries: [
    {
      task_id: 'AZ1',
      project_key: 'platform_payments',
      received_at: '2026-11-19T09:00:00Z',
      outcome: 'linked',
      service_id: 5,
      detail: 'matched by key',
    },
    {
      task_id: 'AZ2',
      project_key: 'orphan',
      received_at: '2026-11-19T08:00:00Z',
      outcome: 'failed',
      service_id: null,
      detail: 'no service uses this key',
    },
  ],
};

function api(extra?: Handler): Handler {
  return async (call) => {
    const custom = await extra?.(call);
    if (custom) return custom;
    if (call.path === '/api/v1/me') return json(200, makeUser());
    if (call.path === '/api/v1/admin/integrations/trivy') return json(200, trivy);
    if (call.path === '/api/v1/admin/integrations/sonarqube') return json(200, sonar);
    return undefined;
  };
}

describe('integrations page', () => {
  it('tells viewers they need the admin role and requests nothing admin', async () => {
    const { calls } = mockFetch((call) =>
      call.path === '/api/v1/me' ? json(200, makeUser({ role: 'viewer' })) : undefined,
    );
    renderApp('/admin/integrations');
    expect(await screen.findByText('You need the admin role to see this page.')).toBeVisible();
    expect(calls.some((c) => c.path.includes('/admin/'))).toBe(false);
  });

  it('shows the scan upload numbers, state, links and rejections', async () => {
    mockFetch(api());
    renderApp('/admin/integrations');
    const card = await screen.findByRole('region', { name: 'Scan uploads' });
    expect(within(card).getByText('Ready')).toBeInTheDocument();
    expect(within(card).getByText('1,234')).toBeInTheDocument();
    expect(within(card).getByText('7')).toBeInTheDocument();
    expect(within(card).getByRole('link', { name: 'GitLab CI guide' })).toHaveAttribute(
      'href',
      'https://github.com/siyamsarker/scanopsis/blob/main/docs/integrations/gitlab-ci.md',
    );
    const table = within(card).getByRole('table');
    const row = within(table).getByRole('row', { name: /invalid_token/ });
    expect(within(row).getByText(rejectionReasons.invalid_token)).toBeInTheDocument();
    expect(within(row).getByText('platform/payments')).toBeInTheDocument();
    const unknownPath = within(table).getByRole('row', { name: /ref_not_protected/ });
    expect(within(unknownPath).getByText('Unknown')).toBeInTheDocument();
    expect(within(table).getByText('This reason code is not recognised.')).toBeInTheDocument();
  });

  it('explains every known rejection code in plain words', () => {
    for (const code of [
      'invalid_token',
      'namespace_not_allowed',
      'ref_not_protected',
      'report_too_large',
      'not_a_trivy_report',
      'invalid_report',
      'unsupported_media_type',
      'too_many_requests',
      'service_archived',
      'service_conflict',
      'gitlab_unavailable',
    ]) {
      expect(rejectionReasons[code]).toMatch(/^[A-Z].+\.$/);
    }
  });

  it('points to Settings when scan uploads are not configured', async () => {
    mockFetch(
      api((call) =>
        call.path.endsWith('/trivy')
          ? json(200, { ...trivy, configured: false, last_upload_at: null, recent_rejections: [] })
          : undefined,
      ),
    );
    renderApp('/admin/integrations');
    const card = await screen.findByRole('region', { name: 'Scan uploads' });
    expect(within(card).getByText(/Not configured — set the GitLab URL in/)).toBeInTheDocument();
    expect(within(card).getByRole('link', { name: 'Settings' })).toHaveAttribute(
      'href',
      '/settings',
    );
    expect(within(card).getByText('Last upload never')).toBeInTheDocument();
    expect(within(card).getByText('No rejections')).toBeInTheDocument();
  });

  it('shows SonarQube status, the sync error and deliveries', async () => {
    mockFetch(api());
    renderApp('/admin/integrations');
    const card = await screen.findByRole('region', { name: 'SonarQube' });
    expect(within(card).getByText('10.7.0')).toBeInTheDocument();
    expect(within(card).getByText('Last sync never')).toBeInTheDocument();
    expect(within(card).getByRole('alert')).toHaveTextContent('connection refused');
    expect(within(card).getByRole('link', { name: 'SonarQube guide' })).toHaveAttribute(
      'href',
      'https://github.com/siyamsarker/scanopsis/blob/main/docs/integrations/sonarqube.md',
    );
    const linked = within(card).getByRole('row', { name: /AZ1/ });
    expect(within(linked).getByText('Linked')).toBeInTheDocument();
    expect(within(linked).getByText('platform_payments')).toBeInTheDocument();
    expect(within(linked).getByRole('link', { name: 'Service 5' })).toHaveAttribute(
      'href',
      '/services/5',
    );
    expect(within(linked).getByText('matched by key')).toBeInTheDocument();
    const failed = within(card).getByRole('row', { name: /AZ2/ });
    expect(within(failed).getByText('Failed')).toBeInTheDocument();
    expect(within(failed).getByText('None')).toBeInTheDocument();
  });

  it('refreshes every 30 seconds while visible and on demand', async () => {
    const client: QueryClient = createQueryClient();
    const { calls } = mockFetch(api());
    renderApp('/admin/integrations', client);
    await screen.findByRole('region', { name: 'SonarQube' });
    for (const key of ['trivy', 'sonarqube']) {
      const query = client.getQueryCache().find({ queryKey: ['admin', 'integrations', key] });
      const options = query?.observers[0].options;
      expect(options?.refetchInterval).toBe(INTEGRATION_REFRESH_MS);
      expect(INTEGRATION_REFRESH_MS).toBe(30_000);
      expect(options?.refetchIntervalInBackground).toBe(false);
    }
    const before = calls.filter((c) => c.path.includes('/integrations/')).length;
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await screen.findByRole('button', { name: 'Refresh' });
    await vi.waitFor(() =>
      expect(calls.filter((c) => c.path.includes('/integrations/')).length).toBe(before + 2),
    );
  });

  it('shows an error state with retry when a card cannot load', async () => {
    mockFetch(
      api((call) =>
        call.path.endsWith('/sonarqube')
          ? apiError(500, 'internal', 'Something went wrong.')
          : undefined,
      ),
    );
    renderApp('/admin/integrations');
    expect(await screen.findByText('SonarQube could not be loaded')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Try again' })).toBeInTheDocument();
    expect(await screen.findByRole('region', { name: 'Scan uploads' })).toBeInTheDocument();
  });
});
