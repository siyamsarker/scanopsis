// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { expect, test, type Browser, type BrowserContext, type Page } from '@playwright/test';
import { resetPasswordWithCli } from '../fixtures/stack';
import { freshCode } from '../fixtures/totp';
import {
  account,
  accountMenu,
  choose,
  cspViolations,
  select,
  signOut,
  watchCsp,
} from '../fixtures/ui';
import { registerDashboardFlows } from './dashboard-flows';

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
  const context = watchCsp(await browser.newContext());
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

// The code field sends the code as soon as it has six digits.
async function enterCode(page: Page, secret: string): Promise<void> {
  await page.getByLabel('6-digit code').fill(await freshCode(secret));
}

/** The enrolment key, read whole from the element that shows it in groups of four. */
async function shownSecret(page: Page): Promise<string> {
  const secret = page.getByLabel('Secret', { exact: true });
  await expect(secret).toHaveText(/^([A-Z2-7]{1,4} ?)+$/);
  return (await secret.getAttribute('data-secret')) ?? '';
}

/** Opens the row menu of a user or service and chooses an item. */
async function rowAction(page: Page, name: string, item: string): Promise<void> {
  await page.getByRole('button', { name: `Actions for ${name}` }).click();
  await page.getByRole('menuitem', { name: item }).click();
}

