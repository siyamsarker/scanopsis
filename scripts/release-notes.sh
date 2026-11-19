#!/usr/bin/env bash
# Copyright 2026 The Scanopsis Authors
# SPDX-License-Identifier: Apache-2.0
#
# Print the release notes of one version: the body of its "## [x.y.z]" section
# in CHANGELOG.md, without the heading and without the link references at the
# end of the file. Fails when the section is missing or empty.
#
# Usage: scripts/release-notes.sh <x.y.z> [changelog-file]
set -euo pipefail

version=${1:-}
file=${2:-$(dirname "$0")/../CHANGELOG.md}

if ! [[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "usage: release-notes.sh <x.y.z> [changelog-file]" >&2
  exit 2
fi
[ -f "$file" ] || { echo "release-notes: $file not found" >&2; exit 1; }

notes=$(awk -v want="$version" '
  /^## \[/ {
    if (inside) exit
    head = $0
    sub(/^## \[/, "", head)
    sub(/\].*/, "", head)
    inside = (head == want)
    found = found || inside
    next
  }
  inside { print }
  END { if (!found) exit 3 }
' "$file") || {
  echo "release-notes: no \"## [$version]\" section in $file" >&2
  exit 1
}

# Drop the link references that follow the last section, then blank edges.
notes=$(printf '%s\n' "$notes" | awk '
  { lines[NR] = $0 }
  END {
    last = NR
    while (last > 0 && (lines[last] ~ /^\[[^]]+\]: / || lines[last] ~ /^[[:space:]]*$/)) last--
    first = 1
    while (first <= last && lines[first] ~ /^[[:space:]]*$/) first++
    for (i = first; i <= last; i++) print lines[i]
  }')

if [ -z "$notes" ]; then
  echo "release-notes: the \"## [$version]\" section in $file is empty" >&2
  exit 1
fi
printf '%s\n' "$notes"
