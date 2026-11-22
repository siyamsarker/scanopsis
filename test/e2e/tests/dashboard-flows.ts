// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

// The dashboard flows: real Trivy reports go in through the upload endpoint
// with a signed CI ID token, and every screen must show exactly their numbers.
// They run after the sign-in flows, in the same serial order, and share the
// administrator session and the database those flows left behind.

import { expect, test, type BrowserContext, type Locator, type Page } from '@playwright/test';
import {
  countBySeverity,
  findingKey,
  gitlabUrl,
  imageFull,
  imageSuppressed,
  secretFs,
  sum,
  summary,
  upload,
  type Counts,
  type Finding,
  type Job,
  type Report,
  type Uploaded,
} from '../fixtures/trivy';

export interface Session {
  root: () => Page;
  rootContext: () => BrowserContext;
  /** A signed-in GitLab user; flow 19 turns that account into a viewer. */
  viewer?: () => Page;
}

const payments = { projectId: 42, projectPath: 'platform/payments-api' };
const billing = { projectId: 43, projectPath: 'platform/billing-worker' };
const jobs = {
  suppressed: { ...payments, pipelineId: 1001, jobId: 5001 },
  full: { ...payments, pipelineId: 1002, jobId: 5002 },
  secrets: { ...payments, pipelineId: 1002, jobId: 5003 },
  billing: { ...billing, pipelineId: 2001, jobId: 6001 },
} satisfies Record<string, Job>;

// The current state of an artifact is its scan with the latest report time,
// not the one uploaded last. image-suppressed.json was written after
// image-full.json, so it is the current image scan of payments-api although
// it was uploaded first.
const [current, previous]: [Report, Report] =
  imageSuppressed.createdAt > imageFull.createdAt
    ? [imageSuppressed, imageFull]
    : [imageFull, imageSuppressed];
const currentJob = current === imageSuppressed ? jobs.suppressed : jobs.full;
const previousJob = current === imageSuppressed ? jobs.full : jobs.suppressed;

// The numbers the fixtures' README documents; the computed values must agree.
const documented = {
  full: { counts: { critical: 2, high: 44, medium: 58, low: 31, unknown: 3 }, fixable: 66, suppressed: 0 },
  suppressed: { counts: { critical: 1, high: 43, medium: 57, low: 31, unknown: 3 }, fixable: 64, suppressed: 3 },
} satisfies Record<string, { counts: Counts; fixable: number; suppressed: number }>;

// Scan IDs from the upload responses, by fixture name.
const scanIds: Record<string, number> = {};

// Split so that secret scanners do not flag these fake values.
const fakeAwsKey = ['AKIA', 'Z7Q3N5XK2WPLM4RT'].join('');
const fakeAwsSecret = ['wJalrXUtnFEMI/K7MDENG/', 'bPxRfiCYzq4mNp8LdT'].join('');

const keys = (findings: Finding[]) => new Set(findings.map(findingKey));
const currentKeys = keys(current.findings);
const previousKeys = keys(previous.findings);
const newKeys = [...currentKeys].filter((k) => !previousKeys.has(k));
const fixedKeys = [...previousKeys].filter((k) => !currentKeys.has(k));
const unchangedKeys = [...currentKeys].filter((k) => previousKeys.has(k));

const fixableTotal = current.fixable + imageFull.fixable;
const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;

/** The text of a locator with all white space collapsed, however it is split across elements. */
const flat = async (locator: Locator) => ((await locator.innerText()) ?? '').replace(/\s+/g, ' ');

function main(page: Page) {
  return page.getByRole('main');
}

async function expectUploaded(
  response: Awaited<ReturnType<typeof upload>>,
  report: Report,
  expected: { counts: Counts; fixable: number; secrets: number; suppressed: number },
): Promise<Uploaded> {
  expect(response.status(), await response.text()).toBe(201);
  const body = (await response.json()) as Uploaded;
  expect(body.counts).toEqual(expected.counts);
  expect(body.fixable).toBe(expected.fixable);
  expect(body.secrets).toBe(expected.secrets);
  expect(body.suppressed).toBe(expected.suppressed);
  // The same numbers, counted independently over the report's JSON.
  expect(countBySeverity(report.findings)).toEqual(expected.counts);
  return body;
}

