#!/usr/bin/env bash
# Copyright 2026 The Scanopsis Authors
# SPDX-License-Identifier: Apache-2.0
#
# Usage: check-dco.sh <base-sha> <head-sha>
#
# Fail if any non-merge commit in base..head lacks a Signed-off-by line that
# matches the commit author's name and e-mail address.
set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "usage: $0 <base-sha> <head-sha>" >&2
  exit 2
fi
base=$1
head=$2

bad=0
for sha in $(git rev-list --no-merges "$base..$head"); do
  author=$(git log -1 --format='%aN <%aE>' "$sha")
  if ! git log -1 --format=%B "$sha" | grep -qxF "Signed-off-by: $author"; then
    echo "missing or mismatched sign-off: $(git log -1 --format='%h %s' "$sha")"
    echo "  expected: Signed-off-by: $author"
    bad=1
  fi
done

if [ "$bad" -ne 0 ]; then
  echo "Every commit needs a Signed-off-by line matching its author." >&2
  echo "Fix with: git rebase --signoff $base" >&2
  echo "then push again with: git push --force-with-lease" >&2
  exit 1
fi
