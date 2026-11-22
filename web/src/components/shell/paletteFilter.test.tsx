// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { render, screen } from '@testing-library/react';
import { Command } from 'cmdk';
import { isSubsequence, paletteFilter } from './paletteFilter';

const payments = ['payments-api', 'platform/payments-api'];
const searchPage = ['Search', 'vulnerability', 'cve', 'ghsa'];

describe('paletteFilter', () => {
  it('shows everything for an empty query', () => {
    expect(paletteFilter('page:/search', '  ', searchPage)).toBe(1);
  });

  it('ranks a service prefix above a page whose synonyms merely contain the letters', () => {
    const service = paletteFilter('service:1', 'pay', payments);
    const page = paletteFilter('page:/search', 'pay', searchPage);
    expect(service).toBeGreaterThan(0.9);
    expect(page).toBe(0);
  });

  it('puts exact names and paths first', () => {
    expect(paletteFilter('service:1', 'payments-api', payments)).toBe(1);
    expect(paletteFilter('service:1', 'platform/payments-api', payments)).toBe(1);
    expect(paletteFilter('service:1', 'payments-api', payments)).toBeGreaterThan(
      paletteFilter('page:/search', 'search', searchPage),
    );
  });

  it('orders prefix, word start, substring and scattered letters', () => {
    const name = (q: string) =>
      paletteFilter('service:2', q, ['billing-worker', 'team/billing-worker']);
    expect(name('bill')).toBeGreaterThan(name('work'));
    expect(name('work')).toBeGreaterThan(name('illing'));
    expect(name('illing')).toBeGreaterThan(name('bgw'));
    expect(name('bgw')).toBeGreaterThan(0);
    expect(name('zzz')).toBe(0);
  });

  it('matches pages by label first, then synonyms', () => {
    expect(paletteFilter('page:/search', 'sea', searchPage)).toBeGreaterThan(
      paletteFilter('page:/search', 'cve', searchPage),
    );
    expect(paletteFilter('page:/search', 'cve', searchPage)).toBeGreaterThan(0);
  });

  it('matches other commands by their label', () => {
    expect(paletteFilter('Use the dark theme', 'dark', ['theme'])).toBeGreaterThan(0);
    expect(paletteFilter('Sign out', 'logout', ['log out', 'logout'])).toBeGreaterThan(0);
    expect(paletteFilter('Sign out', 'pay', [])).toBe(0);
  });

  it('always keeps the vulnerability shortcut', () => {
    expect(paletteFilter('vulnerability:CVE-2024-1', 'cve-2024-1')).toBe(1);
  });

  it('finds subsequences', () => {
    expect(isSubsequence('pa', 'payments')).toBe(true);
    expect(isSubsequence('ap', 'pa')).toBe(false);
    expect(isSubsequence('', 'x')).toBe(true);
  });

  it('makes cmdk list the service before the page', () => {
    render(
      <Command label="Palette" filter={paletteFilter}>
        <Command.Input value="pay" readOnly />
        <Command.List>
          <Command.Group heading="Pages">
            <Command.Item value="page:/search" keywords={searchPage}>
              Search
            </Command.Item>
            <Command.Item value="page:/" keywords={['Portfolio', 'home', 'services']}>
              Portfolio
            </Command.Item>
          </Command.Group>
          <Command.Group heading="Services">
            <Command.Item value="service:1" keywords={payments}>
              payments-api
            </Command.Item>
          </Command.Group>
        </Command.List>
      </Command>,
    );
    const options = screen.getAllByRole('option').map((o) => o.textContent);
    expect(options[0]).toBe('payments-api');
    expect(options).not.toContain('Search');
  });
});
