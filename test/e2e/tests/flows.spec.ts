// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { expect, test, type Browser, type BrowserContext, type Page } from '@playwright/test';
import { resetPasswordWithCli } from '../fixtures/stack';
import { freshCode } from '../fixtures/totp';

// The flows share one database and run in order; each one starts from the
// state the previous one left, which the comments at the top of each state.
test.describe.configure({ mode: 'serial' });

const strongPassword = 'correct horse battery staple 1';
const newPassword = 'a second, different passphrase 2';
const gitlabSecret = 'e2e-client-secret';

// State handed from one flow to the next.
let rootContext: BrowserContext;
let root: Page;
let rootSecret = '';
let recoveryCode = '';
let alice: Page;
let aliceSecret = '';

async function newUser(browser: Browser): Promise<{ context: BrowserContext; page: Page }> {
  const context = await browser.newContext();
  return { context, page: await context.newPage() };
}

function username(page: Page) {
  return page.getByLabel('Username');
}

async function signInLocal(page: Page, user: string, password: string): Promise<void> {
  await page.goto('/login');
  await username(page).fill(user);
  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Sign in' }).click();
}

async function enterCode(page: Page, secret: string): Promise<void> {
  await page.getByLabel('6-digit code').fill(await freshCode(secret));
  await page.getByRole('button', { name: 'Verify' }).click();
}

async function signOut(page: Page): Promise<void> {
  await page.getByRole('button', { name: 'Sign out' }).click();
  await expect(page).toHaveURL(/\/login$/);
}

// Walks the enrolment screens and returns the secret that was shown.
async function enrol(page: Page): Promise<string> {
  await expect(page).toHaveURL(/\/2fa\/setup$/);
  await expect(page.getByRole('heading', { name: 'Set up two-factor sign-in' })).toBeVisible();
  const secret = (await page.getByLabel('Secret', { exact: true }).innerText()).trim();
  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByLabel('6-digit code').fill(await freshCode(secret));
  await page.getByRole('button', { name: 'Confirm' }).click();
  await expect(page.getByRole('list', { name: 'Recovery codes' })).toBeVisible();
  await page.getByLabel('I have saved these codes').check();
  await page.getByRole('button', { name: 'Continue' }).click();
  await expect(page).toHaveURL(/\/$/);
  return secret;
}

// Starts GitLab sign-in and picks a user on the fake provider's list page.
async function signInWithGitLab(page: Page, user: string): Promise<void> {
  await page.goto('/login');
  await page.getByRole('link', { name: 'Sign in with GitLab' }).click();
  await expect(page.getByRole('heading', { name: 'Fake GitLab sign-in' })).toBeVisible();
  await page.getByRole('link', { name: user, exact: true }).click();
}

function header(page: Page) {
  return page.getByRole('banner');
}

test.afterAll(async () => {
  await rootContext?.close();
});

test('1. setup creates the administrator and enrols two-factor', async ({ browser }) => {
  rootContext = await browser.newContext();
  root = await rootContext.newPage();

  await root.goto('/');
  await expect(root).toHaveURL(/\/setup$/);
  await expect(root.getByRole('heading', { name: 'Set up Scanopsis' })).toBeVisible();

  const create = async (token: string) => {
    await root.getByLabel('Setup token').fill(token);
    await username(root).fill('root');
    await root.getByLabel('Password', { exact: true }).fill(strongPassword);
    await root.getByLabel('Confirm password').fill(strongPassword);
    await root.getByRole('button', { name: 'Create administrator' }).click();
  };

  await create('not-the-token');
  await expect(root.getByText('The setup token is not correct.')).toBeVisible();
  await expect(root).toHaveURL(/\/setup$/);

  await create(process.env.E2E_SETUP_TOKEN ?? '');
  await expect(root).toHaveURL(/\/2fa\/setup$/);

  // Enrolment: QR code and secret, then a code from that secret.
  await expect(root.getByRole('img', { name: 'QR code for your authenticator app' })).toBeVisible();
  rootSecret = (await root.getByLabel('Secret', { exact: true }).innerText()).trim();
  expect(rootSecret).toMatch(/^[A-Z2-7]{16,}$/);
  await root.getByRole('button', { name: 'Next' }).click();
  await root.getByLabel('6-digit code').fill(await freshCode(rootSecret));
  await root.getByRole('button', { name: 'Confirm' }).click();

  const codes = root.getByRole('list', { name: 'Recovery codes' }).getByRole('listitem');
  await expect(codes).toHaveCount(10);
  recoveryCode = (await codes.first().innerText()).trim();
  expect(recoveryCode).not.toBe('');

  const proceed = root.getByRole('button', { name: 'Continue' });
  await expect(proceed).toBeDisabled();
  await root.getByLabel('I have saved these codes').check();
  await expect(proceed).toBeEnabled();
  await proceed.click();

  await expect(root).toHaveURL(/\/$/);
  await expect(header(root).getByText('root')).toBeVisible();
  await expect(root.getByRole('heading', { name: 'Services' })).toBeVisible();
});

