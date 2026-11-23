// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { readFileSync } from 'node:fs';
import { gunzipSync } from 'node:zlib';
import { expect, test, type Browser, type BrowserContext, type Locator, type Page } from '@playwright/test';
import { backupStack, compose, emptyService, readSetupToken } from '../fixtures/stack';
import { freshCode } from '../fixtures/totp';
import { account, watchCsp } from '../fixtures/ui';

// The backup tests run on a stack of their own (compose.backups.yaml): a
// restore replaces all data and ends every session, and the setup screen needs
// an empty installation. `scanopsis` on port 18090 is set up and backed up;
// `scanopsis-empty` on 18091 starts empty and takes the downloaded file.
// The flows build on each other and run in order.
test.describe.configure({ mode: 'serial' });

const mainURL = 'http://localhost:18090';
const emptyURL = 'http://localhost:18091';
const password = 'correct horse battery staple 1';
const serviceName = 'payments-api';
const renamedService = 'renamed-by-mistake';

// Content Security Policy violations seen by the browsers of this file only.
const cspViolations: string[] = [];

let adminContext: BrowserContext;
let admin: Page;
let adminSecret = '';
let viewerContext: BrowserContext;
let viewer: Page;
let serviceId = 0;
// The file the download flow saved; the setup-screen restore uploads it.
let downloadedFile = '';

async function open(browser: Browser, baseURL: string): Promise<{ context: BrowserContext; page: Page }> {
  const context = watchCsp(await browser.newContext({ baseURL }), cspViolations);
  return { context, page: await context.newPage() };
}

/** The secret key shown during enrolment, read whole from the element that groups it by four. */
async function shownSecret(page: Page): Promise<string> {
  const secret = page.getByLabel('Secret', { exact: true });
  await expect(secret).toHaveText(/^([A-Z2-7]{1,4} ?)+$/);
  return (await secret.getAttribute('data-secret')) ?? '';
}

/** Walks the enrolment screens and returns the secret that was shown. */
async function enrol(page: Page): Promise<string> {
  await expect(page).toHaveURL(/\/2fa\/setup$/);
  const secret = await shownSecret(page);
  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByLabel('6-digit code').fill(await freshCode(secret));
  await expect(page.getByRole('list', { name: 'Recovery codes' })).toBeVisible();
  await page.getByLabel('I have saved these codes').check();
  await page.getByRole('button', { name: 'Continue' }).click();
  await expect(page).toHaveURL(/\/$/);
  return secret;
}

/** Signs in with the password and an authenticator code. */
async function signIn(page: Page, user: string, secret: string): Promise<void> {
  await page.goto('/login');
  await page.getByLabel('Username').fill(user);
  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: 'Enter your code' })).toBeVisible();
  await page.getByLabel('6-digit code').fill(await freshCode(secret));
  await expect(page).toHaveURL(/\/$/);
  await expect(account(page).getByText(user)).toBeVisible();
}

/** The first name in the tar stream inside a gzip file. */
function firstTarEntry(file: string): string {
  const tar = gunzipSync(readFileSync(file));
  // A tar header starts with the file name, zero-padded to 100 bytes.
  return tar.subarray(0, 100).toString('utf8').replace(/\0.*$/s, '');
}

/** The rows of the backup list, without the header. */
function backupRows(page: Page) {
  return page.getByRole('table', { name: 'Backups' }).getByRole('row').filter({
    has: page.getByRole('button', { name: /^Actions for the backup from/ }),
  });
}

/** Opens the actions menu of exactly this row and chooses an item. */
async function rowMenu(page: Page, row: Locator, item: string) {
  await row.getByRole('button', { name: /^Actions for the backup from/ }).click();
  await page.getByRole('menuitem', { name: item }).click();
}

async function backUpNow(page: Page, expectedRows: number): Promise<void> {
  await page.getByRole('button', { name: 'Back up now' }).first().click();
  await expect(backupRows(page)).toHaveCount(expectedRows);
  // The button is available again once the backup has ended.
  await expect(page.getByRole('button', { name: 'Back up now' })).toBeEnabled();
}

test.beforeAll(async () => {
  // A cold image build takes several minutes.
  test.setTimeout(600_000);
  // Remove anything a killed earlier run left behind, then start clean.
  await compose(['down', '-v', '--remove-orphans'], backupStack);
  try {
    await compose(['up', '-d', '--build', '--wait'], backupStack, 540_000);
  } catch (error) {
    throw new Error(
      `the Compose project ${backupStack.project} did not become ready in 9 minutes: ${String(error)}`,
    );
  }
});

