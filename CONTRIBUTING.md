# Contributing to Scanopsis

Thank you for your interest in contributing to Scanopsis. This guide explains how to set up your environment, run the checks, and submit changes.

## Ways to contribute

You can contribute in several ways:

- Report a bug or request a feature by opening an issue.
- Improve the documentation.
- Fix a bug or implement a feature by submitting a pull request.

Open an issue before you start a large change. This gives the maintainer and other contributors a chance to discuss the approach before you spend time on the implementation.

## Prerequisites

Install the following tools before you start.

- Go 1.27
- Node.js 26 LTS. The exact version is set in the `.nvmrc` file at the root of the repository.
- GNU Make
- Docker, if you want to build the container image

## Local checks

Run the checks before you open a pull request. CI runs the same checks.

1. Run `make check` to run every check that CI runs, except the image build. It performs the following steps:
   - Checks that Go code is formatted.
   - Lints the Go code.
   - Runs the Go tests with the race detector.
   - Checks Go dependencies for known vulnerabilities with `govulncheck`.
   - Lints and type-checks the web interface.
   - Runs the web unit tests.
   - Builds `bin/scanopsis` with the web interface embedded.
2. If `make check` reports formatting problems, run `make fmt` to format the Go code, and then run `make check` again.
3. Run `make help` to list all available targets.

To build the container image, run `make image`.

Two slower checks are not part of `make check`, but CI runs them. Both need Docker. Run them when you change code that they measure:

- `make coverage` enforces the test coverage minimums for Go and the web interface.
- `make perf` checks the performance targets: bundle size, start-up time, idle memory, and the speed of the portfolio and report upload.

See the [development guide](docs/development.md) for the details.

## Commit messages

Write commit messages that follow the [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) specification. A commit message has a type, an optional scope, and a short description. Examples:

```text
feat(ingest): accept gzip-compressed reports
fix(web): keep severity order in trend chart
```

Use a body after a blank line to explain what changed and why, when the reason is not obvious from the description.

## Developer Certificate of Origin

Every commit must be signed off to certify that you have the right to submit it under the project licence. Sign-off is based on the [Developer Certificate of Origin](https://developercertificate.org/), which you accept by adding a `Signed-off-by` line to each commit. The line states your name and e-mail address, and it is added when you commit with the `-s` flag:

```sh
git commit -s -m "fix(web): keep severity order in trend chart"
```

If you forgot the sign-off on your latest commit, amend it:

```sh
git commit --amend -s
```

If you forgot the sign-off on several commits, rebase your branch onto `main` and add the sign-off to each commit:

```sh
git rebase --signoff main
```

Then push the branch again with `git push --force-with-lease`.

## Pull requests

Keep each pull request small and focused on a single change. Follow these guidelines:

1. Add or update tests for any change in behaviour.
2. Update the documentation that is affected by the change.
3. Add an entry under the `Unreleased` section of `CHANGELOG.md`.
4. Make sure `make check` passes.
5. Describe what the change does and how you tested it. The pull request template asks for both.

## Code of conduct

All participants must follow the [Code of Conduct](CODE_OF_CONDUCT.md). Report unacceptable behaviour to the contact address given in that document.

## Security issues

Do not report security vulnerabilities in public issues or pull requests. Follow the process in [SECURITY.md](SECURITY.md) instead.

## Licence

Scanopsis is licensed under the [Apache License 2.0](LICENSE). By contributing, you agree that your contributions are licensed under the same licence.

Every new source file must begin with the following SPDX header:

```text
// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0
```
