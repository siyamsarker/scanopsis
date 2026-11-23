// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { QueryClient } from '@tanstack/react-query';
import { configure, fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import {
  INTEGRATION_REFRESH_MS,
  type SonarIntegration,
  type TrivyIntegration,
} from '../../api/admin';
import { createQueryClient } from '../../api/queryClient';
import { apiError, json, makeUser, mockFetch, renderApp, type Handler } from '../../test-utils';
import { sonarVerdict, uploadsVerdict } from '../IntegrationsPage';
import { rejectionLabels, rejectionReasons } from './labels';

configure({ asyncUtilTimeout: 5000 });

/** Thirty UTC days ending 19 Nov 2026: 0, 1, 2 uploads in turn; one refusal every tenth day. */
const daily = Array.from({ length: 30 }, (_, i) => {
  const day = new Date(Date.UTC(2026, 10, 19 - 29 + i)).toISOString().slice(0, 10);
  return { day, uploads: i % 3, rejections: i % 10 === 0 ? 1 : 0 };
});
const dailyUploads = daily.reduce((n, d) => n + d.uploads, 0);

const trivy: TrivyIntegration = {
  configured: true,
  last_upload_at: '2026-11-19T10:00:00Z',
  uploads_24h: 1234,
  rejections_24h: 7,
  recent_rejections: [
    {
      at: '2026-11-19T09:00:00Z',
      reason: 'invalid_token',
      project_path: 'platform/payments',
      service_id: 5,
      service_name: 'Payments',
    },
    {
      at: '2026-11-19T08:00:00Z',
      reason: 'ref_not_protected',
      project_path: null,
      service_id: null,
      service_name: null,
    },
    {
      at: '2026-11-19T07:00:00Z',
      reason: 'brand_new_code',
      project_path: 'x/y',
      service_id: null,
      service_name: null,
    },
    {
      at: '2026-11-19T06:00:00Z',
      reason: 'invalid_token',
      project_path: 'x/y',
      service_id: null,
      service_name: null,
    },
  ],
  daily,
};
const sonar: SonarIntegration = {
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
      service_name: 'Payments',
      detail: 'matched by key',
    },
    {
      task_id: 'AZ2',
      project_key: 'orphan',
      received_at: '2026-11-19T08:00:00Z',
      outcome: 'failed',
      service_id: null,
      service_name: null,
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

/** Renders a verdict's reason (it may hold a link) as plain text. */
function reasonText(reason: React.ReactNode) {
  const { container, unmount } = render(<MemoryRouter>{reason}</MemoryRouter>);
  const text = container.textContent;
  unmount();
  return text;
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

  it('gives scan uploads a verdict with the reason and key facts', async () => {
    mockFetch(api());
    renderApp('/admin/integrations');
    const band = await screen.findByRole('region', { name: 'Scan uploads from GitLab CI' });
    expect(within(band).getByRole('heading', { name: 'Some uploads were refused' })).toBeVisible();
    // The most frequent reason among the recent refusals is named.
    expect(band).toHaveTextContent(
      '7 uploads refused in the last 24 hours, most often for invalid id token',
    );
    expect(within(band).getByText('1,234')).toBeInTheDocument();
    expect(within(band).getByText('7')).toBeInTheDocument();
    expect(within(band).getByRole('link', { name: /Setup guide/ })).toHaveAttribute(
      'href',
      'https://github.com/siyamsarker/scanopsis/blob/main/docs/integrations/gitlab-ci.md',
    );
  });

  it('draws uploads and refusals per day from the daily series', async () => {
    mockFetch(api());
    renderApp('/admin/integrations');
    const card = await screen.findByRole('region', { name: 'Uploads per day' });
    expect(card).toHaveTextContent(`${dailyUploads} uploads, 3 refused`);
    const uploads = within(card).getByRole('img', { name: /^Accepted uploads per day:/ });
    expect(uploads).toHaveAccessibleName(
      new RegExp(
        `30 values from 21 Oct to 19 Nov, latest 2 uploads, total ${dailyUploads} uploads`,
      ),
    );
    expect(
      within(card).getByRole('img', { name: /^Refused uploads per day:/ }),
    ).toHaveAccessibleName(/total 3 refused/);
    // Each day is in the table behind the chart, by UTC day.
    const table = within(card).getByRole('table', { name: 'Accepted uploads per day' });
    expect(within(table).getAllByRole('row')).toHaveLength(31);
    expect(within(table).getByRole('row', { name: '19 Nov 2' })).toBeInTheDocument();
  });

  it('names the service of each refused upload and explains the reason', async () => {
    mockFetch(api());
    renderApp('/admin/integrations');
    const card = await screen.findByRole('region', { name: 'Refused uploads' });
    const table = await within(card).findByRole('table', { name: 'Recently refused uploads' });
    const named = within(table).getAllByRole('row')[1];
    expect(within(named).getByRole('link', { name: 'Payments' })).toHaveAttribute(
      'href',
      '/services/5',
    );
    expect(within(named).getByText('platform/payments')).toBeInTheDocument();
    expect(within(named).getByText(rejectionLabels.invalid_token)).toBeInTheDocument();
    expect(within(named).getByText(rejectionReasons.invalid_token)).toBeInTheDocument();
    const unknownPath = within(table).getByRole('row', { name: /Branch not protected/ });
    expect(within(unknownPath).getByText('Not known')).toBeInTheDocument();
    const unknownCode = within(table).getByRole('row', { name: /brand_new_code/ });
    expect(within(unknownCode).getByText('x/y')).toBeInTheDocument();
    expect(within(unknownCode).queryByRole('link')).not.toBeInTheDocument();
    expect(within(table).getByText('This reason code is not recognised.')).toBeInTheDocument();
  });

  it('shows the first refusals and the rest on request', async () => {
    const many = Array.from({ length: 9 }, (_, i) => ({
      ...trivy.recent_rejections[3],
      at: `2026-11-19T0${i}:00:00Z`,
    }));
    mockFetch(
      api((call) =>
        call.path.endsWith('/trivy') ? json(200, { ...trivy, recent_rejections: many }) : undefined,
      ),
    );
    renderApp('/admin/integrations');
    const table = await screen.findByRole('table', { name: 'Recently refused uploads' });
    expect(within(table).getAllByRole('row')).toHaveLength(7);
    fireEvent.click(screen.getByRole('button', { name: 'Show all 9' }));
    expect(within(table).getAllByRole('row')).toHaveLength(10);
    expect(screen.queryByRole('button', { name: /Show all/ })).not.toBeInTheDocument();
  });

  it('shows the first deliveries and the rest on request', async () => {
    const many = Array.from({ length: 8 }, (_, i) => ({
      ...sonar.recent_deliveries[0],
      task_id: `task-${i}`,
      received_at: `2026-11-19T0${i}:00:00Z`,
    }));
    mockFetch(
      api((call) =>
        call.path.endsWith('/sonarqube')
          ? json(200, { ...sonar, recent_deliveries: many })
          : undefined,
      ),
    );
    renderApp('/admin/integrations');
    const table = await screen.findByRole('table', { name: 'Recent SonarQube deliveries' });
    expect(within(table).getAllByRole('row')).toHaveLength(7);
    fireEvent.click(screen.getByRole('button', { name: 'Show all 8' }));
    expect(within(table).getAllByRole('row')).toHaveLength(9);
  });

  it('labels and explains every known rejection code in plain words', () => {
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
      'busy',
      'request_timeout',
      'project_rate_limited',
      'job_artifact_limit',
    ]) {
      expect(rejectionReasons[code]).toMatch(/^[A-Z].+\.$/);
      expect(rejectionLabels[code]).toMatch(/^[A-Z][^.]+$/);
    }
  });

  it('points to Settings when scan uploads are not configured', async () => {
    mockFetch(
      api((call) =>
        call.path.endsWith('/trivy')
          ? json(200, {
              ...trivy,
              configured: false,
              last_upload_at: null,
              recent_rejections: [],
              daily: daily.map((d) => ({ ...d, uploads: 0, rejections: 0 })),
            })
          : undefined,
      ),
    );
    renderApp('/admin/integrations');
    const band = await screen.findByRole('region', { name: 'Scan uploads from GitLab CI' });
    expect(
      within(band).getByRole('heading', { name: 'Scan uploads need attention' }),
    ).toBeVisible();
    expect(band).toHaveTextContent(/refused until the GitLab URL is set in Settings/);
    expect(within(band).getByRole('link', { name: 'Settings' })).toHaveAttribute(
      'href',
      '/settings',
    );
    expect(within(band).getByText('Never')).toBeInTheDocument();
    expect(screen.getByText('Nothing refused')).toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'Uploads per day' })).toHaveTextContent(
      '0 uploads, 0 refused',
    );
  });

  it('shows the SonarQube verdict, its facts and deliveries by service name', async () => {
    mockFetch(api());
    renderApp('/admin/integrations');
    const band = await screen.findByRole('region', { name: 'SonarQube' });
    expect(
      within(band).getByRole('heading', { name: 'The last sync with SonarQube failed' }),
    ).toBeVisible();
    expect(band).toHaveTextContent('connection refused');
    expect(within(band).getByText('10.7.0')).toBeInTheDocument();
    expect(within(band).getByRole('link', { name: /Setup guide/ })).toHaveAttribute(
      'href',
      'https://github.com/siyamsarker/scanopsis/blob/main/docs/integrations/sonarqube.md',
    );

    const table = within(screen.getByRole('region', { name: 'SonarQube deliveries' })).getByRole(
      'table',
      { name: 'Recent SonarQube deliveries' },
    );
    const linked = within(table).getByRole('row', { name: /platform_payments/ });
    expect(within(linked).getByText('Linked')).toBeInTheDocument();
    expect(within(linked).getByRole('link', { name: 'Payments' })).toHaveAttribute(
      'href',
      '/services/5',
    );
    expect(within(linked).getByText('matched by key')).toBeInTheDocument();
    const failed = within(table).getByRole('row', { name: /orphan/ });
    expect(within(failed).getByText('Failed')).toBeInTheDocument();
    expect(within(failed).getByText('No service')).toBeInTheDocument();
  });

  it('refreshes every 30 seconds while visible, on demand, and says when it updated', async () => {
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
    expect(await screen.findByText('Updated just now')).toBeInTheDocument();
    expect(screen.getByText(/The page updates every 30 seconds/)).toBeInTheDocument();
    const before = calls.filter((c) => c.path.includes('/integrations/')).length;
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await vi.waitFor(() =>
      expect(calls.filter((c) => c.path.includes('/integrations/')).length).toBe(before + 2),
    );
    await vi.waitFor(() => expect(screen.getByRole('button', { name: 'Refresh' })).toBeEnabled());
  });

  it('shows an error state with retry when one integration cannot load', async () => {
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
    expect(
      await screen.findByRole('region', { name: 'Scan uploads from GitLab CI' }),
    ).toBeInTheDocument();
  });
});

