// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

const codeClass = 'rounded-[4px] bg-sunken px-1 font-mono text-text';

/**
 * Where the setup token is: the command of the Compose install, as the
 * getting-started guide gives it.
 */
export function SetupTokenHint() {
  return (
    <>
      Printed in the server log at start-up. In <code className={codeClass}>deploy/compose</code>,
      run <code className={codeClass}>docker compose logs scanopsis</code> and copy the{' '}
      <code className={codeClass}>setup_token</code> value.
    </>
  );
}
