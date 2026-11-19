#!/usr/bin/env bash
# Copyright 2026 The Scanopsis Authors
# SPDX-License-Identifier: Apache-2.0
#
# Exercise check-headers.sh, check-dco.sh and release-notes.sh against a throwaway repository.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fails=0
expect() { # expect <pass|fail> <description> <command...>
  want=$1 desc=$2
  shift 2
  if "$@" >/dev/null 2>&1; then got=pass; else got=fail; fi
  if [ "$got" = "$want" ]; then
    echo "ok   $desc"
  else
    echo "FAIL $desc (wanted $want, got $got)"
    fails=1
  fi
}

cd "$tmp"
git init -q .
git config user.name "Test Author"
git config user.email "author@example.com"
git config commit.gpgsign false

printf '// SPDX-License-Identifier: Apache-2.0\npackage a\n' >a.go
git add a.go
git commit -q -s -m "base"
base=$(git rev-parse HEAD)

expect pass "headers: file with header" bash "$here/check-headers.sh"
printf 'package b\n' >b.go
git add b.go
expect fail "headers: file without header" bash "$here/check-headers.sh"
git rm -q -f b.go

echo one >f1
git add f1
git commit -q -s -m "signed"
expect pass "dco: signed commit" bash "$here/check-dco.sh" "$base" HEAD

echo two >f2
git add f2
git commit -q -m "unsigned"
expect fail "dco: unsigned commit" bash "$here/check-dco.sh" "$base" HEAD

git reset -q --hard HEAD~1
echo three >f3
git add f3
git commit -q -m "wrong email

Signed-off-by: Test Author <other@example.com>"
expect fail "dco: sign-off with different e-mail" bash "$here/check-dco.sh" "$base" HEAD

git reset -q --hard HEAD~1
echo four >f4
git add f4
git commit -q -m "sign-off quoted in the body only

Signed-off-by: Test Author <author@example.com>

Reviewed-by: Someone Else <someone@example.com>"
expect fail "dco: sign-off outside the trailer block" bash "$here/check-dco.sh" "$base" HEAD

git reset -q --hard HEAD~1
# A different author uses .mailmap to pose as the signer.
printf 'Test Author <author@example.com> Other Person <other@example.com>\n' >.mailmap
git add .mailmap
git commit -q --author "Other Person <other@example.com>" -m "mailmap rewrites the author

Signed-off-by: Test Author <author@example.com>"
expect fail "dco: .mailmap cannot change the author" bash "$here/check-dco.sh" "$base" HEAD

git reset -q --hard HEAD~1
expect fail "dco: empty range" bash "$here/check-dco.sh" HEAD HEAD
expect fail "dco: unknown revision" bash "$here/check-dco.sh" "$base" does-not-exist
expect fail "dco: empty argument" bash "$here/check-dco.sh" "" HEAD

# release-notes.sh: extract one section of a changelog.
cat >CHANGELOG.md <<'CL'
# Changelog

## [Unreleased]

### Added

- Not released yet.

## [1.2.0] - 2026-02-01

### Added

- Second feature.

### Fixed

- A bug.

## [1.10.0]

## [1.1.0] - 2026-01-01

- First feature.

[Unreleased]: https://example.com/compare/v1.2.0...HEAD
[1.2.0]: https://example.com/compare/v1.1.0...v1.2.0
[1.1.0]: https://example.com/releases/v1.1.0
CL
expect_notes() { # expect_notes <description> <version> <expected text>
  desc=$1 version=$2 want=$3
  if got=$(bash "$here/release-notes.sh" "$version" CHANGELOG.md 2>/dev/null) && [ "$got" = "$want" ]; then
    echo "ok   $desc"
  else
    echo "FAIL $desc"
    fails=1
  fi
}
expect_notes "notes: middle section stops at the next heading" 1.2.0 "### Added

- Second feature.

### Fixed

- A bug."
expect_notes "notes: last section drops the link references" 1.1.0 "- First feature."
expect pass "notes: dated heading is found" bash "$here/release-notes.sh" 1.1.0 CHANGELOG.md
expect fail "notes: missing version" bash "$here/release-notes.sh" 9.9.9 CHANGELOG.md
expect fail "notes: empty section" bash "$here/release-notes.sh" 1.10.0 CHANGELOG.md
expect fail "notes: 1.1 does not match 1.1.0" bash "$here/release-notes.sh" 1.1 CHANGELOG.md
expect fail "notes: version prefix is not a match" bash "$here/release-notes.sh" 1.2.1 CHANGELOG.md
expect fail "notes: missing changelog file" bash "$here/release-notes.sh" 1.2.0 nope.md
expect fail "notes: argument with a regex character" bash "$here/release-notes.sh" '1.2.*' CHANGELOG.md

exit "$fails"