export function registerDashboardFlows(session: Session): void {
  const root = () => session.root();

  test('12. the fixtures hold the documented numbers', () => {
    expect(imageFull.counts).toEqual(documented.full.counts);
    expect(imageFull.fixable).toBe(documented.full.fixable);
    expect(imageSuppressed.counts).toEqual(documented.suppressed.counts);
    expect(imageSuppressed.fixable).toBe(documented.suppressed.fixable);
    expect(secretFs.secretRules).toHaveLength(2);
    // The scans differ in exactly the three suppressed findings.
    expect(newKeys.length).toBe(0);
    expect(fixedKeys.length).toBe(3);
    expect(unchangedKeys.length).toBe(135);
  });

  test('13. upload: signed CI tokens store the reports with the exact numbers', async ({
    request,
  }) => {
    // The GitLab URL is what the server verifies tokens against.
    const page = root();
    await page.goto('/settings');
    const url = page.getByLabel('Base URL of the GitLab instance');
    await expect(page.getByRole('heading', { name: 'Settings' })).toBeVisible();
    if ((await url.inputValue()) !== gitlabUrl) {
      await url.fill(gitlabUrl);
      await page.getByRole('button', { name: 'Save changes' }).click();
      await expect(page.getByText('Settings saved. Changes apply now.')).toBeVisible();
    }

    const first = await expectUploaded(
      await upload(request, imageSuppressed, jobs.suppressed),
      imageSuppressed,
      { ...documented.suppressed, secrets: 0 },
    );
    scanIds[imageSuppressed.name] = first.scan_id;
    expect(first.service_created).toBe(true);
    expect(first.replaced).toBe(false);

    const second = await expectUploaded(
      await upload(request, imageFull, jobs.full),
      imageFull,
      { ...documented.full, secrets: 0 },
    );
    scanIds[imageFull.name] = second.scan_id;
    expect(second.service_created).toBe(false);
    expect(second.service_id).toBe(first.service_id);

    await expectUploaded(
      await upload(request, secretFs, jobs.secrets, 'secret-fs'),
      secretFs,
      { counts: countBySeverity([]), fixable: 0, secrets: 2, suppressed: 0 },
    );

    const other = await expectUploaded(
      await upload(request, imageFull, jobs.billing),
      imageFull,
      { ...documented.full, secrets: 0 },
    );
    expect(other.service_created).toBe(true);
    expect(other.service_id).not.toBe(first.service_id);
  });

  test('14. portfolio: summary, tiles, sorting, treemap and URL filters', async () => {
    const page = root();
    await page.goto('/');
    await expect(page.getByRole('heading', { name: 'Portfolio' })).toBeVisible();

    const summaryTiles = page.getByRole('region', { name: 'Summary' });
    await expect(summaryTiles).toContainText(/Services\s*2(?!\d)/);
    await expect(summaryTiles).toContainText(/Failing quality gates\s*0/);
    await expect(summaryTiles).toContainText(/Services with critical vulnerabilities\s*2/);
    await expect(summaryTiles).toContainText(new RegExp(`Fixable vulnerabilities\\s*${fixableTotal}`));
    await expect(page.getByText('Showing 2 of 2 services')).toBeVisible();

    // Each tile shows the current state of its service.
    const payTile = page.getByRole('listitem').filter({ hasText: 'platform/payments-api' });
    await expect(payTile).toContainText(summary(current.counts));
    await expect(payTile).toContainText(`${current.fixable} fixable`);
    await expect(payTile).toContainText('No SonarQube analysis yet');
    const billTile = page.getByRole('listitem').filter({ hasText: 'platform/billing-worker' });
    await expect(billTile).toContainText(summary(imageFull.counts));
    await expect(billTile).toContainText(`${imageFull.fixable} fixable`);

    // Sorting by name is alphabetical; by freshness the service whose newest
    // report is newest comes first (the secret scan of payments-api).
    const names = page.getByRole('heading', { level: 3 });
    await page.getByLabel('Sort by', { exact: true }).selectOption('name');
    await expect(page).toHaveURL(/sort=name/);
    await expect(names).toHaveText(['billing-worker', 'payments-api']);
    await page.getByLabel('Sort by', { exact: true }).selectOption('freshness');
    await expect(page).toHaveURL(/sort=freshness/);
    const newestReport = (r: Report) => r.createdAt;
    const paymentsFirst = Math.max(newestReport(current), newestReport(secretFs)) > newestReport(imageFull);
    await expect(names).toHaveText(paymentsFirst ? ['payments-api', 'billing-worker'] : ['billing-worker', 'payments-api']);

    // The treemap is a canvas; the grid lists the same tiles.
    await page.getByRole('radio', { name: 'Treemap' }).click();
    await expect(page).toHaveURL(/view=treemap/);
    const chart = page.getByRole('img', { name: /^Open vulnerabilities per service/ });
    await expect(chart.locator('canvas')).toBeVisible();
    await expect(page.getByText('Treemap tiles are also listed in the grid view.')).toBeVisible();
    const chartTable = page.getByRole('table', { name: /^Open vulnerabilities per service/ });
    await expect(chartTable).toContainText(new RegExp(`billing-worker\\s*Critical\\s*${sum(imageFull.counts)}`));
    await expect(chartTable).toContainText(new RegExp(`payments-api\\s*Critical\\s*${sum(current.counts)}`));

    // Filters live in the URL.
    await page.goto('/?filter=critical');
    await expect(page.getByText('Showing 2 of 2 services')).toBeVisible();
    await expect(page.getByRole('button', { name: /^Has critical\s*2$/ })).toHaveAttribute('aria-pressed', 'true');
    await page.goto('/?filter=gate-failed');
    await expect(page.getByText('No services match')).toBeVisible();
    await page.goto('/?filter=fixable&q=payments');
    await expect(page.getByText('Showing 1 of 2 services')).toBeVisible();
    await expect(page.getByRole('heading', { level: 3 })).toHaveText(['payments-api']);
  });

  test('15. service dashboard shows the current state of payments-api', async () => {
    const page = root();
    await page.goto('/');
    await page.getByRole('link', { name: 'payments-api' }).click();
    await expect(page).toHaveURL(/\/services\/\d+$/);
    await expect(page.getByRole('heading', { name: 'payments-api', level: 1 })).toBeVisible();
    const m = main(page);

    // Counts are those of the scan with the later report time.
    await expect(m).toContainText('Current state of main, latest scan per artifact');
    await expect(m).toContainText(summary(current.counts));
    const tile = (href: string) => m.locator(`a[href*="${href}"]`).first();
    await expect(tile('severity=CRITICAL')).toHaveText(new RegExp(`Critical\\s*${current.counts.critical}$`));
    await expect(tile('severity=HIGH')).toHaveText(new RegExp(`High\\s*${current.counts.high}$`));
    await expect(tile('fixable=true')).toContainText(new RegExp(`Fixable\\s*${current.fixable}`));
    await expect(tile('tab=secrets')).toContainText(new RegExp(`Secrets\\s*${secretFs.secretRules.length}`));
    await expect(m).toContainText(new RegExp(`New since previous scan\\s*${newKeys.length}\\s*Findings not in the previous scan`));
    await expect(m).toContainText(new RegExp(`Fixed since previous scan\\s*${fixedKeys.length}\\s*Findings gone since the previous scan`));

    // The package that clears the most findings comes first and is in the fixture.
    const perPackage = new Map<string, number>();
    for (const f of current.findings) {
      const key = `${f.pkg}@${f.installed}`;
      perPackage.set(key, (perPackage.get(key) ?? 0) + 1);
    }
    const fixFirst = m.locator('ol > li');
    await expect(fixFirst).toHaveCount(Math.min(10, perPackage.size));
    const top = await flat(fixFirst.first());
    const topName = /^\d+\s+(\S+)\s+(\S+)/.exec(top);
    expect(topName, top).not.toBeNull();
    const [, pkg, installed] = topName ?? [];
    const topFindings = perPackage.get(`${pkg}@${installed}`);
    expect(topFindings, `${pkg}@${installed} is in ${current.name}`).toBeDefined();
    expect(top).toContain(plural(topFindings ?? 0, 'finding'));
    expect(topFindings).toBe(Math.max(...perPackage.values()));

    // Artifacts: the image and the source tree of the secret scan.
    await expect(m).toContainText('Source tree');
    await expect(m).toContainText('Secret scan');
    await expect(m).toContainText(current.artifact);
    await expect(m).toContainText(plural(sum(current.counts), 'vulnerability', 'vulnerabilities'));
    await expect(m).toContainText('2 secrets');

    // No SonarQube project is linked in this stack.
    await expect(m).toContainText('SonarQube posture');
    await expect(m).toContainText('No SonarQube project linked');

    // The trend chart: a canvas, with one point per image scan in its table.
    await expect(m.getByText('How did the findings change scan by scan?')).toBeVisible();
    const trend = page.getByRole('img', { name: /^Findings per scan/ });
    await expect(trend.locator('canvas')).toBeVisible();
    const points = page.getByRole('table', { name: /^Findings per scan/ }).getByRole('row');
    await expect(points).toHaveCount(3); // header and the two image scans
  });

  test('16. findings explorer: counts, filters, search, secrets and fixes', async () => {
    const page = root();
    await page.goto('/');
    await page.getByRole('link', { name: 'payments-api' }).click();
    await page.getByRole('link', { name: 'Explore findings' }).click();
    await expect(page).toHaveURL(/\/services\/\d+\/findings/);
    const m = main(page);

    await expect(m.getByText(`${sum(current.counts)} vulnerabilities, ${current.fixable} fixable`)).toBeVisible();
    for (const [name, n] of Object.entries(current.counts)) {
      const label = name[0].toUpperCase() + name.slice(1);
      await expect(m.getByRole('button', { name: new RegExp(`^${label}\\s*${n}$`) })).toBeVisible();
    }

    // The Critical chip narrows the table to the critical findings.
    const critical = current.findings.filter((f) => f.severity === 'critical');
    await m.getByRole('button', { name: /^Critical\s*\d+$/ }).click();
    await expect(m.getByText(`${plural(critical.length, 'vulnerability', 'vulnerabilities')}, `)).toBeVisible();
    const rows = m.getByRole('table').getByRole('row');
    await expect(rows).toHaveCount(critical.length + 1);
    for (const f of critical) await expect(m.getByRole('table')).toContainText(f.vulnId);
    await m.getByRole('button', { name: /^Critical\s*\d+$/ }).click();
    await expect(m.getByText(`${sum(current.counts)} vulnerabilities`)).toBeVisible();

    // A search for a package narrows the table to that package.
    const needle = 'libc6';
    const hits = current.findings.filter((f) => f.pkg.toLowerCase().includes(needle));
    expect(hits.length).toBeGreaterThan(0);
    await m.getByLabel('Search package, vulnerability ID or title').fill(needle);
    await expect(m.getByText(`${plural(hits.length, 'vulnerability', 'vulnerabilities')}, `)).toBeVisible();
    await expect(rows).toHaveCount(hits.length + 1);
    await expect(m.getByRole('table')).toContainText(needle);
    await expect(m.getByRole('table')).not.toContainText('zlib1g');

    // Advisory links open http(s) pages in a new tab and never hand over the opener.
    const advisories = m.getByRole('table').locator('a[target="_blank"]');
    await expect(advisories).toHaveCount(hits.length);
    for (const link of await advisories.all()) {
      await expect(link).toHaveAttribute('href', /^https?:\/\//);
      await expect(link).toHaveAttribute('rel', /noopener/);
    }
    await m.getByLabel('Search package, vulnerability ID or title').fill('');

    // Secrets: rule and place, never the secret.
    await page.getByRole('tab', { name: /^Secrets/ }).click();
    const secrets = m.getByRole('table', { name: 'Secret findings' });
    await expect(secrets.getByRole('row')).toHaveCount(secretFs.secretRules.length + 1);
    for (const rule of secretFs.secretRules) await expect(secrets).toContainText(rule);
    await expect(secrets).toContainText('config.env');
    await expect(secrets).toContainText('Source tree');
    const html = await page.content();
    for (const value of [fakeAwsKey, fakeAwsSecret, 'wJalrXUtnFEMI']) {
      expect(html).not.toContain(value);
      expect(await page.locator('body').innerText()).not.toContain(value);
    }

    // The upgrade plan.
    await page.getByRole('tab', { name: 'Fix by upgrading' }).click();
    await expect(
      m.getByText(new RegExp(`^Upgrading \\d+ packages? clears \\d+ of ${current.fixable} fixable vulnerabilities$`)),
    ).toBeVisible();
    await expect(m.getByRole('table', { name: 'Packages to upgrade' }).getByRole('row').nth(1)).toContainText(/\d+ findings?/);
  });

  test('17. scan detail: links to GitLab and the difference to the previous scan', async () => {
    const page = root();
    await page.goto('/');
    await page.getByRole('link', { name: 'payments-api' }).click();
    // The latest scan of the image artifact, as the dashboard lists it.
    await page.getByRole('link', { name: new RegExp(`^${current.artifact.replace('.', '\\.')}`) }).click();
    await expect(page).toHaveURL(/\/scans\/\d+$/);
    const m = main(page);

    await expect(m.getByRole('heading', { name: current.artifact })).toBeVisible();
    const project = `${gitlabUrl}/${currentJob.projectPath}`;
    await expect(m.getByRole('link', { name: `Pipeline #${currentJob.pipelineId}` })).toHaveAttribute('href', `${project}/-/pipelines/${currentJob.pipelineId}`);
    await expect(m.getByRole('link', { name: `Job #${currentJob.jobId}` })).toHaveAttribute('href', `${project}/-/jobs/${currentJob.jobId}`);
    await expect(m.getByRole('link', { name: '01234567' })).toHaveAttribute('href', `${project}/-/commit/0123456789abcdef0123456789abcdef01234567`);
    await expect(m).toContainText('Trivy version');

    const c = current.counts;
    await expect(m).toContainText(
      new RegExp(`Critical\\s*${c.critical}\\s*High\\s*${c.high}\\s*Medium\\s*${c.medium}\\s*Low\\s*${c.low}\\s*Unknown\\s*${c.unknown}`),
    );
    await expect(m).toContainText(new RegExp(`Fixable\\s*${current.fixable}\\s*with a fixed version`));
    await expect(m).toContainText(new RegExp(`Suppressed\\s*${current === imageSuppressed ? documented.suppressed.suppressed : 0}`));
    await expect(m).toContainText(new RegExp(`New\\s*${newKeys.length}\\s*not in the previous scan`));
    await expect(m).toContainText(new RegExp(`Fixed\\s*${fixedKeys.length}\\s*no longer reported`));
    await expect(m).toContainText(new RegExp(`Unchanged\\s*${unchangedKeys.length}\\s*${unchangedKeys.length} findings in both`));
    if (newKeys.length === 0) await expect(m).toContainText('Nothing new since the previous scan');

    // The earlier scan knows it is no longer the current state, and is a first scan.
    await page.goto(`/scans/${scanIds[previous.name]}`);
    await expect(main(page)).toContainText('A newer scan of this artifact exists.');
    await expect(main(page)).toContainText(`First scan of this artifact on this ref: all ${previous.findings.length} findings are new.`);
    await expect(main(page).getByRole('link', { name: `Pipeline #${previousJob.pipelineId}` })).toHaveAttribute(
      'href',
      `${project}/-/pipelines/${previousJob.pipelineId}`,
    );
  });

  test('18. vulnerability search lists the affected services', async () => {
    const page = root();
    const inBoth = current.findings.find(
      (f) => f.severity === 'critical' && imageFull.findings.some((g) => g.vulnId === f.vulnId),
    );
    expect(inBoth, 'a critical finding present in both services').toBeDefined();
    const id = inBoth?.vulnId ?? '';

    await page.goto('/search');
    await page.getByLabel('Vulnerability ID').fill(id);
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL(new RegExp(`id=${id}`));
    const m = main(page);
    await expect(m).toContainText(`${id} affects 2 services in 2 artifacts`);
    await expect(m.getByRole('region', { name: 'payments-api' })).toContainText(inBoth?.pkg ?? '');
    await expect(m.getByRole('region', { name: 'billing-worker' })).toContainText(inBoth?.pkg ?? '');

    // A finding that payments-api suppressed is not in its current state.
    const suppressedHere = imageFull.findings.find(
      (f) => f.severity === 'critical' && !keys(current.findings).has(findingKey(f)),
    );
    if (suppressedHere) {
      await page.goto(`/search?id=${suppressedHere.vulnId}`);
      await expect(m).toContainText(`${suppressedHere.vulnId} affects 1 service in 1 artifact`);
      await expect(m.getByRole('region', { name: 'billing-worker' })).toBeVisible();
      await expect(m.getByRole('region', { name: 'payments-api' })).toHaveCount(0);
    }

    await page.getByLabel('Vulnerability ID').fill('CVE-2099-0001');
    await page.keyboard.press('Enter');
    await expect(m.getByText('No service is affected in its current scans.')).toBeVisible();
    await expect(m).toContainText('CVE-2099-0001 does not appear in the latest scan of any service, searching dashboard branches and protected refs.');
  });

  test('18a. the search covers dashboard branches and protected refs unless all branches are asked for', async ({ request }) => {
    const page = root();
    const m = main(page);
    const scope = page.getByRole('radiogroup', { name: 'Search scope' });
    const demo = { projectId: 44, projectPath: 'platform/scope-demo' };

    // The service scans an unprotected branch with an older report than main.
    // main is protected and is the ref its dashboards show; the finding that
    // only the older report has is therefore found by "All branches" alone.
    const onlyOld = previous.findings.find((f) => !currentKeys.has(findingKey(f)));
    expect(onlyOld, 'a finding only the older report has').toBeDefined();
    const id = onlyOld?.vulnId ?? '';
    const onMain = await upload(request, current, { ...demo, pipelineId: 3001, jobId: 7001, ref: 'main' });
    expect(onMain.status()).toBe(201);
    const onBranch = await upload(request, previous, { ...demo, pipelineId: 3002, jobId: 7002, ref: 'feature/old', refProtected: false });
    expect(onBranch.status()).toBe(201);

    await page.goto(`/search?id=${id}`);
    await expect(scope.getByRole('radio', { name: 'Dashboard branches and protected refs' })).toBeChecked();
    await expect(m).toContainText('Searched: dashboard branches and protected refs');
    await expect(m.getByRole('region', { name: 'scope-demo' })).toHaveCount(0);

    await scope.getByRole('radio', { name: 'All branches' }).click();
    await expect(page).toHaveURL(new RegExp(`id=${id}&scope=all`));
    await expect(m).toContainText('Searched: all branches');
    const hit = m.getByRole('region', { name: 'scope-demo' });
    await expect(hit).toBeVisible();
    await expect(hit).toContainText('feature/old');

    // The scope is part of the address: a reload keeps it, and so does a new search.
    await page.reload();
    await expect(scope.getByRole('radio', { name: 'All branches' })).toBeChecked();
    await expect(m.getByRole('region', { name: 'scope-demo' })).toBeVisible();
    await page.getByLabel('Vulnerability ID').fill('CVE-2099-0001');
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL(/id=CVE-2099-0001&scope=all/);
    await expect(m).toContainText('searching all branches.');

    await scope.getByRole('radio', { name: 'Dashboard branches and protected refs' }).click();
    await expect(page).toHaveURL(/\/search\?id=CVE-2099-0001$/);
  });

  test('19. admin: integrations, rejected uploads, audit log and archiving', async ({ request }) => {
    const page = root();
    const m = main(page);

    await page.goto('/admin/integrations');
    await expect(m.getByRole('heading', { name: 'Integrations' })).toBeVisible();
    await expect(m).toContainText('Ready');
    const uploads = /Uploads, last 24 hours\s*(\d+)/.exec(await flat(m));
    expect(Number(uploads?.[1])).toBeGreaterThanOrEqual(4);
    await expect(m).toContainText(/Recorded rejections, last 24 hours\s*0/);

    // A token for another audience is refused, and the page says why.
    const wrong = await upload(request, imageFull, { ...jobs.billing, pipelineId: 2002, jobId: 6002, audience: 'https://elsewhere.example.com' });
    expect(wrong.status()).toBe(401);
    expect(((await wrong.json()) as { error: { code: string } }).error.code).toBe('invalid_token');
    await page.reload();
    await expect(m).toContainText(/Recorded rejections, last 24 hours\s*1/);
    await expect(m.getByText('invalid_token', { exact: true })).toBeVisible();

    // The audit log keeps it.
    await page.goto('/admin/audit');
    await page.getByLabel('Action', { exact: true }).selectOption('ingest.rejected');
    const audit = m.getByRole('table', { name: 'Audit log' });
    await expect(audit.getByRole('row')).toHaveCount(2);
    await expect(audit).toContainText('ingest.rejected');
    await expect(audit).toContainText('Upload rejected');
    await expect(audit).toContainText('invalid_token');

    // Archiving hides a service and refuses its uploads; unarchiving undoes both.
    await page.goto('/admin/services');
    const table = m.getByRole('table', { name: 'Active services' });
    await expect(table).toContainText('payments-api');
    await expect(table).toContainText('billing-worker');
    await page.getByRole('button', { name: 'Archive billing-worker' }).click();
    const dialog = page.getByRole('alertdialog');
    await expect(dialog.getByText('Archive billing-worker?')).toBeVisible();
    await dialog.getByRole('button', { name: 'Archive', exact: true }).click();
    await expect(page.getByText('Archived billing-worker.')).toBeVisible();
    await expect(table).not.toContainText('billing-worker');

    await page.goto('/');
    await expect(summaryTile(page, 'Services')).toContainText(/Services\s*1(?!\d)/);
    await expect(page.getByRole('heading', { level: 3 })).toHaveText(['payments-api']);

    const refused = await upload(request, imageFull, { ...jobs.billing, pipelineId: 2003, jobId: 6003 });
    expect(refused.status()).toBe(409);
    expect(((await refused.json()) as { error: { code: string } }).error.code).toBe('service_archived');

    await page.goto('/admin/services');
    await page.getByRole('radio', { name: 'Archived' }).click();
    await expect(m.getByRole('table', { name: 'Archived services' })).toContainText('billing-worker');
    await page.getByRole('button', { name: 'Unarchive billing-worker' }).click();
    await expect(page.getByText('Unarchived billing-worker.')).toBeVisible();

    await page.goto('/');
    await expect(page.getByRole('heading', { level: 3 })).toHaveText(['billing-worker', 'payments-api']);
    await expect(summaryTile(page, 'Services')).toContainText(/Services\s*2(?!\d)/);
  });

  test('20. a viewer sees the dashboard but not the admin screens', async () => {
    test.skip(!session.viewer, 'needs a signed-in GitLab user');
    const admin = root();
    const viewer = session.viewer?.() as Page;

    // The account was promoted in flow 6; make it a viewer again.
    await admin.goto('/admin/users');
    const role = admin.getByLabel('Role for alice');
    await role.selectOption('viewer');
    await expect(admin.getByText('Role changed to Viewer.')).toBeVisible();

    await viewer.goto('/');
    await expect(viewer.getByRole('banner').getByText('Viewer')).toBeVisible();
    await expect(viewer.getByRole('heading', { name: 'Portfolio' })).toBeVisible();
    await expect(summaryTile(viewer, 'Services')).toContainText(/Services\s*2(?!\d)/);
    await expect(viewer.getByRole('listitem').filter({ hasText: 'platform/payments-api' })).toContainText(summary(current.counts));

    await viewer.getByRole('link', { name: 'payments-api' }).click();
    await viewer.getByRole('link', { name: 'Explore findings' }).click();
    await expect(main(viewer).getByText(`${sum(current.counts)} vulnerabilities, ${current.fixable} fixable`)).toBeVisible();

    await viewer.goto('/admin/services');
    await expect(viewer.getByText('You need the admin role to see this page.')).toBeVisible();
    await viewer.goto('/admin/audit');
    await expect(viewer.getByText('You need the admin role to see this page.')).toBeVisible();
    const status = await viewer.evaluate(async () => (await fetch('/api/v1/admin/audit')).status);
    expect(status).toBe(403);
    const integrations = await viewer.evaluate(async () => (await fetch('/api/v1/admin/integrations/trivy')).status);
    expect(integrations).toBe(403);
  });

  test('21. an administrator chooses the branch the dashboards show', async ({ request }) => {
    const page = root();
    const m = main(page);
    // An older scan of another branch of payments-api. The latest scan is still on main.
    const feature = { ...payments, pipelineId: 1004, jobId: 5004, ref: 'feature/login' };
    await expectUploaded(await upload(request, imageFull, feature), imageFull, {
      ...documented.full,
      secrets: 0,
    });

    const tile = () => page.getByRole('listitem').filter({ hasText: 'platform/payments-api' });
    const row = () =>
      m
        .getByRole('table', { name: 'Active services' })
        .getByRole('row')
        .filter({ hasText: 'platform/payments-api' });
    async function choose(branch: string) {
      await page.goto('/admin/services');
      await row()
        .getByRole('button', { name: /^(Choose|Change) dashboard branch of payments-api$/ })
        .click();
      await row().getByLabel('Dashboard branch of payments-api', { exact: true }).fill(branch);
      await row().getByRole('button', { name: 'Save dashboard branch of payments-api' }).click();
      await expect(page.getByText(`The dashboards of payments-api now show ${branch}.`)).toBeVisible();
    }

    // Nothing is chosen yet: the latest scanned branch is shown, and the page says so.
    await page.goto('/admin/services');
    await expect(row()).toContainText('Not chosen — showing the latest scanned branch (main)');
    await row().getByRole('button', { name: 'Choose dashboard branch of payments-api' }).click();
    // The editor suggests the branches that have scans.
    await expect(row().locator('datalist option')).toHaveCount(2);
    await row().getByRole('button', { name: 'Cancel editing the dashboard branch of payments-api' }).click();
    await page.goto('/');
    await expect(tile()).toContainText('main');
    await expect(tile()).toContainText(summary(current.counts));

    // Choose the feature branch: the portfolio card and the dashboard follow it.
    await choose('feature/login');
    await expect(row()).toContainText('feature/login');
    await expect(row()).not.toContainText('Not chosen');
    await page.goto('/');
    await expect(tile()).toContainText('feature/login');
    await expect(tile()).toContainText(summary(imageFull.counts));
    await expect(tile()).toContainText(`${imageFull.fixable} fixable`);
    await tile().getByRole('link', { name: 'payments-api' }).click();
    await expect(page.getByTestId('ref-source')).toHaveText('Showing feature/login · dashboard branch');
    await expect(m).toContainText('Current state of feature/login, latest scan per artifact');
    await expect(m).toContainText(summary(imageFull.counts));
    await expect(page.getByLabel('Git ref')).toHaveValue('feature/login');
    await page.getByRole('link', { name: 'Explore findings' }).click();
    await expect(m.getByText(`${sum(imageFull.counts)} vulnerabilities, ${imageFull.fixable} fixable`)).toBeVisible();

    // A branch without scans shows the empty state for that branch, not another branch.
    await choose('release/9');
    await page.goto('/');
    await expect(tile()).toContainText('No scans of release/9 yet.');
    await tile().getByRole('link', { name: 'payments-api' }).click();
    await expect(m).toContainText('No scans of release/9 yet');
    await expect(page.getByTestId('ref-source')).toHaveText('Showing release/9 · dashboard branch');
    await expect(m).not.toContainText('Findings by severity');

    // Clearing the choice brings back the latest scanned branch.
    await page.goto('/admin/services');
    await row().getByRole('button', { name: 'Change dashboard branch of payments-api' }).click();
    await row().getByRole('button', { name: 'Clear dashboard branch of payments-api' }).click();
    await expect(page.getByText(/Cleared the dashboard branch of payments-api/)).toBeVisible();
    await expect(row()).toContainText('Not chosen — showing the latest scanned branch (main)');
    await page.goto('/');
    await expect(tile()).toContainText('main');
    await expect(tile()).toContainText(summary(current.counts));
    await tile().getByRole('link', { name: 'payments-api' }).click();
    await expect(page.getByTestId('ref-source')).toHaveText(
      'Showing main · latest scan (no dashboard branch chosen)',
    );
    await expect(m).toContainText('Current state of main, latest scan per artifact');
    await expect(m).toContainText(summary(current.counts));

    // Every change is in the audit log with the old and the new branch.
    await page.goto('/admin/audit');
    await page.getByLabel('Action', { exact: true }).selectOption('service.updated');
    const audit = m.getByRole('table', { name: 'Audit log' });
    // The details are stored as JSON, which lists the shorter key first.
    await expect(audit).toContainText('{"to":"feature/login","from":null}');
    await expect(audit).toContainText('{"to":"release/9","from":"feature/login"}');
    await expect(audit).toContainText('{"to":null,"from":"release/9"}');
  });
}

function summaryTile(page: Page, label: string): Locator {
  return page.getByRole('region', { name: 'Summary' }).getByRole('link').filter({ hasText: label }).first();
}
