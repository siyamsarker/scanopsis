// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { fireEvent, render, screen } from '@testing-library/react';
import { applyTheme, readStoredTheme, themeStorageKey } from '../theme';
import { ThemeSwitch } from './ThemeSwitch';

afterEach(() => {
  localStorage.clear();
  document.documentElement.removeAttribute('data-theme');
});

describe('theme storage', () => {
  it('defaults to system when nothing is saved or the value is invalid', () => {
    expect(readStoredTheme()).toBe('system');
    localStorage.setItem(themeStorageKey, 'purple');
    expect(readStoredTheme()).toBe('system');
  });

  it('reads a saved choice', () => {
    localStorage.setItem(themeStorageKey, 'light');
    expect(readStoredTheme()).toBe('light');
  });

  it('falls back to system when storage throws', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    expect(readStoredTheme()).toBe('system');
    vi.restoreAllMocks();
  });

  it('applies and removes the data-theme attribute', () => {
    applyTheme('dark');
    expect(document.documentElement).toHaveAttribute('data-theme', 'dark');
    applyTheme('system');
    expect(document.documentElement).not.toHaveAttribute('data-theme');
  });
});

describe('theme switch', () => {
  it('offers System, Light and Dark and marks System as current by default', () => {
    render(<ThemeSwitch />);
    const group = screen.getByRole('group', { name: 'Theme' });
    expect(group).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'System' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('button', { name: 'Light' })).toHaveAttribute('aria-pressed', 'false');
    expect(screen.getByRole('button', { name: 'Dark' })).toHaveAttribute('aria-pressed', 'false');
  });

  it('applies and saves the choice', () => {
    render(<ThemeSwitch />);
    fireEvent.click(screen.getByRole('button', { name: 'Dark' }));
    expect(document.documentElement).toHaveAttribute('data-theme', 'dark');
    expect(localStorage.getItem(themeStorageKey)).toBe('dark');
    expect(screen.getByRole('button', { name: 'Dark' })).toHaveAttribute('aria-pressed', 'true');
    fireEvent.click(screen.getByRole('button', { name: 'Light' }));
    expect(document.documentElement).toHaveAttribute('data-theme', 'light');
    expect(localStorage.getItem(themeStorageKey)).toBe('light');
  });

  it('returns to the system preference', () => {
    localStorage.setItem(themeStorageKey, 'dark');
    applyTheme('dark');
    render(<ThemeSwitch />);
    expect(screen.getByRole('button', { name: 'Dark' })).toHaveAttribute('aria-pressed', 'true');
    fireEvent.click(screen.getByRole('button', { name: 'System' }));
    expect(document.documentElement).not.toHaveAttribute('data-theme');
    expect(localStorage.getItem(themeStorageKey)).toBeNull();
  });

  it('still switches when storage is blocked', () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    render(<ThemeSwitch />);
    fireEvent.click(screen.getByRole('button', { name: 'Light' }));
    expect(document.documentElement).toHaveAttribute('data-theme', 'light');
    expect(screen.getByRole('button', { name: 'Light' })).toHaveAttribute('aria-pressed', 'true');
    vi.restoreAllMocks();
  });
});