test('2. the session cookie is HttpOnly, SameSite=Lax and not in the page', async () => {
  const cookies = await rootContext.cookies();
  const session = cookies.find((c) => c.name.endsWith('scanopsis_session'));
  expect(session, 'session cookie present').toBeDefined();
  expect(session?.httpOnly).toBe(true);
  expect(session?.sameSite).toBe('Lax');

  const value = session?.value ?? '';
  expect(value.length).toBeGreaterThan(16);
  expect(await root.content()).not.toContain(value);
  expect(await root.evaluate(() => document.cookie)).not.toContain(value);
});

test('3. sign-in asks for the second factor; recovery codes work once', async () => {
  await signOut(root);

  await signInLocal(root, 'root', strongPassword);
  await expect(root.getByRole('heading', { name: 'Enter your code' })).toBeVisible();

  await root.getByLabel('6-digit code').fill('000000');
  await root.getByRole('button', { name: 'Verify' }).click();
  await expect(root.getByText('That code is not correct.')).toBeVisible();

  await enterCode(root, rootSecret);
  await expect(root).toHaveURL(/\/$/);
  await expect(header(root).getByText('root')).toBeVisible();

  // A recovery code in place of an app code.
  await signOut(root);
  await signInLocal(root, 'root', strongPassword);
  await root.getByRole('button', { name: 'Use a recovery code' }).click();
  await root.getByLabel('Recovery code').fill(recoveryCode);
  await root.getByRole('button', { name: 'Verify' }).click();
  await expect(root).toHaveURL(/\/$/);
  await expect(header(root).getByText('root')).toBeVisible();

  // The same code a second time is refused.
  await signOut(root);
  await signInLocal(root, 'root', strongPassword);
  await root.getByRole('button', { name: 'Use a recovery code' }).click();
  await root.getByLabel('Recovery code').fill(recoveryCode);
  await root.getByRole('button', { name: 'Verify' }).click();
  await expect(
    root.getByText('That recovery code is not correct, or it was already used.'),
  ).toBeVisible();

  // Finish the sign-in with the app code so the next flow starts signed in.
  // The wait for a new time step also outlasts the attempt limit.
  await root.getByRole('button', { name: 'Use an authenticator code' }).click();
  await enterCode(root, rootSecret);
  await expect(root).toHaveURL(/\/$/);
});

test('4. settings: GitLab sign-in is configured and the secret stays hidden', async () => {
  await root.goto('/settings');
  await expect(root.getByRole('heading', { name: 'Settings' })).toBeVisible();

  // Local sign-in cannot be turned off while GitLab is not configured.
  const local = root.getByRole('switch', { name: /sign in with a local username and password/ });
  await expect(local).toBeChecked();
  await local.uncheck();
  await root.getByRole('button', { name: 'Save changes' }).click();
  await expect(root.getByText('Some settings need attention. Nothing was saved.')).toBeVisible();
  await expect(
    root.getByText('Local sign-in can be disabled only when GitLab sign-in is fully configured.'),
  ).toBeVisible();
  await root.reload();
  await expect(local).toBeChecked();

  // Configure GitLab against the fake provider.
  await root.getByLabel('Base URL of the GitLab instance').fill('http://fake-gitlab:19000');
  await root.getByLabel('Application ID of the GitLab OAuth application').fill('e2e-client');
  await root.getByLabel('Secret of the GitLab OAuth application').fill(gitlabSecret);
  await root.getByRole('button', { name: 'Save changes' }).click();
  await expect(root.getByText('Settings saved. Changes apply now.')).toBeVisible();
  await expect(root.getByText('Set here', { exact: true })).toHaveCount(3);

  // The secret is saved, and never shown again, on screen or from the API.
  await root.reload();
  await expect(root.getByText('Set here', { exact: true })).toHaveCount(3);
  await expect(root.getByLabel('Secret of the GitLab OAuth application')).toHaveValue('');
  expect(await root.content()).not.toContain(gitlabSecret);
  const settings = await rootContext.request.get('/api/v1/settings');
  expect(settings.ok()).toBe(true);
  expect(await settings.text()).not.toContain(gitlabSecret);

  const methods = await rootContext.request.get('/api/v1/auth/methods');
  expect(await methods.json()).toMatchObject({ gitlab: true, local: true });
});

test('5. a GitLab user enrols two-factor and lands as a viewer', async ({ browser }) => {
  const user = await newUser(browser);
  alice = user.page;

  await signInWithGitLab(alice, 'alice');
  aliceSecret = await enrol(alice);

  await expect(header(alice).getByText('Alice Example')).toBeVisible();
  await expect(header(alice).getByText('Viewer')).toBeVisible();
  const nav = alice.getByRole('navigation', { name: 'Main' });
  await expect(nav.getByRole('link', { name: 'Portfolio' })).toBeVisible();
  await expect(nav.getByRole('link', { name: 'Integrations' })).toHaveCount(0);
  await expect(nav.getByRole('link', { name: 'Users' })).toHaveCount(0);
  await expect(nav.getByRole('link', { name: 'Settings' })).toHaveCount(0);

  await alice.goto('/admin/users');
  await expect(alice.getByText('You need the admin role to see this page.')).toBeVisible();

  const status = await alice.evaluate(async () => (await fetch('/api/v1/users')).status);
  expect(status).toBe(403);
});