describe('integration verdicts', () => {
  it('reads scan uploads as ready, waiting, partly or fully refused, or not configured', () => {
    const quiet = { ...trivy, rejections_24h: 0, recent_rejections: [] };
    expect(uploadsVerdict(quiet)).toMatchObject({
      status: 'ok',
      title: 'Scan uploads are arriving',
    });
    expect(uploadsVerdict({ ...quiet, last_upload_at: null })).toMatchObject({
      status: 'none',
      title: 'Ready, waiting for the first upload',
    });
    expect(uploadsVerdict(trivy)).toMatchObject({ status: 'warn' });
    expect(uploadsVerdict({ ...trivy, uploads_24h: 0 })).toMatchObject({
      status: 'fail',
      title: 'Every recent upload was refused',
    });
    const unconfigured = uploadsVerdict({ ...trivy, configured: false });
    expect(unconfigured).toMatchObject({ status: 'fail', title: 'Scan uploads need attention' });
    expect(reasonText(unconfigured.reason)).toMatch(/GitLab URL is set in Settings/);
  });

  it('reads SonarQube from configuration, sync error, failed and unlinked deliveries', () => {
    const fine: SonarIntegration = {
      ...sonar,
      last_sync_error: null,
      recent_deliveries: [sonar.recent_deliveries[0]],
    };
    expect(sonarVerdict(fine)).toMatchObject({
      status: 'ok',
      title: 'SonarQube analyses are arriving',
    });
    expect(sonarVerdict({ ...fine, last_webhook_at: null })).toMatchObject({
      status: 'none',
      title: 'Ready, waiting for the first webhook',
    });
    expect(sonarVerdict({ ...fine, configured: false })).toMatchObject({
      status: 'fail',
      title: 'SonarQube needs attention',
    });
    expect(sonarVerdict(sonar)).toMatchObject({
      status: 'fail',
      title: 'The last sync with SonarQube failed',
    });
    const failed = sonarVerdict({ ...sonar, last_sync_error: null });
    expect(failed).toMatchObject({ status: 'warn', title: 'Some analyses could not be read' });
    expect(reasonText(failed.reason)).toBe('1 recent delivery failed. The detail column says why.');
    const unlinked = sonarVerdict({
      ...fine,
      recent_deliveries: [{ ...sonar.recent_deliveries[1], outcome: 'unlinked' }],
    });
    expect(unlinked).toMatchObject({
      status: 'warn',
      title: 'Analyses arrive for projects no service is linked to',
    });
  });
});
