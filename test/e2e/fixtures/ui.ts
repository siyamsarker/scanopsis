// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { expect, type BrowserContext, type Locator, type Page } from '@playwright/test';

/** The account menu button at the foot of the sidebar; it shows the name and the role. */
export function account(page: Page): Locator {
  return page
    .getByRole('complementary', { name: 'Sidebar' })
    .getByRole('button', { name: /^Account:/ });
}

/** Opens the account menu and chooses an item. */
export async function accountMenu(page: Page, item: string): Promise<void> {
  await account(page).click();
  await page.getByRole('menuitem', { name: item }).click();
}

export async function signOut(page: Page): Promise<void> {
  await accountMenu(page, 'Sign out');
  await expect(page).toHaveURL(/\/login$/);
}

/** The select (a combobox) named by its label. */
export function select(page: Page, label: string): Locator {
  return page.getByRole('combobox', { name: label, exact: true });
}

/** Opens a select and chooses the option with this name. */
export async function choose(page: Page, label: string, option: string | RegExp): Promise<void> {
  await select(page, label).click();
  const name = typeof option === 'string' ? { name: option, exact: true } : { name: option };
  await page.getByRole('listbox').getByRole('option', name).click();
  await expect(page.getByRole('listbox')).toHaveCount(0);
}

/**
 * Collects Content Security Policy violations reported in the console of
 * every page of a browser context. The flows assert that none happened.
 */
export const cspViolations: string[] = [];

export function watchCsp(context: BrowserContext): BrowserContext {
  context.on('console', (message) => {
    const text = message.text();
    if (/Content Security Policy/i.test(text)) cspViolations.push(text);
  });
  context.on('weberror', (error) => {
    const text = String(error.error());
    if (/Content Security Policy/i.test(text)) cspViolations.push(text);
  });
  return context;
}
