// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { fireEvent, screen } from '@testing-library/react';
import { apiError, json, makeUser, mockFetch, noContent, renderApp } from '../test-utils';

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

  it('shows the Users link, name and role for admins', async () => {
    mockFetch((call) =>
      call.path === '/api/v1/me' ? json(200, makeUser({ role: 'admin' })) : undefined,
    );
    renderApp('/');
    expect(await screen.findByRole('link', { name: 'Users' })).toHaveAttribute(
      'href',
      '/admin/users',
    );
    expect(screen.getByRole('link', { name: 'Home' })).toBeInTheDocument();
    expect(screen.getByText('Ada Lovelace')).toBeInTheDocument();
    expect(screen.getByText('Admin')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Services' })).toBeInTheDocument();
    expect(
      screen.getByText(
        'No services yet. Services appear here after the first pipeline uploads a scan.',
      ),
    ).toBeInTheDocument();
  });

  it('hides the Users link for viewers and falls back to the username', async () => {
    mockFetch((call) =>
      call.path === '/api/v1/me'
        ? json(200, makeUser({ role: 'viewer', name: '', username: 'grace' }))
        : undefined,
    );
    renderApp('/');
    expect(await screen.findByText('grace')).toBeInTheDocument();
    expect(screen.getByText('Viewer')).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'Users' })).not.toBeInTheDocument();
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
    fireEvent.click(await screen.findByRole('button', { name: 'Sign out' }));
    expect(await screen.findByRole('heading', { name: 'Sign in' })).toBeInTheDocument();
    expect(screen.getByTestId('location')).toHaveTextContent(/^\/login$/);
    expect(calls.some((c) => c.method === 'POST' && c.path === '/api/v1/auth/logout')).toBe(true);
  });

  it('shows an error and stays signed in when sign-out fails', async () => {
    mockFetch((call) => {
      if (call.path === '/api/v1/auth/logout') throw new TypeError('Failed to fetch');
      if (call.path === '/api/v1/me') return json(200, makeUser());
      return undefined;
    });
    renderApp('/');
    fireEvent.click(await screen.findByRole('button', { name: 'Sign out' }));
    expect(
      await screen.findByText('Signing out failed. Check your connection and try again.'),
    ).toBeVisible();
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

  it('shows a not-found page with a link home', async () => {
    mockFetch((call) => (call.path === '/api/v1/me' ? json(200, makeUser()) : undefined));
    renderApp('/nowhere');
    expect(await screen.findByRole('heading', { name: 'Page not found' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'home page' })).toHaveAttribute('href', '/');
  });
});
