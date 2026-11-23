#!/usr/bin/env bash
# Copyright 2026 Siyam Sarker
# SPDX-License-Identifier: Apache-2.0
#
# Usage: check-dco.sh <base-sha> <head-sha>
#
# Fail if any non-merge commit in base..head lacks a Signed-off-by trailer
# that matches the commit author's name and e-mail address. The check fails
# closed: unresolvable revisions or an empty range are errors.
set -euo pipefail

if [ "$#" -ne 2 ] || [ -z "$1" ] || [ -z "$2" ]; then
  echo "usage: $0 <base-sha> <head-sha>" >&2
  exit 2
fi

for rev in "$1" "$2"; do
  if ! git rev-parse --quiet --verify "${rev}^{commit}" >/dev/null; then
    echo "cannot resolve commit: $rev" >&2
    exit 1
  fi
done
base=$(git rev-parse "$1^{commit}")
head=$(git rev-parse "$2^{commit}")

if ! shas=$(git rev-list --no-merges "$base..$head"); then
  echo "cannot list commits in $base..$head" >&2
  exit 1
fi
if [ -z "$shas" ]; then
  echo "no commits to check in $base..$head" >&2
  exit 1
fi

bad=0
while IFS= read -r sha; do
  # Raw author identity: .mailmap must not be able to rewrite who the author is.
  author=$(git log -1 --no-mailmap --format='%an <%ae>' "$sha")
  # Only real trailers count, not a sign-off quoted in the message body.
  if ! git log -1 --format='%(trailers:key=Signed-off-by,valueonly)' "$sha" | grep -qxF -- "$author"; then
    echo "missing or mismatched sign-off: $(git log -1 --no-mailmap --format='%h %s' "$sha")"
    echo "  expected: Signed-off-by: $author"
    bad=1
  fi
done <<<"$shas"

if [ "$bad" -ne 0 ]; then
  echo "Every commit needs a Signed-off-by line matching its author." >&2
  echo "Fix with: git rebase --signoff $base" >&2
  echo "then push again with: git push --force-with-lease" >&2
  exit 1
fi
