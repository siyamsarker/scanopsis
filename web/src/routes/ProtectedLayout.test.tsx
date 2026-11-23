// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { createQueryClient } from '../api/queryClient';
import { sidebarStorageKey } from '../components/shell/Sidebar';
import { apiError, json, makeUser, mockFetch, noContent, openMenu, renderApp } from '../test-utils';

const emptyPortfolio = {
  items: [],
  totals: { services: 0, failing_quality_gates: 0, services_with_critical: 0, fixable: 0 },
  generated_at: '2026-11-19T10:00:00Z',
};

const portfolio = {
  ...emptyPortfolio,
  items: [
    {
      service: {
        id: 7,
        name: 'payments-api',
        gitlab_project_path: 'platform/payments-api',
      },
    },
  ],
};

afterEach(() => {
  localStorage.clear();
  document.documentElement.removeAttribute('data-theme');
});

function signedIn(user = makeUser(), extra?: (path: string) => Response | undefined) {
  return mockFetch((call) => {
    if (call.path === '/api/v1/me') return json(200, user);
    if (call.path === '/api/v1/portfolio') return json(200, emptyPortfolio);
    return extra?.(call.path);
  });
}

/** The sidebar's navigation: in jsdom the phone bar renders too, but its menu stays closed. */
async function mainNav() {
  return screen.findByRole('navigation', { name: 'Main' });
}

