// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { compose } from './fixtures/stack';

export default async function globalTeardown(): Promise<void> {
  if (process.env.E2E_KEEP === '1') {
    console.log('E2E_KEEP=1: the stack keeps running; stop it with `make e2e-down`.');
    return;
  }
  await compose(['down', '-v', '--remove-orphans']);
}
