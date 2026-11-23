#!/usr/bin/env bash
# Copyright 2026 Siyam Sarker
# SPDX-License-Identifier: Apache-2.0
#
# Exercise check-headers.sh and check-dco.sh and check-commits.sh against a throwaway repository.
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

# Commit subject checks use their own commits on top of the signed history.
git reset -q --hard "$base"
echo c1 >c1
git add c1
git commit -q -s -m "feat(web): add a conventional subject"
expect pass "commits: conventional subject passes" bash "$here/check-commits.sh" "$base" HEAD

git reset -q --hard "$base"
echo c2 >c2
git add c2
git commit -q -s -m "feat(web): $(printf 'x%.0s' $(seq 1 70))"
expect fail "commits: subject over 72 characters fails" bash "$here/check-commits.sh" "$base" HEAD

git reset -q --hard "$base"
echo c3 >c3
git add c3
git commit -q -s -m "add a subject without a type"
expect fail "commits: missing type fails" bash "$here/check-commits.sh" "$base" HEAD

git reset -q --hard "$base"
echo c4 >c4
git add c4
git commit -q -s -m "fix: Start with an uppercase letter"
expect fail "commits: uppercase description fails" bash "$here/check-commits.sh" "$base" HEAD

git reset -q --hard "$base"
git checkout -q -b side
echo c5 >c5
git add c5
git commit -q -s -m "feat: a side commit"
git checkout -q -
echo c6 >c6
git add c6
git commit -q -s -m "fix: a main commit"
git merge -q --no-ff -m "Merge branch 'side'" side
expect pass "commits: merge commits are skipped" bash "$here/check-commits.sh" "$base" HEAD

expect fail "commits: empty range" bash "$here/check-commits.sh" HEAD HEAD
expect fail "commits: unknown revision" bash "$here/check-commits.sh" "$base" does-not-exist
expect fail "commits: empty argument" bash "$here/check-commits.sh" "" HEAD

exit "$fails"