describe('protected layout', () => {
  it('redirects anonymous users to the login page with return_to', async () => {
    mockFetch((call) => {
      if (call.path === '/api/v1/me') return apiError(401, 'unauthenticated', 'Sign in first.');
      if (call.path === '/api/v1/auth/methods') return json(200, { gitlab: true, local: false });
      return undefined;
    });
    renderApp('/admin/users?page=2');
    expect(await screen.findByRole('link', { name: 'Sign in with GitLab' })).toHaveAttribute(
      'href',
      '/api/v1/auth/gitlab/login?return_to=%2Fadmin%2Fusers%3Fpage%3D2',
    );
    expect(screen.getByTestId('location')).toHaveTextContent(
      '/login?return_to=%2Fadmin%2Fusers%3Fpage%3D2',
    );
  });

  it('shows a loading state while the account loads', async () => {
    mockFetch((call) =>
      call.path === '/api/v1/me' ? new Promise<Response>(() => {}) : json(200, { required: false }),
    );
    renderApp('/');
    expect(await screen.findByText('Loading your workspace…')).toBeInTheDocument();
  });

  it('lists every page in groups for admins, with the current one marked', async () => {
    signedIn(makeUser({ role: 'admin' }));
    renderApp('/');
    const nav = await mainNav();
    expect(Array.from(nav.querySelectorAll('a')).map((a) => a.textContent)).toEqual([
      'Portfolio',
      'Search',
      'Services',
      'Users',
      'Integrations',
      'Audit log',
      'Backups',
      'Settings',
    ]);
    for (const [name, href] of [
      ['Services', '/admin/services'],
      ['Users', '/admin/users'],
      ['Integrations', '/admin/integrations'],
      ['Audit log', '/admin/audit'],
      ['Backups', '/admin/backups'],
      ['Settings', '/settings'],
    ]) {
      expect(within(nav).getByRole('link', { name })).toHaveAttribute('href', href);
    }
    expect(within(nav).getByText('Overview')).toBeInTheDocument();
    expect(within(nav).getByText('Admin')).toBeInTheDocument();
    expect(within(nav).getByRole('link', { name: 'Portfolio' })).toHaveAttribute(
      'aria-current',
      'page',
    );
    expect(await screen.findByRole('heading', { name: 'Portfolio' })).toBeInTheDocument();
    expect(await screen.findByText('No services yet')).toBeInTheDocument();
  });

  it('hides the admin pages from viewers and falls back to the username', async () => {
    signedIn(makeUser({ role: 'viewer', name: '', username: 'grace' }));
    renderApp('/');
    const nav = await mainNav();
    expect(within(nav).queryByRole('link', { name: 'Users' })).not.toBeInTheDocument();
    expect(within(nav).queryByRole('link', { name: 'Backups' })).not.toBeInTheDocument();
    expect(within(nav).queryByText('Admin')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Account: grace, Viewer' })).toBeInTheDocument();
  });

  it('shows the signed-in person in the account menu with theme and sign-out', async () => {
    signedIn();
    renderApp('/');
    const trigger = await screen.findByRole('button', { name: 'Account: Ada Lovelace, Admin' });
    expect(trigger).toHaveTextContent('Ada Lovelace');
    expect(trigger).toHaveTextContent('Admin');
    const menu = await openMenu('Account: Ada Lovelace, Admin');
    expect(within(menu).getByText('ada')).toBeInTheDocument();
    expect(within(menu).getByRole('menuitem', { name: 'Your account' })).toBeInTheDocument();
    expect(within(menu).getByRole('menuitem', { name: 'Sign out' })).toBeInTheDocument();
    expect(within(menu).getByRole('menuitemradio', { name: 'System' })).toHaveAttribute(
      'aria-checked',
      'true',
    );
    fireEvent.click(within(menu).getByRole('menuitemradio', { name: 'Dark' }));
    expect(document.documentElement).toHaveAttribute('data-theme', 'dark');
  });

  it('shows the mark with the wordmark and the illustration in the empty state', async () => {
    signedIn();
    const { container } = renderApp('/');
    const [mark] = await screen.findAllByRole('link', { name: 'scanopsis' });
    expect(mark).toHaveAttribute('href', '/');
    expect(mark.querySelectorAll('svg path')).toHaveLength(2);
    await screen.findByText('No services yet');
    expect(container.querySelector('main [data-art="pipeline"]')).not.toBeNull();
  });

  it('collapses the sidebar to a rail and remembers it', async () => {
    signedIn();
    const first = renderApp('/');
    fireEvent.click(await screen.findByRole('button', { name: 'Collapse sidebar' }));
    expect(screen.getByRole('complementary', { name: 'Sidebar' })).toHaveAttribute(
      'data-collapsed',
      'true',
    );
    expect(localStorage.getItem(sidebarStorageKey)).toBe('collapsed');
    // The labels stay for screen readers.
    expect(within(await mainNav()).getByRole('link', { name: 'Portfolio' })).toBeInTheDocument();
    first.unmount();

    signedIn();
    renderApp('/');
    fireEvent.click(await screen.findByRole('button', { name: 'Expand sidebar' }));
    expect(localStorage.getItem(sidebarStorageKey)).toBeNull();
  });

  it('keeps working when storage is blocked', async () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    signedIn();
    renderApp('/');
    fireEvent.click(await screen.findByRole('button', { name: 'Collapse sidebar' }));
    expect(screen.getByRole('button', { name: 'Expand sidebar' })).toBeInTheDocument();
    vi.restoreAllMocks();
  });

  it('opens the phone menu as a sheet and closes it after navigating', async () => {
    signedIn();
    renderApp('/');
    fireEvent.click(await screen.findByRole('button', { name: 'Open menu' }));
    const sheet = await screen.findByRole('dialog', { name: 'Menu' });
    expect(within(sheet).getByRole('group', { name: 'Theme' })).toBeInTheDocument();
    expect(within(sheet).getByRole('button', { name: 'Sign out' })).toBeInTheDocument();
    fireEvent.click(within(sheet).getByRole('link', { name: 'Search' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Menu' })).toBeNull());
    await waitFor(() => expect(screen.getByTestId('location')).toHaveTextContent('/search'));
  });

  it('opens the command palette with Ctrl+K and jumps to a page', async () => {
    signedIn();
    renderApp('/');
    await mainNav();
    fireEvent.keyDown(window, { key: 'k', ctrlKey: true });
    const palette = await screen.findByRole('dialog', { name: 'Command palette' });
    const input = within(palette).getByRole('combobox');
    fireEvent.change(input, { target: { value: 'audit' } });
    fireEvent.click(await within(palette).findByRole('option', { name: /Audit log/ }));
    await waitFor(() => expect(screen.getByTestId('location')).toHaveTextContent('/admin/audit'));
    expect(screen.queryByRole('dialog', { name: 'Command palette' })).toBeNull();
  });

  it('lists services in the palette and offers to search a vulnerability ID', async () => {
    signedIn(makeUser({ role: 'viewer' }), (path) =>
      path === '/api/v1/portfolio' ? json(200, portfolio) : undefined,
    );
    mockFetch((call) => {
      if (call.path === '/api/v1/me') return json(200, makeUser({ role: 'viewer' }));
      if (call.path === '/api/v1/portfolio') return json(200, portfolio);
      return undefined;
    });
    renderApp('/account');
    fireEvent.click((await screen.findAllByRole('button', { name: 'Search or jump to' }))[0]);
    const palette = await screen.findByRole('dialog', { name: 'Command palette' });
    expect(
      await within(palette).findByRole('option', { name: /payments-api/ }),
    ).toBeInTheDocument();
    expect(within(palette).queryByRole('option', { name: /Integrations/ })).toBeNull();
    fireEvent.change(within(palette).getByRole('combobox'), {
      target: { value: 'cve-2024-3094' },
    });
    const offer = await within(palette).findByRole('option', {
      name: 'Search vulnerability CVE-2024-3094',
    });
    // The ID is a match, so the palette does not also claim that nothing matches.
    expect(within(palette).queryByText(/Nothing matches/)).toBeNull();
    fireEvent.click(offer);
    await waitFor(() =>
      expect(screen.getByTestId('location')).toHaveTextContent('/search?id=CVE-2024-3094'),
    );
  });

  it('switches the theme from the palette', async () => {
    signedIn();
    renderApp('/');
    await mainNav();
    fireEvent.keyDown(window, { key: 'k', metaKey: true });
    const palette = await screen.findByRole('dialog', { name: 'Command palette' });
    fireEvent.click(await within(palette).findByRole('option', { name: 'Use the light theme' }));
    expect(document.documentElement).toHaveAttribute('data-theme', 'light');
  });

  it('shows the shortcuts with ? and focuses the page search with /', async () => {
    signedIn();
    renderApp('/search');
    await mainNav();
    fireEvent.keyDown(document.body, { key: '?' });
    const dialog = await screen.findByRole('dialog', { name: 'Keyboard shortcuts' });
    expect(dialog).toHaveTextContent('Open the command palette');
    fireEvent.keyDown(dialog, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());

    const field = document.querySelector<HTMLElement>('main [data-search-field]');
    expect(field).not.toBeNull();
    act(() => {
      fireEvent.keyDown(document.body, { key: '/' });
    });
    expect(field).toHaveFocus();
    // While typing, "?" is just a character.
    fireEvent.keyDown(field!, { key: '?' });
    expect(screen.queryByRole('dialog', { name: 'Keyboard shortcuts' })).toBeNull();
  });

  it('signs out with a POST and goes to the login page', async () => {
    let signedOut = false;
    const { calls } = mockFetch((call) => {
      if (call.path === '/api/v1/auth/logout') {
        signedOut = true;
        return noContent();
      }
      if (call.path === '/api/v1/me') {
        return signedOut
          ? apiError(401, 'unauthenticated', 'Sign in first.')
          : json(200, makeUser());
      }
      if (call.path === '/api/v1/auth/methods') return json(200, { gitlab: true, local: true });
      return undefined;
    });
    renderApp('/');
    const menu = await openMenu(/^Account:/);
    fireEvent.click(within(menu).getByRole('menuitem', { name: 'Sign out' }));
    expect(await screen.findByRole('heading', { name: 'Sign in' })).toBeInTheDocument();
    expect(screen.getByTestId('location')).toHaveTextContent(/^\/login$/);
    expect(calls.some((c) => c.method === 'POST' && c.path === '/api/v1/auth/logout')).toBe(true);
  });

  it('shows an error toast and stays signed in when sign-out fails', async () => {
    mockFetch((call) => {
      if (call.path === '/api/v1/auth/logout') throw new TypeError('Failed to fetch');
      if (call.path === '/api/v1/me') return json(200, makeUser());
      return undefined;
    });
    renderApp('/');
    const menu = await openMenu(/^Account:/);
    fireEvent.click(within(menu).getByRole('menuitem', { name: 'Sign out' }));
    const region = screen.getByRole('region', { name: /Notifications/ });
    expect(
      await within(region).findByText('Signing out failed. Check your connection and try again.'),
    ).toBeInTheDocument();
    expect(screen.getByTestId('location')).toHaveTextContent(/^\/$/);
  });

  it('shows a retry button when the account cannot be loaded', async () => {
    mockFetch((call) =>
      call.path === '/api/v1/me' ? apiError(500, 'internal', 'Something broke.') : undefined,
    );
    renderApp('/');
    expect(await screen.findByRole('button', { name: 'Try again' })).toBeInTheDocument();
    expect(screen.getByTestId('location')).toHaveTextContent(/^\/$/);
  });

  it('shows a not-found page with a way home', async () => {
    signedIn();
    renderApp('/nowhere');
    expect(await screen.findByRole('heading', { name: 'Page not found' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Go to the portfolio' })).toHaveAttribute('href', '/');
  });

  it('clears the query cache when the session is gone', async () => {
    mockFetch((call) => {
      if (call.path === '/api/v1/me') return apiError(401, 'unauthenticated', 'Sign in first.');
      if (call.path === '/api/v1/auth/methods') return json(200, { gitlab: true, local: false });
      return undefined;
    });
    const client = createQueryClient();
    client.setQueryData(['users'], { pages: ['previous principal'] });
    renderApp('/', client);
    expect(await screen.findByRole('link', { name: 'Sign in with GitLab' })).toBeInTheDocument();
    expect(client.getQueryData(['users'])).toBeUndefined();
  });

  it('clears the query cache on sign-out, also when logout answers 401', async () => {
    for (const answer of [noContent, () => apiError(401, 'unauthenticated', 'Gone.')]) {
      let out = false;
      mockFetch((call) => {
        if (call.path === '/api/v1/auth/logout') {
          out = true;
          return answer();
        }
        if (call.path === '/api/v1/me')
          return out ? apiError(401, 'unauthenticated', 'Sign in first.') : json(200, makeUser());
        if (call.path === '/api/v1/auth/methods') return json(200, { gitlab: true, local: true });
        return undefined;
      });
      const client = createQueryClient();
      client.setQueryData(['anything'], 'cached');
      const view = renderApp('/', client);
      const menu = await openMenu(/^Account:/);
      fireEvent.click(within(menu).getByRole('menuitem', { name: 'Sign out' }));
      expect(await screen.findByRole('heading', { name: 'Sign in' })).toBeInTheDocument();
      expect(screen.getByTestId('location')).toHaveTextContent(/^\/login$/);
      expect(client.getQueryData(['anything'])).toBeUndefined();
      view.unmount();
    }
  });

  it('keeps the error and the session when logout answers 5xx', async () => {
    mockFetch((call) => {
      if (call.path === '/api/v1/auth/logout') return apiError(503, 'unavailable', 'Down.');
      if (call.path === '/api/v1/me') return json(200, makeUser());
      return undefined;
    });
    const client = createQueryClient();
    client.setQueryData(['anything'], 'cached');
    renderApp('/', client);
    const menu = await openMenu(/^Account:/);
    fireEvent.click(within(menu).getByRole('menuitem', { name: 'Sign out' }));
    expect(
      await screen.findAllByText('Signing out failed. Check your connection and try again.'),
    ).not.toHaveLength(0);
    expect(screen.getByTestId('location')).toHaveTextContent(/^\/$/);
    expect(client.getQueryData(['anything'])).toBe('cached');
  });
});
