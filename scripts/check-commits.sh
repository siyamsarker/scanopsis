#!/usr/bin/env bash
# Copyright 2026 Siyam Sarker
# SPDX-License-Identifier: Apache-2.0
#
# Usage: check-commits.sh <base-sha> <head-sha>
#
# Fail if the subject of any non-merge commit in base..head is not a
# Conventional Commits subject of at most 72 characters. The check fails
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

pattern='^(feat|fix|docs|chore|refactor|test|perf|build|ci|style|revert)(\([a-z0-9/._-]+\))?!?: [a-z0-9`"'"'"'(]'
max=72

bad=0
while IFS= read -r sha; do
  subject=$(git log -1 --format='%s' "$sha")
  if ! [[ $subject =~ $pattern ]]; then
    echo "not a conventional subject: $(git log -1 --format='%h %s' "$sha")"
    bad=1
  elif [ "${#subject}" -gt "$max" ]; then
    echo "subject longer than $max characters (${#subject}): $(git log -1 --format='%h %s' "$sha")"
    bad=1
  fi
done <<<"$shas"

if [ "$bad" -ne 0 ]; then
  echo "Use: type(scope): lowercase description, at most $max characters." >&2
  echo "Types: feat fix docs chore refactor test perf build ci style revert." >&2
  echo "Fix with: git rebase -i $base (reword the listed commits)" >&2
  exit 1
fi