test.afterAll(async () => {
  await adminContext?.close();
  await viewerContext?.close();
  if (process.env.E2E_KEEP === '1') return;
  test.setTimeout(120_000);
  await compose(['down', '-v', '--remove-orphans'], backupStack);
});

// No page may break the Content Security Policy (inline styles or scripts).
test.afterAll(() => {
  expect(cspViolations).toEqual([]);
});

test('1. an installation with an administrator, a service and a viewer', async ({ browser }) => {
  const token = await readSetupToken(backupStack);
  ({ context: adminContext, page: admin } = await open(browser, mainURL));

  await admin.goto('/');
  await expect(admin).toHaveURL(/\/setup$/);
  await admin.getByRole('button', { name: /Set up a new installation/ }).click();
  await admin.getByLabel('Setup token').fill(token);
  await admin.getByLabel('Username').fill('root');
  await admin.getByLabel('Password', { exact: true }).fill(password);
  await admin.getByLabel('Confirm password').fill(password);
  await admin.getByRole('button', { name: 'Create administrator' }).click();
  adminSecret = await enrol(admin);

  // GitLab sign-in against the fake provider gives a viewer; a service gives
  // something to rename and to find again after a restore.
  const settings = await adminContext.request.patch('/api/v1/settings', {
    data: {
      'gitlab.url': 'http://fake-gitlab:19001',
      'gitlab.client_id': 'e2e-client',
      'gitlab.client_secret': 'e2e-client-secret',
    },
  });
  expect(settings.ok(), await settings.text()).toBe(true);
  const created = await adminContext.request.post('/api/v1/services', {
    data: { gitlab_project_path: `platform/${serviceName}`, name: serviceName },
  });
  expect(created.status(), await created.text()).toBe(201);
  serviceId = ((await created.json()) as { id: number }).id;

  ({ context: viewerContext, page: viewer } = await open(browser, mainURL));
  await viewer.goto('/login');
  await viewer.getByRole('link', { name: 'Sign in with GitLab' }).click();
  await expect(viewer.getByRole('heading', { name: 'Fake GitLab sign-in' })).toBeVisible();
  await viewer.getByRole('link', { name: 'alice', exact: true }).click();
  await enrol(viewer);
  await expect(account(viewer).getByText('Viewer')).toBeVisible();
});

test('2. a viewer has no Backups item and is refused the page and the API', async () => {
  await expect(
    viewer.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Portfolio' }),
  ).toBeVisible();
  await expect(
    viewer.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Backups' }),
  ).toHaveCount(0);

  await viewer.goto('/admin/backups');
  await expect(viewer.getByText('You need the admin role to see this page.')).toBeVisible();
  await expect(viewer.getByRole('button', { name: 'Back up now' })).toHaveCount(0);

  const status = await viewer.evaluate(async () => (await fetch('/api/v1/admin/backups')).status);
  expect(status).toBe(403);
});

