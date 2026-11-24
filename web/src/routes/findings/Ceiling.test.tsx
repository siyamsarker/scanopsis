// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, renderHook, screen, waitFor } from '@testing-library/react';
import { useState, type ReactNode } from 'react';
import { MemoryRouter } from 'react-router';
import { createQueryClient } from '../../api/queryClient';
import { useAllVulnerabilities, type Vulnerability } from '../../api/findings';
import { json, mockFetch } from '../../test-utils';
import { FixTab } from './FixTab';
import { VulnerabilitiesTab } from './VulnerabilitiesTab';

function vuln(id: number, over: Partial<Vulnerability> = {}): Vulnerability {
  return {
    id,
    scan_id: 1,
    artifact_key: 'app',
    artifact_name: 'app',
    target: 'debian 12',
    class: 'os-pkgs',
    pkg_type: 'debian',
    pkg_name: `pkg${id}`,
    pkg_path: '',
    installed_version: '1.0',
    fixed_version: '1.1',
    vuln_id: `CVE-2024-${id}`,
    severity: 'HIGH',
    status: 'fixed',
    title: `Title ${id}`,
    primary_url: '',
    suppressed: false,
    suppression: '',
    first_seen_at: '2026-11-01T00:00:00Z',
    ...over,
  };
}

/** A server with `total` vulnerabilities in pages of the requested limit. */
function server(total: number, narrowedTotal = 3) {
  return mockFetch((call) => {
    const url = new URL(call.path, 'http://x');
    if (url.pathname !== '/api/v1/services/5/vulnerabilities') return undefined;
    const q = url.searchParams;
    const count = q.get('severity') || q.get('q') || q.get('fixable') ? narrowedTotal : total;
    const after = Number(q.get('after') ?? 0);
    const limit = Number(q.get('limit'));
    const ids = Array.from({ length: Math.min(limit, count - after) }, (_, i) => after + i + 1);
    const last = ids[ids.length - 1] ?? after;
    return json(200, { items: ids.map((id) => vuln(id)), next_after: last < count ? last : null });
  });
}

function Wrapper({ children }: { children: ReactNode }) {
  const [client] = useState(createQueryClient);
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

const params = (calls: { path: string }[]) =>
  calls.map((c) => new URL(c.path, 'http://x').searchParams);

describe('loading ceiling', () => {
  it('stops after the ceiling and reports it', async () => {
    const { calls } = server(2500);
    const { result } = renderHook(
      () => useAllVulnerabilities(5, 'main', false, true, undefined, 1000),
      { wrapper: Wrapper },
    );
    await waitFor(() => expect(result.current.truncated).toBe(true));
    expect(result.current.rows).toHaveLength(1000);
    expect(result.current.loadingMore).toBe(false);
    expect(result.current.complete).toBe(false);
    // No further request follows.
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(calls).toHaveLength(2);
  });

  it('sends the server-side filters once the ceiling is reached', async () => {
    const { calls } = server(2500);
    const { result } = renderHook(
      () =>
        useAllVulnerabilities(
          5,
          'main',
          false,
          true,
          { severity: 'CRITICAL,HIGH', fixable: true, q: 'ssl' },
          1000,
        ),
      { wrapper: Wrapper },
    );
    await waitFor(() => expect(result.current.narrowed).toBe(true));
    await waitFor(() => expect(result.current.complete).toBe(true));
    expect(result.current.truncated).toBe(false);
    expect(result.current.rows).toHaveLength(3);
    const all = params(calls);
    expect(all.slice(0, 2).every((p) => p.get('severity') === null)).toBe(true);
    const filtered = all.slice(2);
    expect(filtered).toHaveLength(1);
    expect(filtered[0].get('severity')).toBe('CRITICAL,HIGH');
    expect(filtered[0].get('fixable')).toBe('true');
    expect(filtered[0].get('q')).toBe('ssl');
  });

  it('keeps filtering on the client below the ceiling', async () => {
    const { calls } = server(700);
    const { result } = renderHook(
      () => useAllVulnerabilities(5, 'main', false, true, { severity: 'CRITICAL' }, 1000),
      { wrapper: Wrapper },
    );
    await waitFor(() => expect(result.current.complete).toBe(true));
    expect(result.current.rows).toHaveLength(700);
    expect(result.current.truncated).toBe(false);
    expect(result.current.narrowed).toBe(false);
    expect(params(calls).every((p) => p.get('severity') === null)).toBe(true);
  });
});

describe('truncation notices', () => {
  const noop = () => undefined;

  it('tells the explorer user to narrow the filters', () => {
    render(
      <MemoryRouter>
        <VulnerabilitiesTab
          serviceId={5}
          rows={[vuln(1)]}
          loading={false}
          loadingMore={false}
          truncated
          error={null}
          onRetry={noop}
        />
      </MemoryRouter>,
    );
    expect(
      screen.getByText('Showing the first 50 000 findings. Narrow the filters to see the rest.'),
    ).toBeInTheDocument();
  });

  it('shows no notice below the ceiling', () => {
    render(
      <MemoryRouter>
        <VulnerabilitiesTab
          serviceId={5}
          rows={[vuln(1)]}
          loading={false}
          loadingMore={false}
          error={null}
          onRetry={noop}
        />
      </MemoryRouter>,
    );
    expect(screen.queryByText(/Showing the first/)).not.toBeInTheDocument();
  });

  function fix(props: { truncated?: boolean; error?: unknown; onRetry?: () => void }) {
    return render(
      <MemoryRouter>
        <FixTab
          serviceId={5}
          urlRef={null}
          rows={[vuln(1), vuln(2)]}
          loading={false}
          loadingMore={false}
          error={props.error ?? null}
          truncated={props.truncated}
          onRetry={props.onRetry ?? noop}
        />
      </MemoryRouter>,
    );
  }

  it('states complete totals plainly', () => {
    fix({});
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(
      screen.getByText(/upgrading 2 packages clears 10 of 10 \(100%, 2 findings\)/),
    ).toBeInTheDocument();
  });

  it('warns at the ceiling without a retry and qualifies the headline', () => {
    fix({ truncated: true });
    expect(screen.getByRole('alert')).toHaveTextContent(
      'Loading stopped after 2 findings; these totals are incomplete.',
    );
    expect(screen.queryByRole('button', { name: 'Try again' })).not.toBeInTheDocument();
    expect(screen.getByText(/clears at least 10 of at least 10/)).toBeInTheDocument();
  });

  it('warns after an error with a retry', () => {
    const onRetry = vi.fn();
    fix({ error: new Error('network'), onRetry });
    expect(screen.getByRole('alert')).toHaveTextContent(
      'Loading stopped after 2 findings; these totals are incomplete.',
    );
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(onRetry).toHaveBeenCalledOnce();
    expect(screen.getByText(/clears at least 10 of at least 10/)).toBeInTheDocument();
  });
});
