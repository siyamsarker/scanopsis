// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { compose, readSetupToken } from './fixtures/stack';

export default async function globalSetup(): Promise<void> {
  // Remove anything a killed earlier run left behind, then start clean.
  await compose(['down', '-v', '--remove-orphans']);
  await compose(['up', '-d', '--build', '--wait']);
  // Environment changes made here are inherited by the test worker.
  process.env.E2E_SETUP_TOKEN = await readSetupToken();
}
