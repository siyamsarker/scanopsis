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
import { account, choose as chooseOption, select } from '../fixtures/ui';

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
// The service the payments-api uploads created.
let paymentsId = 0;

// Risk points as the pages count them (OWASP Dependency-Track weights).
const WEIGHTS: Counts = { critical: 10, high: 5, medium: 3, low: 1, unknown: 5 };
const risk = (findings: Finding[]) => findings.reduce((n, f) => n + WEIGHTS[f.severity], 0);
const riskOf = (counts: Counts) =>
  (Object.keys(WEIGHTS) as (keyof Counts)[]).reduce((n, k) => n + counts[k] * WEIGHTS[k], 0);

/** Findings grouped by package and installed version, by risk points, then size and name. */
function byPackage(findings: Finding[]) {
  const groups = new Map<string, Finding[]>();
  for (const f of findings) {
    const key = `${f.pkg}\u0000${f.installed}`;
    groups.set(key, [...(groups.get(key) ?? []), f]);
  }
  return [...groups.values()]
    .map((items) => ({ pkg: items[0].pkg, items, risk: risk(items) }))
    .sort(
      (a, b) =>
        b.risk - a.risk ||
        b.items.length - a.items.length ||
        a.pkg.localeCompare(b.pkg, 'en', { numeric: true }),
    );
}

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

// Weights and the iris wording of the risk points (see riskOf above).
const weights = { critical: 10, high: 5, medium: 3, low: 1, unknown: 5 } as const;
const severities = ['critical', 'high', 'medium', 'low', 'unknown'] as const;
/** The iris' accessible explanation: "Risk 455: 1 critical × 10 = 10, …". */
const explainRisk = (counts: Counts) =>
  `Risk ${riskOf(counts)}: ${severities
    .filter((k) => counts[k] > 0)
    .map((k) => `${counts[k]} ${k} × ${weights[k]} = ${counts[k] * weights[k]}`)
    .join(', ')}.`;
const formatInt = (n: number) => n.toLocaleString('en-US');

/** The link to a service on its portfolio card. */
function serviceLink(page: Page, name: string): Locator {
  return page.getByRole('list', { name: 'Services' }).getByRole('link', { name, exact: true });
}