test('3. Back up now shows progress and then lists the backup', async () => {
  await admin.goto('/admin/backups');
  await expect(admin.getByRole('heading', { name: 'Backups', level: 1 })).toBeVisible();
  await expect(admin.getByText('No backups yet')).toBeVisible();
  await expect(admin.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Backups' })).toBeVisible();

  await admin.getByRole('button', { name: 'Back up now' }).first().click();
  // Right after the click, before slower checks; the list is the real result.
  await expect(admin.getByText('Backup started', { exact: true })).toBeVisible();
  // A small installation finishes in an instant, so the progress band may be
  // gone before it is seen; the result is what counts.
  await expect(backupRows(admin)).toHaveCount(1);
  const row = backupRows(admin).first();
  await expect(row).toContainText('Manual');
  await expect(admin.getByText('1 backup,')).toBeVisible();
  await expect(admin.getByRole('region', { name: 'Running backup' })).toHaveCount(0);

  await admin.reload();
  await expect(backupRows(admin)).toHaveCount(1);
});

test('4. a downloaded backup is a gzip tar that starts with manifest.json', async ({}, testInfo) => {
  const row = backupRows(admin).first();
  const downloading = admin.waitForEvent('download');
  await rowMenu(admin, row, 'Download');
  const download = await downloading;
  expect(download.suggestedFilename()).toMatch(
    /^scanopsis-backup-\d{8}T\d{6}Z-manual-[a-z0-9]{6}\.tar\.gz$/,
  );
  downloadedFile = testInfo.outputPath(download.suggestedFilename());
  await download.saveAs(downloadedFile);
  expect(firstTarEntry(downloadedFile)).toBe('manifest.json');
});

test('5. the schedule is saved, shown again after a reload, and kept out of Settings', async () => {
  await admin.goto('/admin/backups');
  const schedule = admin.getByRole('region', { name: 'Scheduled backups' });
  const enabled = schedule.getByRole('switch', { name: 'Scheduled backups' });
  await expect(enabled).not.toBeChecked();
  await enabled.click();
  await expect(enabled).toBeChecked();

  // The default time is replaced by two others.
  await schedule.getByRole('button', { name: 'Remove 02:00' }).click();
  for (const time of ['03:30', '14:45']) {
    await schedule.getByLabel('Add a time').fill(time);
    await schedule.getByRole('button', { name: 'Add time' }).click();
  }
  await schedule.getByLabel('Keep scheduled backups for').fill('3');
  await schedule.getByRole('button', { name: 'Save schedule' }).click();
  // Saved: the form is clean again, so the button is off.
  await expect(schedule.getByRole('button', { name: 'Save schedule' })).toBeDisabled();

  await admin.reload();
  const times = schedule.getByRole('list', { name: 'Backup times' }).getByRole('listitem');
  await expect(times).toHaveCount(2);
  await expect(times.nth(0)).toContainText('03:30');
  await expect(times.nth(1)).toContainText('14:45');
  await expect(schedule.getByRole('switch', { name: 'Scheduled backups' })).toBeChecked();
  await expect(schedule.getByLabel('Keep scheduled backups for')).toHaveValue('3');

  // The settings page is for the other settings only; these have their own form.
  await admin.goto('/settings');
  await expect(admin.getByRole('heading', { name: 'Settings', level: 1 })).toBeVisible();
  const main = admin.getByRole('main');
  await expect(main).toContainText('Idle timeout');
  await expect(main).not.toContainText(/backup\.|schedule_times|keep_days|Scheduled backups/i);
});

test('6. deleting a backup asks first and then removes exactly that row', async () => {
  const names = async () =>
    (
      (await (await adminContext.request.get('/api/v1/admin/backups')).json()) as {
        backups: { name: string }[];
      }
    ).backups.map((b) => b.name);
  const [kept] = await names();
  expect(kept, 'the backup of flow 3').toBeTruthy();

  await admin.goto('/admin/backups');
  await backUpNow(admin, 2);
  const added = (await names()).filter((n) => n !== kept);
  expect(added).toHaveLength(1);
  const doomed = added[0];

  // Two backups can share a second, so the row is found by the file name its
  // delete dialog shows. Cancel keeps both.
  const dialog = admin.getByRole('alertdialog');
  let target: Locator | undefined;
  for (let i = 0; i < 2 && !target; i++) {
    const row = backupRows(admin).nth(i);
    await rowMenu(admin, row, 'Delete');
    await expect(dialog).toContainText('cannot be restored afterwards');
    if ((await dialog.innerText()).includes(doomed)) target = row;
    else {
      await dialog.getByRole('button', { name: 'Cancel' }).click();
      await expect(dialog).toHaveCount(0);
    }
  }
  expect(target, `a row whose dialog names ${doomed}`).toBeDefined();
  await dialog.getByRole('button', { name: 'Cancel' }).click();
  await expect(dialog).toHaveCount(0);
  await expect(backupRows(admin)).toHaveCount(2);

  await rowMenu(admin, target!, 'Delete');
  await expect(dialog).toContainText(doomed);
  await dialog.getByRole('button', { name: 'Delete backup' }).click();
  await expect(backupRows(admin)).toHaveCount(1);
  await expect(dialog).toHaveCount(0);
  expect(await names()).toEqual([kept]);
});

test('7. a restore is refused without a code and with a wrong code', async () => {
  await admin.goto('/admin/backups');
  await rowMenu(admin, backupRows(admin).first(), 'Restore');
  const dialog = admin.getByRole('alertdialog');
  await expect(dialog.getByRole('heading', { name: /^Restore the backup from/ })).toBeVisible();
  const restore = dialog.getByRole('button', { name: 'Restore backup' });
  const phrase = dialog.getByLabel('Type RESTORE to confirm');
  const code = dialog.getByLabel('Two-factor code');

  // Nothing typed, the phrase alone and the code alone do not enable it.
  await expect(restore).toBeDisabled();
  await phrase.fill('RESTORE');
  await expect(restore).toBeDisabled();
  await phrase.fill('restore');
  await code.fill('123456');
  await expect(restore).toBeDisabled();

  // A wrong code is refused and the dialog stays.
  await phrase.fill('RESTORE');
  await code.fill('000000');
  await expect(restore).toBeEnabled();
  await restore.click();
  await expect(dialog.getByRole('alert')).toHaveText(
    'The two-factor code is wrong or was already used. Enter a new code.',
  );
  await expect(dialog).toBeVisible();
  await expect(code).toHaveValue('');
  await expect(admin).toHaveURL(/\/admin\/backups$/);

  await dialog.getByRole('button', { name: 'Cancel' }).click();
  await expect(dialog).toHaveCount(0);
  // Nothing was restored: the session still works.
  await expect(account(admin).getByText('root')).toBeVisible();
});

test('8. an administrator restores a backup and the old data is back', async () => {
  // Change something after the backup: the restore must undo it.
  const renamed = await adminContext.request.patch(`/api/v1/services/${serviceId}`, {
    data: { name: renamedService },
  });
  expect(renamed.ok(), await renamed.text()).toBe(true);
  await admin.goto('/admin/services');
  await expect(admin.getByRole('main')).toContainText(renamedService);

  await admin.goto('/admin/backups');
  await rowMenu(admin, backupRows(admin).first(), 'Restore');
  const dialog = admin.getByRole('alertdialog');
  await dialog.getByLabel('Type RESTORE to confirm').fill('RESTORE');
  await dialog.getByLabel('Two-factor code').fill(await freshCode(adminSecret));
  await dialog.getByRole('button', { name: 'Restore backup' }).click();

  const overlay = admin.getByRole('alertdialog', { name: 'Restore finished' });
  await expect(overlay).toBeVisible({ timeout: 60_000 });
  await expect(overlay).toContainText('Sign in with an account from the backup.');
  await overlay.getByRole('button', { name: 'Go to sign-in' }).click();
  await expect(admin).toHaveURL(/\/login/);

  await signIn(admin, 'root', adminSecret);
  await admin.goto('/admin/services');
  const main = admin.getByRole('main');
  await expect(main).toContainText(serviceName);
  await expect(main).not.toContainText(renamedService);

  await admin.goto('/admin/audit');
  await expect(admin.getByRole('main')).toContainText('Backup restored');

  // The safety backup made before the restore is in the list.
  await admin.goto('/admin/backups');
  await expect(backupRows(admin).filter({ hasText: 'Before restore' })).toHaveCount(1);
});

test('9. a backup is restored on the setup screen of an empty installation', async ({ browser }, testInfo) => {
  expect(downloadedFile, 'the download flow ran').not.toBe('');
  const { context, page } = await open(browser, emptyURL);
  try {
    await page.goto('/');
    await expect(page).toHaveURL(/\/setup$/);
    await expect(page.getByRole('heading', { name: 'Welcome to Scanopsis' })).toBeVisible();
    await page.getByRole('button', { name: /Restore from a backup/ }).click();
    await expect(page.getByRole('heading', { name: 'Restore from a backup' })).toBeVisible();

    // A wrong token stays on the token step.
    await page.getByLabel('Setup token').fill('not-the-token');
    await page.getByRole('button', { name: 'Continue' }).click();
    await expect(page.getByText('The setup token is not correct.')).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Restore from a backup' })).toBeVisible();
    await expect(page.getByLabel('Backup file')).toHaveCount(0);

    await page.getByLabel('Setup token').fill(await readSetupToken(backupStack, emptyService));
    await page.getByRole('button', { name: 'Continue' }).click();
    await expect(page.getByRole('heading', { name: 'Choose the backup file' })).toBeVisible();

    await page.getByLabel('Backup file').setInputFiles(downloadedFile);
    await expect(page.getByRole('heading', { name: 'Review the backup' })).toBeVisible({
      timeout: 60_000,
    });
    await expect(page.getByText('The same secret key as this installation')).toBeVisible();
    const restore = page.getByRole('button', { name: 'Restore backup' });
    await expect(restore).toBeDisabled();
    await page.getByLabel('Type RESTORE to confirm').fill('RESTORE');
    await restore.click();

    const overlay = page.getByRole('alertdialog', { name: 'Restore finished' });
    await expect(overlay).toBeVisible({ timeout: 60_000 });
    await overlay.getByRole('button', { name: 'Go to sign-in' }).click();
    await expect(page).toHaveURL(/\/login/);

    // The administrator and the two-factor secret come from the backup.
    await signIn(page, 'root', adminSecret);
    await page.goto('/admin/services');
    await expect(page.getByRole('main')).toContainText(serviceName);
    await page.goto('/admin/audit');
    await expect(page.getByRole('main')).toContainText('Backup restored');
  } finally {
    await testInfo.attach('empty-installation', {
      body: await page.screenshot(),
      contentType: 'image/png',
    });
    await context.close();
  }
});