test('6. an administrator promotes a viewer', async () => {
  await root.goto('/admin/users');
  const role = root.getByLabel('Role for alice');
  await expect(role).toHaveValue('viewer');
  await role.selectOption('admin');
  await expect(root.getByText('Role changed to Admin.')).toBeVisible();

  await alice.goto('/');
  await expect(header(alice).getByText('Admin', { exact: true })).toBeVisible();
  await expect(
    alice.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Users' }),
  ).toBeVisible();
});

test('7. the group allow-list admits subgroups and refuses others', async ({ browser }) => {
  await root.goto('/settings');
  await root.getByLabel(/whose members may sign in/).fill('platform');
  await root.getByRole('button', { name: 'Save changes' }).click();
  await expect(root.getByText('Settings saved. Changes apply now.')).toBeVisible();

  const bob = await newUser(browser);
  await signInWithGitLab(bob.page, 'bob');
  await expect(bob.page).toHaveURL(/\/login\?error=group_not_allowed/);
  await expect(
    bob.page.getByText('Your GitLab account is not in a group that may use Scanopsis.'),
  ).toBeVisible();
  await bob.context.close();

  const carol = await newUser(browser);
  await signInWithGitLab(carol.page, 'carol');
  await expect(carol.page).toHaveURL(/\/2fa\/setup$/);
  await expect(
    carol.page.getByRole('heading', { name: 'Set up two-factor sign-in' }),
  ).toBeVisible();
  await carol.context.close();
});

test('8. resetting two-factor ends the sessions and forces a new enrolment', async () => {
  await root.goto('/admin/users');
  await root.getByRole('button', { name: 'Reset two-factor for alice' }).click();
  const dialog = root.getByRole('alertdialog');
  await expect(dialog.getByText('Reset two-factor for alice?')).toBeVisible();
  await dialog.getByRole('button', { name: 'Reset two-factor' }).click();
  await expect(
    root.getByText('Two-factor reset. alice sets it up again at the next sign-in.'),
  ).toBeVisible();

  await alice.reload();
  await expect(alice).toHaveURL(/\/login/);
  await expect(alice.getByRole('heading', { name: 'Sign in' })).toBeVisible();

  await signInWithGitLab(alice, 'alice');
  await expect(alice).toHaveURL(/\/2fa\/setup$/);
  const secret = await enrol(alice);
  expect(secret).not.toBe(aliceSecret);
  await expect(header(alice).getByText('Alice Example')).toBeVisible();
});

test('9. a CLI password reset ends the session and forces a new password', async ({
  browser,
}) => {
  const temporary = await resetPasswordWithCli('root');

  await root.reload();
  await expect(root).toHaveURL(/\/login/);

  await signInLocal(root, 'root', temporary);
  await enterCode(root, rootSecret);
  await expect(root).toHaveURL(/\/password$/);
  await expect(root.getByRole('heading', { name: 'Choose a new password' })).toBeVisible();

  const change = async (next: string) => {
    await root.getByLabel('Current password').fill(temporary);
    await root.getByLabel('New password', { exact: true }).fill(next);
    await root.getByLabel('Confirm new password').fill(next);
    await root.getByRole('button', { name: 'Change password' }).click();
  };

  await change('aaaaaaaaaaaa');
  await expect(root.getByText('The password must not be a single repeated character.')).toBeVisible();
  await expect(root).toHaveURL(/\/password$/);

  await change(newPassword);
  await expect(root).toHaveURL(/\/$/);
  await expect(header(root).getByText('root')).toBeVisible();

  // The password in use before the reset no longer works.
  const other = await newUser(browser);
  await signInLocal(other.page, 'root', strongPassword);
  await expect(other.page.getByText('Wrong username or password.')).toBeVisible();
  await other.context.close();
});

test('10. the theme choice is applied and kept', async () => {
  const html = root.locator('html');
  await root.getByRole('button', { name: 'Light' }).click();
  await expect(html).toHaveAttribute('data-theme', 'light');

  await root.reload();
  await expect(html).toHaveAttribute('data-theme', 'light');
  await expect(root.getByRole('button', { name: 'Light' })).toHaveAttribute('aria-pressed', 'true');

  await root.getByRole('button', { name: 'System' }).click();
  await expect(html).not.toHaveAttribute('data-theme', /.*/);
});

test('11. security headers are set', async () => {
  const page = await rootContext.request.get('/');
  const csp = page.headers()['content-security-policy'];
  expect(csp, 'Content-Security-Policy').toBeTruthy();
  expect(csp).not.toContain("'unsafe-inline'");
  expect(page.headers()['x-frame-options']).toBe('DENY');

  const me = await rootContext.request.get('/api/v1/me');
  expect(me.status()).toBe(200);
  expect(me.headers()['cache-control']).toContain('no-store');
});
