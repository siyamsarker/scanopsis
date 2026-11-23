// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { execFile } from 'node:child_process';
import { fileURLToPath } from 'node:url';

/** A Docker Compose project the tests start and stop. */
export interface Stack {
  project: string;
  file: string;
  /** The service whose log holds the setup token. */
  app: string;
}

export const mainStack: Stack = {
  project: 'scanopsis-e2e',
  file: fileURLToPath(new URL('../compose.yaml', import.meta.url)),
  app: 'scanopsis',
};

/**
 * The stack of the backup tests: its own database, so that a restore cannot
 * disturb the other tests. `app` is the installation that gets set up and
 * backed up; `empty` stays empty for a restore from the setup screen.
 */
export const backupStack: Stack = {
  project: 'scanopsis-e2e-backups',
  file: fileURLToPath(new URL('../compose.backups.yaml', import.meta.url)),
  app: 'scanopsis',
};
export const emptyService = 'scanopsis-empty';

/** Runs `docker compose` against a test stack and returns standard output. */
export function compose(args: string[], stack: Stack = mainStack): Promise<string> {
  const baseArgs = ['compose', '-p', stack.project, '-f', stack.file];
  return new Promise((resolve, reject) => {
    execFile(
      'docker',
      [...baseArgs, ...args],
      { maxBuffer: 16 * 1024 * 1024 },
      (error, stdout, stderr) => {
        if (error) {
          reject(new Error(`docker compose ${args.join(' ')} failed:\n${stderr || error.message}`));
        } else {
          resolve(stdout);
        }
      },
    );
  });
}

/** Reads the one-time setup token Scanopsis writes to its log on first start. */
export async function readSetupToken(
  stack: Stack = mainStack,
  service: string = stack.app,
): Promise<string> {
  const logs = await compose(['logs', '--no-log-prefix', service], stack);
  for (const line of logs.split('\n')) {
    if (!line.includes('"setup_token"')) continue;
    const entry = JSON.parse(line) as { setup_token?: string };
    if (entry.setup_token) return entry.setup_token;
  }
  throw new Error(`the log of ${service} has no setup_token line`);
}

/** Sets the temporary password of a local account and returns it. */
export async function resetPasswordWithCli(username: string): Promise<string> {
  const out = await compose([
    'exec',
    '-T',
    'scanopsis',
    '/scanopsis',
    'admin',
    'reset-password',
    username,
  ]);
  // The password is the only non-empty line after the line ending in "is:".
  const lines = out.split('\n').map((l) => l.trim());
  const at = lines.findIndex((l) => l.endsWith('is:'));
  const password = at >= 0 ? lines.slice(at + 1).find((l) => l !== '') : undefined;
  if (!password) throw new Error(`no temporary password in the CLI output:\n${out}`);
  return password;
}