/** The services the portfolio counts: "Showing 2 of 2 services". */
function shownServices(page: Page): Locator {
  return page.getByText(/^Showing \d+ of \d+ services?/);
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
    const url = page.getByLabel('GitLab URL', { exact: true });
    await expect(page.getByRole('heading', { name: 'Settings' })).toBeVisible();
    if ((await url.inputValue()) !== gitlabUrl) {
      await url.fill(gitlabUrl);
      await page.getByRole('button', { name: 'Save changes' }).click();
      await expect(page.getByText('Settings saved', { exact: true })).toBeVisible();
    }

    const first = await expectUploaded(
      await upload(request, imageSuppressed, jobs.suppressed),
      imageSuppressed,
      { ...documented.suppressed, secrets: 0 },
    );
    scanIds[imageSuppressed.name] = first.scan_id;
    paymentsId = first.service_id;
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

  test('14. portfolio: summary band, cards, sorting, list and URL filters', async () => {
    const page = root();
    await page.goto('/');
    await expect(page.getByRole('heading', { name: 'Portfolio', exact: true })).toBeVisible();

    // The band sums both services' current state.
    const band = page.getByRole('region', { name: 'Summary' });
    const risk = riskOf(current.counts) + riskOf(imageFull.counts);
    await expect(band.getByTestId('portfolio-risk')).toHaveText(formatInt(risk));
    await expect(band).toContainText(
      `${formatInt(sum(current.counts) + sum(imageFull.counts))} open findings in 2 services`,
    );
    const critical = band.getByRole('button', { name: /services have critical findings/ });
    await expect(critical).toContainText(/^2/);
    await expect(band.getByRole('button', { name: /findings have a fixed version/ })).toContainText(
      new RegExp(`^${formatInt(fixableTotal)}\\b`),
    );
    await expect(band).toContainText('No SonarQube analysis yet');
    await expect(shownServices(page)).toHaveText('Showing 2 of 2 services');

    // Each card shows the current state of its service: risk, counts, branch.
    const payCard = page.getByRole('listitem').filter({ hasText: 'platform/payments-api' });
    await expect(payCard.getByRole('img', { name: `payments-api: ${explainRisk(current.counts)}` })).toBeVisible();
    await expect(payCard).toContainText(summary(current.counts));
    await expect(payCard).toContainText(`${current.fixable} fixable`);
    await expect(payCard).toContainText('main');
    await expect(payCard).toContainText('No SonarQube analysis yet');
    const billCard = page.getByRole('listitem').filter({ hasText: 'platform/billing-worker' });
    await expect(billCard.getByRole('img', { name: `billing-worker: ${explainRisk(imageFull.counts)}` })).toBeVisible();
    await expect(billCard).toContainText(summary(imageFull.counts));
    await expect(billCard).toContainText(`${imageFull.fixable} fixable`);

    // A signal is a filter: choosing it narrows the cards, again clears it.
    await critical.click();
    await expect(page).toHaveURL(/filter=critical/);
    await expect(critical).toHaveAttribute('aria-pressed', 'true');
    await critical.click();
    await expect(page).toHaveURL(/\/$/);

    // Ranked by risk; sorting by name is alphabetical; by freshness the
    // service whose newest report is newest comes first.
    const names = page.getByRole('list', { name: 'Services' }).getByRole('heading', { level: 3 });
    const byRisk = riskOf(imageFull.counts) >= riskOf(current.counts)
      ? ['billing-worker', 'payments-api']
      : ['payments-api', 'billing-worker'];
    await expect(names).toHaveText(byRisk);
    await chooseOption(page, 'Sort by', 'Sort by name');
    await expect(page).toHaveURL(/sort=name/);
    await expect(names).toHaveText(['billing-worker', 'payments-api']);
    await chooseOption(page, 'Sort by', 'Sort by freshness');
    await expect(page).toHaveURL(/sort=freshness/);
    const newestReport = (r: Report) => r.createdAt;
    const paymentsFirst = Math.max(newestReport(current), newestReport(secretFs)) > newestReport(imageFull);
    await expect(names).toHaveText(paymentsFirst ? ['payments-api', 'billing-worker'] : ['billing-worker', 'payments-api']);

    // The list ranks by risk with every count, and is remembered.
    await page.goto('/');
    await page.getByRole('radio', { name: 'List' }).click();
    await expect(page).toHaveURL(/view=list/);
    const table = page.getByRole('table', { name: /^Services ranked by risk/ });
    for (const [name, counts] of [['payments-api', current.counts], ['billing-worker', imageFull.counts]] as const) {
      const row = table.getByRole('row').filter({ hasText: name });
      await expect(row).toContainText(String(riskOf(counts)));
      for (const k of severities) await expect(row).toContainText(`${counts[k]} ${k}`);
    }
    await page.goto('/');
    await expect(page.getByRole('table', { name: /^Services ranked by risk/ })).toBeVisible();
    await page.getByRole('radio', { name: 'Cards' }).click();
    await expect(page.getByRole('list', { name: 'Services' })).toBeVisible();

    // Filters live in the URL.
    await page.goto('/?filter=critical');
    await expect(shownServices(page)).toHaveText('Showing 2 of 2 services');
    await expect(page.getByRole('button', { name: /^Has critical\s*2$/ })).toHaveAttribute('aria-pressed', 'true');
    await page.goto('/?filter=gate-failed');
    await expect(page.getByText('No services match')).toBeVisible();
    await page.goto('/?filter=fixable&q=payments');
    await expect(shownServices(page)).toHaveText('Showing 1 of 2 services');
    await expect(names).toHaveText(['payments-api']);
  });

  test('15. service dashboard shows the current state of payments-api', async () => {
    const page = root();
    await page.goto('/');
    await serviceLink(page, 'payments-api').click();
    await expect(page).toHaveURL(/\/services\/\d+$/);
    await expect(page.getByRole('heading', { name: 'payments-api', level: 1 })).toBeVisible();
    const m = main(page);

    // The verdict: risk of the scan with the later report time, and what
    // changed against the scan before it.
    await expect(m.getByRole('img', { name: `payments-api: ${explainRisk(current.counts)}` })).toBeVisible();
    expect(current.counts.critical).toBeGreaterThan(0);
    await expect(page.getByTestId('verdict')).toHaveText(
      `${current.counts.critical} critical; ${fixedKeys.length} fixed and ${newKeys.length} new since pipeline #${previousJob.pipelineId}.`,
    );

    // The pipelines: one column per image pipeline. The older report is the
    // artifact's first scan, so only the later one counts new and fixed.
    const strip = m.getByRole('group', { name: 'Pipelines on main in the last 90 days: pipelines' });
    await expect(strip.getByRole('button')).toHaveCount(2);
    await expect(page.getByTestId('strip-totals')).toHaveText(
      `2 pipelines on main in the last 90 days: ${newKeys.length} new and ${fixedKeys.length} fixed. The first scan of an artifact is its starting point and does not count as new.`,
    );

    // Open findings, shown once, each severity linked to its findings.
    const open = m.getByRole('region', { name: 'Open findings' });
    await expect(open).toContainText('Current state of main, latest scan per artifact');
    for (const k of severities) {
      if (current.counts[k] === 0) continue;
      await expect(
        open.locator(`a[href*="severity=${k.toUpperCase()}"]`),
      ).toHaveAccessibleName(new RegExp(`^${current.counts[k]} ${k},`));
    }
    await expect(open.getByRole('link', { name: new RegExp(`^${current.fixable} fixable`) })).toHaveAttribute('href', /fixable=true/);
    await expect(open.getByRole('link', { name: new RegExp(`^${secretFs.secretRules.length} secrets`) })).toHaveAttribute('href', /tab=secrets/);
    // Artifacts: the image and the source tree of the secret scan.
    await expect(open).toContainText('Source tree');
    await expect(open).toContainText('Secret scan');
    await expect(open).toContainText(current.artifact);
    await expect(open).toContainText(plural(sum(current.counts), 'vulnerability', 'vulnerabilities'));
    await expect(open).toContainText('2 secrets');

    // Fix first: the upgrade that clears the most risk points comes first,
    // and its points are those of its findings that have a fixed version.
    const cleared = new Map<string, number>();
    for (const f of current.findings) {
      if (!f.fixed) continue;
      const key = `${f.pkg}@${f.installed}`;
      cleared.set(key, (cleared.get(key) ?? 0) + weights[f.severity]);
    }
    const fixFirst = m.getByRole('list', { name: 'Packages to upgrade' }).getByRole('listitem');
    await expect(fixFirst).toHaveCount(10);
    const top = await flat(fixFirst.first());
    const topName = /^\d+\s*(\S+?)\s*(\S+)\s*→/.exec(top);
    expect(topName, top).not.toBeNull();
    const [, pkg, installed] = topName ?? [];
    const topRisk = cleared.get(`${pkg}@${installed}`);
    expect(topRisk, `${pkg}@${installed} is fixable in ${current.name}`).toBeDefined();
    expect(top).toContain(`−${topRisk} risk`);
    expect(topRisk).toBe(Math.max(...cleared.values()));

    // No SonarQube project is linked in this stack.
    await expect(m).toContainText('No SonarQube project linked');

    // Choosing a pipeline opens its scan.
    await strip.getByRole('button', { name: new RegExp(`^Pipeline #${currentJob.pipelineId},`) }).click();
    await expect(page).toHaveURL(new RegExp(`/scans/${scanIds[current.name]}$`));
  });

  test('16. findings explorer: counts, filters, grouping, details, secrets and the upgrade plan', async () => {
    const page = root();
    await page.goto(`/services/${paymentsId}/findings`);
    const m = main(page);
    const list = m.getByRole('grid', { name: 'Vulnerabilities' });

    await expect(m.getByText(`${sum(current.counts)} vulnerabilities, ${current.fixable} fixable`)).toBeAttached();
    for (const [name, n] of Object.entries(current.counts)) {
      const label = name[0].toUpperCase() + name.slice(1);
      await expect(m.getByRole('button', { name: new RegExp(`^${label}\\s*${n}$`) })).toBeVisible();
    }
    // The live summary: the risk points of everything loaded.
    const summaryRegion = m.getByRole('region', { name: 'Selection summary' });
    await expect(summaryRegion).toContainText(`Risk${riskOf(current.counts)} pts`);

    // The Critical chip narrows the list to the critical findings, and the
    // summary counts the selection against the whole.
    const critical = current.findings.filter((f) => f.severity === 'critical');
    await m.getByRole('button', { name: /^Critical\s*\d+$/ }).click();
    await expect(page).toHaveURL(/severity=critical/);
    await expect(m.getByText(`${plural(critical.length, 'vulnerability', 'vulnerabilities')}, `)).toBeAttached();
    await expect(summaryRegion).toContainText(`of ${sum(current.counts)} in focus`);
    await expect(list.getByRole('row')).toHaveCount(critical.length + 1);
    for (const f of critical) await expect(list).toContainText(f.vulnId);

    // The details of a finding, from the keyboard: the fix, the other
    // services with the same ID, and an advisory link that keeps the opener.
    const first = critical[0];
    await list.getByRole('row').nth(1).focus();
    await page.keyboard.press('Enter');
    const sheet = page.getByRole('dialog', { name: new RegExp(first.vulnId) });
    await expect(sheet).toBeVisible();
    await expect(sheet).toContainText(first.fixed ? first.fixed : 'No fixed version yet');
    await expect(sheet).toContainText('Affects 1 other service: billing-worker.');
    const advisory = sheet.getByRole('link', { name: /Advisory/ });
    await expect(advisory).toHaveAttribute('href', /^https?:\/\//);
    await expect(advisory).toHaveAttribute('rel', /noopener/);
    await page.keyboard.press('Escape');
    await expect(sheet).toHaveCount(0);
    await expect(list.getByRole('row').nth(1)).toBeFocused();
    await m.getByRole('button', { name: 'Clear filters' }).first().click();
    await expect(page).not.toHaveURL(/severity=/);
    await expect(m.getByText(`${sum(current.counts)} vulnerabilities, `)).toBeAttached();

    // A search for a package narrows the list to that package.
    const needle = 'libc6';
    const hits = current.findings.filter((f) => f.pkg.toLowerCase().includes(needle));
    expect(hits.length).toBeGreaterThan(0);
    await m.getByLabel('Search package, vulnerability ID or title').fill(needle);
    await expect(m.getByText(`${plural(hits.length, 'vulnerability', 'vulnerabilities')}, `)).toBeAttached();
    await expect(list.getByRole('row')).toHaveCount(hits.length + 1);
    await expect(list).toContainText(needle);
    await expect(list).not.toContainText('zlib1g');
    await m.getByLabel('Search package, vulnerability ID or title').fill('');
    await expect(m.getByText(`${sum(current.counts)} vulnerabilities, `)).toBeAttached();

    // Grouped by package, the riskiest package comes first and folds away.
    await m.getByRole('radiogroup', { name: 'Group by' }).getByRole('radio', { name: 'Package' }).click();
    await expect(page).toHaveURL(/group=package/);
    const top = byPackage(current.findings)[0];
    const tree = m.getByRole('treegrid', { name: 'Vulnerabilities' });
    const group = tree.locator('[aria-level="1"]').first();
    await expect(group).toContainText(top.pkg);
    await expect(group).toContainText(`${plural(top.items.length, 'finding')}`);
    await expect(group).toContainText(`${top.risk} pts`);
    await expect(group).toHaveAttribute('aria-expanded', 'true');
    await group.focus();
    await page.keyboard.press('Enter');
    await expect(group).toHaveAttribute('aria-expanded', 'false');
    await m.getByRole('radiogroup', { name: 'Group by' }).getByRole('radio', { name: 'None' }).click();

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

    // The upgrade plan: fixable packages ranked by the risk they clear.
    await page.getByRole('tab', { name: /^Fix by upgrading/ }).click();
    const fixable = byPackage(current.findings.filter((f) => f.fixed !== ''));
    const plan = fixable.slice(0, 5);
    const cleared = plan.reduce((n, g) => n + g.risk, 0);
    await expect(m).toContainText(
      `upgrading ${plural(plan.length, 'package')} clears ${cleared} of ${riskOf(current.counts)}`,
    );
    const upgrades = m.getByRole('list', { name: 'Packages to upgrade' }).getByRole('listitem');
    await expect(upgrades).toHaveCount(fixable.length);
    await expect(upgrades.first()).toContainText(fixable[0].pkg);
    await expect(upgrades.first()).toContainText(`−${fixable[0].risk} pts`);
  });

  test('17. scan detail: links to GitLab and the comparison with an earlier scan', async () => {
    const page = root();
    await page.goto(`/scans/${scanIds[current.name]}`);
    const m = main(page);

    await expect(m.getByRole('heading', { name: current.artifact })).toBeVisible();
    const project = `${gitlabUrl}/${currentJob.projectPath}`;
    await expect(m.getByRole('link', { name: new RegExp(`^#${currentJob.pipelineId}`) })).toHaveAttribute('href', `${project}/-/pipelines/${currentJob.pipelineId}`);
    await expect(m.getByRole('link', { name: new RegExp(`^#${currentJob.jobId}`) })).toHaveAttribute('href', `${project}/-/jobs/${currentJob.jobId}`);
    await expect(m.getByRole('link', { name: /^01234567/ })).toHaveAttribute('href', `${project}/-/commit/0123456789abcdef0123456789abcdef01234567`);
    await expect(m).toContainText('protected');

    // Posture: the numbers of this scan, each a way into the explorer.
    const posture = m.getByRole('region', { name: 'Posture' });
    await expect(posture).toContainText(`${sum(current.counts)} open findings worth ${riskOf(current.counts)} risk points`);
    await expect(posture.getByRole('link', { name: /fixable/ })).toContainText(String(current.fixable));
    await expect(posture.getByRole('link', { name: /suppressed/ })).toContainText(
      String(current === imageSuppressed ? documented.suppressed.suppressed : 0),
    );

    // What changed since the previous scan of the same ref.
    const changes = m.getByRole('region', { name: 'What changed' });
    await expect(changes).toContainText(`Since pipeline #${previousJob.pipelineId} on main`);
    await expect(changes).toContainText(`${riskOf(previous.counts)} → ${riskOf(current.counts)} points`);
    await expect(page.getByRole('tab', { name: new RegExp(`^New\\s*${newKeys.length}$`) })).toBeVisible();
    await expect(page.getByRole('tab', { name: new RegExp(`^Fixed\\s*${fixedKeys.length}$`) })).toBeVisible();
    await expect(page.getByRole('tab', { name: new RegExp(`^Unchanged\\s*${unchangedKeys.length}$`) })).toBeVisible();
    // The view opens on the first list with findings.
    const opened = newKeys.length > 0 ? 'New' : 'Fixed';
    await expect(page.getByRole('tab', { name: new RegExp(`^${opened}`) })).toHaveAttribute('aria-selected', 'true');
    if (newKeys.length === 0) {
      await expect(m.getByRole('list', { name: 'Fixed findings' }).getByRole('listitem')).toHaveCount(fixedKeys.length);
    }

    // The picker lists the earlier scans of the artifact; the previous one is chosen.
    await changes.getByRole('button', { name: /^Compare with pipeline/ }).click();
    const options = page.getByRole('listbox', { name: 'Earlier scans' }).getByRole('option');
    await expect(options).toHaveCount(1);
    await expect(options.first()).toContainText(`Pipeline #${previousJob.pipelineId}`);
    await expect(options.first()).toContainText('Previous');
    await expect(options.first()).toHaveAttribute('aria-selected', 'true');
    await page.keyboard.press('Escape');

    // The same comparison, asked for by id, and a base that cannot be compared.
    await page.goto(`/scans/${scanIds[current.name]}?base=${scanIds[previous.name]}`);
    await expect(main(page).getByRole('region', { name: 'What changed' })).toContainText(`Since pipeline #${previousJob.pipelineId}`);
    await page.goto(`/scans/${scanIds[previous.name]}?base=${scanIds[current.name]}`);
    await expect(main(page)).toContainText('That scan cannot be compared');

    // The earlier scan knows it is no longer the current state, and is a first scan.
    await page.goto(`/scans/${scanIds[previous.name]}`);
    await expect(main(page)).toContainText('A newer scan of this artifact exists.');
    await expect(main(page)).toContainText('First scan of this artifact on this ref: every finding is new.');
    await expect(page.getByRole('tab', { name: new RegExp(`^New\\s*${previousKeys.size}$`) })).toBeVisible();
    await expect(main(page).getByRole('link', { name: new RegExp(`^#${previousJob.pipelineId}`) })).toHaveAttribute(
      'href',
      `${project}/-/pipelines/${previousJob.pipelineId}`,
    );
  });

  test('18. vulnerability search summarises the vulnerability and lists the affected services', async () => {
    const page = root();
    const inBoth = current.findings.find(
      (f) => f.severity === 'critical' && imageFull.findings.some((g) => g.vulnId === f.vulnId),
    );
    expect(inBoth, 'a critical finding present in both services').toBeDefined();
    const id = inBoth?.vulnId ?? '';

    await page.goto('/search');
    await expect(page.getByRole('heading', { name: 'Vulnerability search' })).toBeVisible();
    await page.keyboard.press('/');
    await expect(page.getByLabel('Vulnerability ID')).toBeFocused();
    await page.keyboard.type(id);
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL(new RegExp(`id=${id}`));
    const m = main(page);
    await expect(m).toContainText(`${id} affects 2 services in 2 images.`);
    const card = m.getByRole('region', { name: 'Vulnerability summary' });
    await expect(card).toContainText('Critical');
    await expect(card).toContainText(inBoth?.fixed ? `Fixed in ${inBoth.fixed}` : 'No fixed version reported yet.');
    const table = m.getByRole('table', { name: `Where ${id} is present` });
    await expect(table.getByRole('rowgroup', { name: 'payments-api' })).toContainText(inBoth?.pkg ?? '');
    await expect(table.getByRole('rowgroup', { name: 'billing-worker' })).toContainText(inBoth?.pkg ?? '');

    // A finding that payments-api suppressed is not in its current state.
    const suppressedHere = imageFull.findings.find(
      (f) => f.severity === 'critical' && !keys(current.findings).has(findingKey(f)),
    );
    if (suppressedHere) {
      await page.goto(`/search?id=${suppressedHere.vulnId}`);
      await expect(m).toContainText(`${suppressedHere.vulnId} affects 1 service in 1 image.`);
      await expect(m.getByRole('rowgroup', { name: 'billing-worker' })).toBeVisible();
      await expect(m.getByRole('rowgroup', { name: 'payments-api' })).toHaveCount(0);
    }

    await page.getByLabel('Vulnerability ID').fill('CVE-2099-0001');
    await page.keyboard.press('Enter');
    await expect(m.getByText('No service is affected in its current scans')).toBeVisible();
    await expect(m).toContainText('CVE-2099-0001 is not in the latest scan of any service, searching dashboard branches and protected refs.');
    await expect(m.getByRole('button', { name: 'Search all branches' })).toBeVisible();
  });

  test('19. admin: integrations, rejected uploads, audit log and archiving', async ({ request }) => {
    const page = root();
    const m = main(page);

    await page.goto('/admin/integrations');
    await expect(m.getByRole('heading', { name: 'Integrations' })).toBeVisible();
    await expect(m.getByRole('region', { name: 'Scan uploads from GitLab CI' })).toContainText(
      'Scan uploads are arriving',
    );
    const uploads = /Accepted, 24 h\s*(\d+)/.exec(await flat(m));
    expect(Number(uploads?.[1])).toBeGreaterThanOrEqual(4);
    await expect(m).toContainText(/Refused, 24 h\s*0/);
    // The 30-day chart counts today's uploads.
    await expect(m.getByRole('img', { name: /Accepted uploads per day/ })).toBeVisible();

    // A token for another audience is refused, and the page says why.
    const wrong = await upload(request, imageFull, { ...jobs.billing, pipelineId: 2002, jobId: 6002, audience: 'https://elsewhere.example.com' });
    expect(wrong.status()).toBe(401);
    expect(((await wrong.json()) as { error: { code: string } }).error.code).toBe('invalid_token');
    await page.reload();
    await expect(m).toContainText(/Refused, 24 h\s*1/);
    await expect(m).toContainText('Some uploads were refused');
    const refusedTable = m.getByRole('table', { name: 'Recently refused uploads' });
    await expect(refusedTable).toContainText('Invalid ID token');
    // A token that fails verification names no project.
    await expect(refusedTable).toContainText('Not known');

    // The audit log keeps it.
    await page.goto('/admin/audit');
    await chooseOption(page, 'Action', 'Upload rejected');
    await expect(page).toHaveURL(/action=ingest\.rejected/);
    await expect(m.getByRole('listitem')).toHaveCount(1);
    await expect(m).toContainText('refused: invalid id token');

    // Archiving hides a service and refuses its uploads; unarchiving undoes both.
    await page.goto('/admin/services');
    const table = m.getByRole('table', { name: 'Active services' });
    await expect(table).toContainText('payments-api');
    await expect(table).toContainText('billing-worker');
    await page.getByRole('button', { name: 'Actions for billing-worker' }).click();
    await page.getByRole('menuitem', { name: 'Archive' }).click();
    const dialog = page.getByRole('alertdialog');
    await expect(dialog.getByText('Archive billing-worker?')).toBeVisible();
    await dialog.getByRole('button', { name: 'Archive service' }).click();
    await expect(page.getByText('Service archived', { exact: true })).toBeVisible();
    await expect(table).not.toContainText('billing-worker');

    await page.goto('/');
    await expect(shownServices(page)).toHaveText('Showing 1 of 1 service');
    await expect(page.getByRole('heading', { level: 3 })).toHaveText(['payments-api']);

    const refused = await upload(request, imageFull, { ...jobs.billing, pipelineId: 2003, jobId: 6003 });
    expect(refused.status()).toBe(409);
    expect(((await refused.json()) as { error: { code: string } }).error.code).toBe('service_archived');

    await page.goto('/admin/services');
    await page.getByRole('tab', { name: 'Archived' }).click();
    await expect(m.getByRole('table', { name: 'Archived services' })).toContainText('billing-worker');
    await page.getByRole('button', { name: 'Actions for billing-worker' }).click();
    await page.getByRole('menuitem', { name: 'Unarchive' }).click();
    await expect(page.getByText('Service unarchived', { exact: true })).toBeVisible();

    await page.goto('/');
    await expect(page.getByRole('heading', { level: 3 })).toHaveText(['billing-worker', 'payments-api']);
    await expect(shownServices(page)).toHaveText('Showing 2 of 2 services');
  });

  test('20. a viewer sees the dashboard but not the admin screens', async () => {
    test.skip(!session.viewer, 'needs a signed-in GitLab user');
    const admin = root();
    const viewer = session.viewer?.() as Page;

    // The account was promoted in flow 6; make it a viewer again.
    await admin.goto('/admin/users');
    await chooseOption(admin, 'Role for alice', 'Viewer');
    await expect(admin.getByText('Alice Example is now a viewer.', { exact: true })).toBeVisible();

    await viewer.goto('/');
    await expect(account(viewer).getByText('Viewer')).toBeVisible();
    await expect(viewer.getByRole('heading', { name: 'Portfolio', exact: true })).toBeVisible();
    await expect(shownServices(viewer)).toHaveText('Showing 2 of 2 services');
    await expect(viewer.getByRole('listitem').filter({ hasText: 'platform/payments-api' })).toContainText(summary(current.counts));

    await serviceLink(viewer, 'payments-api').click();
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
    // The branch tag opens the editor in a popover.
    const editor = () => page.getByRole('dialog', { name: 'Dashboard branch of payments-api' });
    async function openEditor() {
      await row().getByRole('button', { name: /^Dashboard branch of payments-api/ }).click();
      await expect(editor()).toBeVisible();
    }
    async function choose(branch: string) {
      await page.goto('/admin/services');
      await openEditor();
      await editor().getByRole('combobox', { name: 'Branch name' }).fill(branch);
      await editor().getByRole('button', { name: 'Save branch' }).click();
      await expect(page.getByText(`payments-api shows ${branch}.`, { exact: true })).toBeVisible();
    }

    // Nothing is chosen yet: the tag says the latest scanned branch is shown.
    await page.goto('/admin/services');
    await expect(row()).toContainText(/Latest\s*main/);
    await openEditor();
    // The editor offers the branches that have scans.
    await expect(editor().getByRole('option')).toHaveCount(2);
    await page.keyboard.press('Escape');
    await expect(editor()).toHaveCount(0);
    await page.goto('/');
    await expect(tile()).toContainText('main');
    await expect(tile()).toContainText(summary(current.counts));

    // Choose the feature branch: the portfolio card and the dashboard follow it.
    await choose('feature/login');
    await expect(row()).toContainText('feature/login');
    await expect(row()).not.toContainText('Latest');
    await page.goto('/');
    await expect(tile()).toContainText('feature/login');
    await expect(tile()).toContainText(summary(imageFull.counts));
    await expect(tile()).toContainText(`${imageFull.fixable} fixable`);
    await tile().getByRole('link', { name: 'payments-api' }).click();
    await expect(page.getByTestId('ref-source')).toHaveText('Showing feature/login, the dashboard branch');
    await expect(m).toContainText('Current state of feature/login, latest scan per artifact');
    await expect(m.getByRole('img', { name: `payments-api: ${explainRisk(imageFull.counts)}` })).toBeVisible();
    // The feature branch has one scan: its findings are the start, not news.
    await expect(page.getByTestId('verdict')).toHaveText(
      `${imageFull.counts.critical} critical; first scan on this branch.`,
    );
    await expect(select(page, 'Branch')).toHaveText('feature/login');
    await page.getByRole('link', { name: 'Explore findings' }).click();
    await expect(m.getByText(`${sum(imageFull.counts)} vulnerabilities, ${imageFull.fixable} fixable`)).toBeVisible();

    // A branch without scans shows the empty state for that branch, not another branch.
    await choose('release/9');
    await page.goto('/');
    await expect(tile()).toContainText('No scans of release/9 yet.');
    await tile().getByRole('link', { name: 'payments-api' }).click();
    await expect(m).toContainText('No scans of release/9 yet');
    await expect(page.getByTestId('ref-source')).toHaveText('Showing release/9, the dashboard branch');
    await expect(m.getByRole('region', { name: 'Open findings' })).toHaveCount(0);

    // Clearing the choice brings back the latest scanned branch.
    await page.goto('/admin/services');
    await openEditor();
    await editor().getByRole('button', { name: 'Use latest scanned' }).click();
    await expect(page.getByText('payments-api shows the latest scanned branch.', { exact: true })).toBeVisible();
    await expect(row()).toContainText(/Latest\s*main/);
    await page.goto('/');
    await expect(tile()).toContainText('main');
    await expect(tile()).toContainText(summary(current.counts));
    await tile().getByRole('link', { name: 'payments-api' }).click();
    await expect(page.getByTestId('ref-source')).toHaveText(
      'Showing main, the latest scanned branch; no dashboard branch is chosen',
    );
    await expect(m).toContainText('Current state of main, latest scan per artifact');
    await expect(m.getByRole('img', { name: `payments-api: ${explainRisk(current.counts)}` })).toBeVisible();

    // Every change is in the audit log with the old and the new branch.
    await page.goto('/admin/audit');
    await chooseOption(page, 'Action', 'Service updated');
    // Each change shows the branch before and after.
    const changes = m.getByRole('definition');
    await expect(changes.filter({ hasText: 'from Latest scanned to feature/login' })).toHaveCount(1);
    await expect(changes.filter({ hasText: 'from feature/login to release/9' })).toHaveCount(1);
    await expect(changes.filter({ hasText: 'from release/9 to Latest scanned' })).toHaveCount(1);
  });

  test('21a. the search covers dashboard branches and protected refs unless all branches are asked for', async ({ request }) => {
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
    await expect(m).toContainText('Searched dashboard branches and protected refs');
    await expect(m.getByRole('rowgroup', { name: 'scope-demo' })).toHaveCount(0);

    await scope.getByRole('radio', { name: 'All branches' }).click();
    await expect(page).toHaveURL(new RegExp(`id=${id}&scope=all`));
    await expect(m).toContainText('Searched all branches');
    const hit = m.getByRole('rowgroup', { name: 'scope-demo' });
    await expect(hit).toBeVisible();
    await expect(hit).toContainText('feature/old');

    // The scope is part of the address: a reload keeps it, and so does a new search.
    await page.reload();
    await expect(scope.getByRole('radio', { name: 'All branches' })).toBeChecked();
    await expect(m.getByRole('rowgroup', { name: 'scope-demo' })).toBeVisible();
    await page.getByLabel('Vulnerability ID').fill('CVE-2099-0001');
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL(/id=CVE-2099-0001&scope=all/);
    await expect(m).toContainText('searching all branches.');

    await scope.getByRole('radio', { name: 'Dashboard branches and protected refs' }).click();
    await expect(page).toHaveURL(/\/search\?id=CVE-2099-0001$/);
  });
}

