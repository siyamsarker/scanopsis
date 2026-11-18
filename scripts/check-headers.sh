#!/usr/bin/env bash
# Copyright 2026 The Scanopsis Authors
# SPDX-License-Identifier: Apache-2.0
#
# Fail if a tracked source file lacks the SPDX licence identifier in its
# first five lines. Only tracked files are scanned, so dependencies and build
# output are never visited.
set -euo pipefail

bad=0
while IFS= read -r -d '' file; do
  if ! head -n 5 -- "$file" | grep -q 'SPDX-License-Identifier: Apache-2.0'; then
    echo "missing SPDX header: $file"
    bad=1
  fi
done < <(git ls-files -z -- '*.go' '*.ts' '*.tsx' '*.mjs' '*.js' '*.css' '*.sh' 'Makefile' '**/Makefile')

if [ "$bad" -ne 0 ]; then
  echo "Add 'SPDX-License-Identifier: Apache-2.0' within the first 5 lines of each file listed above." >&2
  exit 1
fi