// Walks the enrolment screens and returns the secret that was shown.
async function enrol(page: Page): Promise<string> {
  await expect(page).toHaveURL(/\/2fa\/setup$/);
  await expect(page.getByRole('heading', { name: 'Set up two-factor sign-in' })).toBeVisible();
  const secret = await shownSecret(page);
  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByLabel('6-digit code').fill(await freshCode(secret));
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

test.afterAll(async () => {
  await rootContext?.close();
});

// No page may break the Content Security Policy (inline styles or scripts).
test.afterAll(() => {
  expect(cspViolations).toEqual([]);
});

test('1. setup creates the administrator and enrols two-factor', async ({ browser }) => {
  rootContext = watchCsp(await browser.newContext());
  root = await rootContext.newPage();

  await root.goto('/');
  await expect(root).toHaveURL(/\/setup$/);
  await expect(root.getByRole('heading', { name: 'Set up Scanopsis' })).toBeVisible();
  await expect(root.getByText('Step 1 of 3')).toBeVisible();

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
  // Setup continues its steps into the enrolment.
  await expect(root.getByText('Step 2 of 3')).toBeVisible();
  rootSecret = await shownSecret(root);
  expect(rootSecret).toMatch(/^[A-Z2-7]{16,}$/);
  await root.getByRole('button', { name: 'Next' }).click();
  // The code confirms itself on the sixth digit.
  await root.getByLabel('6-digit code').fill(await freshCode(rootSecret));

  const codes = root.getByRole('list', { name: 'Recovery codes' }).getByRole('listitem');
  await expect(codes).toHaveCount(10);
  recoveryCode = (await codes.first().innerText()).trim();
  // Four groups of five symbols from a 31-character alphabet.
  expect(recoveryCode).toMatch(/^[a-hjkmnp-z2-9]{5}(-[a-hjkmnp-z2-9]{5}){3}$/);

  const proceed = root.getByRole('button', { name: 'Continue' });
  await expect(proceed).toBeDisabled();
  await root.getByLabel('I have saved these codes').check();
  await expect(proceed).toBeEnabled();
  await proceed.click();

  await expect(root).toHaveURL(/\/$/);
  await expect(account(root).getByText('root')).toBeVisible();
  await expect(root.getByRole('heading', { name: 'Services' })).toBeVisible();
});

test('2. the session cookie is HttpOnly, SameSite=Lax, ends with the browser and is not in the page', async () => {
  const cookies = await rootContext.cookies();
  const session = cookies.find((c) => c.name.endsWith('scanopsis_session'));
  expect(session, 'session cookie present').toBeDefined();
  expect(session?.httpOnly).toBe(true);
  expect(session?.sameSite).toBe('Lax');
  // -1: a browser-session cookie without Expires or Max-Age.
  expect(session?.expires).toBe(-1);

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
  await expect(root.getByText('That code is not correct.')).toBeVisible();

  await enterCode(root, rootSecret);
  await expect(root).toHaveURL(/\/$/);
  await expect(account(root).getByText('root')).toBeVisible();

  // A recovery code in place of an app code.
  await signOut(root);
  await signInLocal(root, 'root', strongPassword);
  await root.getByRole('button', { name: 'Use a recovery code' }).click();
  await root.getByLabel('Recovery code').fill(recoveryCode);
  await root.getByRole('button', { name: 'Verify' }).click();
  await expect(root).toHaveURL(/\/$/);
  await expect(account(root).getByText('root')).toBeVisible();

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
  const local = root.getByRole('switch', { name: 'Local accounts' });
  const saveBar = root.getByRole('region', { name: 'Unsaved changes' });
  await expect(local).toBeChecked();
  await expect(saveBar).toBeHidden();
  await local.click();
  await expect(local).not.toBeChecked();
  await expect(saveBar).toContainText('1 unsaved change');
  await saveBar.getByRole('button', { name: 'Save changes' }).click();
  await expect(saveBar.getByText('Nothing was saved.')).toBeVisible();
  await expect(
    root.getByText('Local sign-in can be disabled only when GitLab sign-in is fully configured.'),
  ).toBeVisible();
  // Discard puts the switch back without saving.
  await saveBar.getByRole('button', { name: 'Discard' }).click();
  await expect(local).toBeChecked();
  await root.reload();
  await expect(local).toBeChecked();

  // Configure GitLab against the fake provider.
  await root.getByLabel('GitLab URL', { exact: true }).fill('http://fake-gitlab:19000');
  await root.getByLabel('Application ID', { exact: true }).fill('e2e-client');
  await root.getByLabel('Application secret', { exact: true }).fill(gitlabSecret);
  await expect(saveBar).toContainText('3 unsaved changes');
  await saveBar.getByRole('button', { name: 'Save changes' }).click();
  await expect(root.getByText('Settings saved', { exact: true })).toBeVisible();
  await expect(root.getByText('Set here', { exact: true })).toHaveCount(3);

  // The secret is saved, and never shown again, on screen or from the API.
  await root.reload();
  await expect(root.getByText('Set here', { exact: true })).toHaveCount(3);
  await expect(root.getByLabel('Application secret', { exact: true })).toHaveValue('');
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

  await expect(account(alice).getByText('Alice Example')).toBeVisible();
  await expect(account(alice).getByText('Viewer')).toBeVisible();
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
  await expect(select(root, 'Role for alice')).toHaveText('Viewer');
  await choose(root, 'Role for alice', 'Admin');
  await expect(root.getByText('Alice Example is now an admin.', { exact: true })).toBeVisible();

  await alice.goto('/');
  await expect(account(alice).getByText('Admin', { exact: true })).toBeVisible();
  await expect(
    alice.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Users' }),
  ).toBeVisible();
});

test('7. the group allow-list admits subgroups and refuses others', async ({ browser }) => {
  await root.goto('/settings');
  await root.getByLabel('Groups that may sign in', { exact: true }).fill('platform');
  await root.getByRole('button', { name: 'Save changes' }).click();
  await expect(root.getByText('Settings saved', { exact: true })).toBeVisible();

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
  await expect(root.getByRole('row', { name: /^Alice Example/ })).toContainText('On');
  await rowAction(root, 'alice', 'Reset two-factor');
  const dialog = root.getByRole('alertdialog');
  await expect(dialog.getByText('Reset two-factor for Alice Example?')).toBeVisible();
  await dialog.getByRole('button', { name: 'Reset two-factor' }).click();
  await expect(
    root.getByText('Alice Example sets it up again at the next sign-in.', { exact: true }),
  ).toBeVisible();
  await expect(root.getByRole('row', { name: /^Alice Example/ })).toContainText('Not set up');

  await alice.reload();
  await expect(alice).toHaveURL(/\/login/);
  await expect(alice.getByRole('heading', { name: 'Sign in' })).toBeVisible();

  await signInWithGitLab(alice, 'alice');
  await expect(alice).toHaveURL(/\/2fa\/setup$/);
  const secret = await enrol(alice);
  expect(secret).not.toBe(aliceSecret);
  aliceSecret = secret;
  await expect(account(alice).getByText('Alice Example')).toBeVisible();
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
  await expect(account(root).getByText('root')).toBeVisible();

  // The password in use before the reset no longer works.
  const other = await newUser(browser);
  await signInLocal(other.page, 'root', strongPassword);
  await expect(other.page.getByText('Wrong username or password.')).toBeVisible();
  await other.context.close();
});

test('10. the theme choice is applied and kept', async () => {
  const html = root.locator('html');
  await account(root).click();
  await root.getByRole('menuitemradio', { name: 'Light' }).click();
  await expect(html).toHaveAttribute('data-theme', 'light');

  await root.reload();
  await expect(html).toHaveAttribute('data-theme', 'light');
  await account(root).click();
  await expect(root.getByRole('menuitemradio', { name: 'Light' })).toHaveAttribute(
    'aria-checked',
    'true',
  );
  await root.getByRole('menuitemradio', { name: 'System' }).click();
  await expect(html).not.toHaveAttribute('data-theme', /.*/);

  // The command palette switches it too.
  await root.keyboard.press('ControlOrMeta+k');
  const palette = root.getByRole('dialog', { name: 'Command palette' });
  await palette.getByRole('combobox').fill('dark theme');
  await palette.getByRole('option', { name: 'Use the dark theme' }).click();
  await expect(html).toHaveAttribute('data-theme', 'dark');
  await expect(palette).toHaveCount(0);
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

// Flows 12 to 20 upload real Trivy reports and check every dashboard screen.
registerDashboardFlows({
  root: () => root,
  rootContext: () => rootContext,
  viewer: () => alice,
});

test('22. a return_to that tries to leave the site ends on a local page', async ({ browser }) => {
  const { context, page } = await newUser(browser);
  const hosts = new Set<string>();
  page.on('request', (request) => hosts.add(new URL(request.url()).hostname));

  // "/./\evil.example" passed a prefix check and was cleaned to "/\evil.example",
  // which browsers resolve to https://evil.example/.
  await page.goto(`/api/v1/auth/gitlab/login?return_to=${encodeURIComponent('/./\\evil.example')}`);
  await expect(page.getByRole('heading', { name: 'Fake GitLab sign-in' })).toBeVisible();
  await page.getByRole('link', { name: 'alice', exact: true }).click();
  await expect(page).toHaveURL(/^http:\/\/localhost:18080\/2fa(\?|$)/);
  await enterCode(page, aliceSecret);
  await expect(page).toHaveURL('http://localhost:18080/');
  expect([...hosts].filter((h) => h.includes('evil'))).toEqual([]);

  // The same rule applies when the sign-in page passes return_to on.
  await page.goto(`/login?return_to=${encodeURIComponent('/%2e%2e/%5Cevil.example')}`);
  await expect(page).toHaveURL('http://localhost:18080/');
  await context.close();
});

test('23. the account page lists sessions and ends other ones', async ({ browser }) => {
  // Another browser signs in as root and completes the second factor.
  const laptop = await newUser(browser);
  await signInLocal(laptop.page, 'root', newPassword);
  await enterCode(laptop.page, rootSecret);
  await expect(laptop.page).toHaveURL(/\/$/);

  await accountMenu(root, 'Your account');
  await expect(root).toHaveURL(/\/account$/);
  const list = root.getByRole('list', { name: 'Sessions' });
  await expect(list.getByRole('listitem')).toHaveCount(2);
  await expect(list.getByText('This browser')).toHaveCount(1);
  await list.getByRole('button', { name: /^End session/ }).click();
  await root.getByRole('alertdialog').getByRole('button', { name: 'End session' }).click();
  await expect(root.getByText('Session ended', { exact: true })).toBeVisible();
  await expect(list.getByRole('listitem')).toHaveCount(1);

  await laptop.page.reload();
  await expect(laptop.page).toHaveURL(/\/login/);
  await laptop.context.close();

  // A browser that stopped after the password shows up too, and ends with the others.
  const phone = await newUser(browser);
  await signInLocal(phone.page, 'root', newPassword);
  await expect(phone.page.getByRole('heading', { name: 'Enter your code' })).toBeVisible();
  await root.reload();
  await expect(list.getByText('Second factor not completed')).toBeVisible();
  await root.getByRole('button', { name: 'End all other sessions' }).click();
  await root.getByRole('alertdialog').getByRole('button', { name: 'End other sessions' }).click();
  await expect(root.getByText('1 session signed out. This browser stays signed in.', { exact: true })).toBeVisible();
  await expect(list.getByRole('listitem')).toHaveCount(1);
  await expect(root.getByRole('button', { name: 'End all other sessions' })).toBeDisabled();

  await phone.page.reload();
  await expect(phone.page).toHaveURL(/\/login/);
  await phone.context.close();

  await root.goto('/admin/audit?action=session.revoked');
  const audit = root.getByRole('main');
  await expect(audit.getByRole('listitem')).toHaveCount(2);
  await expect(audit).toContainText(/root.*ended 1 other session/);
  await expect(audit).toContainText(/root.*ended one of their sessions/);
});

test('24. an administrator disables, enables and deletes users', async () => {
  await root.goto('/admin/users');
  const row = (name: string) => root.getByRole('row', { name: new RegExp(`^${name}\\b`) });
  // Your own account cannot be disabled or deleted.
  await root.getByRole('button', { name: 'Actions for root' }).click();
  await expect(root.getByRole('menuitem', { name: 'Disable user' })).toBeDisabled();
  await expect(root.getByRole('menuitem', { name: 'Delete user' })).toBeDisabled();
  await root.keyboard.press('Escape');

  // Disabling ends alice's session and refuses her next GitLab sign-in.
  await rowAction(root, 'alice', 'Disable user');
  await root.getByRole('alertdialog').getByRole('button', { name: 'Disable user' }).click();
  await expect(
    root.getByText('Alice Example was signed out everywhere and cannot sign in.', { exact: true }),
  ).toBeVisible();
  await expect(row('Alice Example').getByText('Disabled', { exact: true })).toBeVisible();

  await alice.reload();
  await expect(alice).toHaveURL(/\/login/);
  await signInWithGitLab(alice, 'alice');
  await expect(alice).toHaveURL(/\/login\?error=account_disabled/);
  await expect(
    alice.getByText('Your account is disabled. Ask an administrator to enable it.'),
  ).toBeVisible();

  // Enabling restores access, with the same two-factor setup.
  await rowAction(root, 'alice', 'Enable user');
  await expect(root.getByText('Alice Example can sign in again.', { exact: true })).toBeVisible();
  await signInWithGitLab(alice, 'alice');
  await enterCode(alice, aliceSecret);
  await expect(alice).toHaveURL(/\/$/);
  await expect(account(alice).getByText('Alice Example')).toBeVisible();

  // Deleting removes carol from the list.
  await rowAction(root, 'carol', 'Delete user');
  await root.getByRole('alertdialog').getByRole('button', { name: 'Delete user' }).click();
  await expect(root.getByText('User deleted', { exact: true })).toBeVisible();
  await expect(row('Carol Example')).toHaveCount(0);

  await root.goto('/admin/audit');
  const audit = root.getByRole('main');
  for (const sentence of ['disabled alice', 'enabled alice', 'deleted carol']) {
    await expect(audit).toContainText(sentence);
  }
});

test('25. a settings change is audited with the values before and after', async () => {
  await root.goto('/settings');
  await root.getByLabel('Idle timeout', { exact: true }).fill('45m');
  await root.getByRole('button', { name: 'Save changes' }).click();
  await expect(root.getByText('Settings saved', { exact: true })).toBeVisible();

  await root.goto('/admin/audit');
  await choose(root, 'Action', 'Settings changed');
  await expect(root).toHaveURL(/action=settings\.changed/);
  const audit = root.getByRole('main');
  const newest = audit.getByRole('listitem').first();
  await expect(newest).toContainText(/root.*changed 1 setting/);
  const change = newest.getByRole('definition');
  await expect(newest.getByRole('term')).toHaveText('Idle timeout');
  await expect(change).toHaveText('from 30m to 45m');
  // Flow 4 saved the GitLab client secret: the log says only that it changed.
  await expect(audit).toContainText('Changed; secret values are never recorded');
  await expect(audit).not.toContainText(gitlabSecret);
  const response = await rootContext.request.get('/api/v1/admin/audit?action=settings.changed');
  expect(await response.text()).not.toContain(gitlabSecret);
  const page = (await response.json()) as {
    items: { detail: { changes?: Record<string, unknown>[] } }[];
  };
  const changes = page.items.flatMap((item) => item.detail.changes ?? []);
  expect(changes).toContainEqual({ key: 'gitlab.client_secret', changed: true });
});
