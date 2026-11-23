# Contributing

This page shows you how to propose a change to Scanopsis: the checks to run, how to write commits, and what a good pull request contains.

**Audience:** Developers · **Back to:** [README](README.md) · [All guides](README.md#documentation)

---

Thank you for helping. Every fix, test and doc improvement counts.

## Ways to help

- Report a bug or ask for a feature in an
  [issue](https://github.com/siyamsarker/scanopsis/issues).
- Improve the documentation.
- Fix a bug or build a feature in a pull request.

For a large change, open an issue first. You can agree on the approach before
you spend time on the code.

Found a security problem? Do not open an issue. Follow the
[security policy](SECURITY.md) instead.

## Set up

You need Go 1.27, Node.js 26 (the exact version is in `.nvmrc`), GNU Make,
and Docker for the image and the slower tests. The
[development guide](docs/development.md) shows how to build, run the server
against a local database, and work on the web interface.

## Run the checks

The repository has no CI service, so your local checks are the gate. Run them
before you open a pull request:

```sh
make check
```

`make check` formats and lints the Go code, runs the Go tests with the race
detector, checks Go dependencies with `govulncheck`, lints, type-checks and
tests the web interface, builds `bin/scanopsis`, checks the licence headers,
tests the helper scripts, checks dependency licences and audits the runtime
npm dependencies. If it reports formatting problems, run `make fmt` and try
again. `make help` lists every target.

The slower checks need Docker. Run the ones that cover your change:

| Command | What it checks |
| --- | --- |
| `make test-integration` | Go tests against a real PostgreSQL. |
| `make e2e` | End-to-end tests in a browser. |
| `make coverage` | The coverage minimums for Go and the web interface. |
| `make perf` | Bundle size, start-up time, idle memory and response times. |
| `make generate-check` | Generated code and `docs/api.md` are up to date. |
| `make image-scan` | Builds the image and scans it with Trivy. |
| `make dco` | Every commit is signed off. |

## Write commit messages

Follow [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/):
a type, an optional scope, and a short description.

```text
feat(ingest): accept gzip-compressed reports
fix(web): keep severity order in trend chart
```

After a blank line, add a body that says what changed and why, when the
reason is not obvious.

## Sign off your commits

Every commit needs a `Signed-off-by` line. With it you certify the
[Developer Certificate of Origin](https://developercertificate.org/): that you
have the right to submit the change under the project's licence. Commit with
`-s` to add it:

```sh
git commit -s -m "fix(web): keep severity order in trend chart"
```

Forgot it on the last commit? Run `git commit --amend -s`. On several commits,
run `git rebase --signoff main`, then push again with
`git push --force-with-lease`.

## Open a pull request

Keep each pull request small and about one thing. Open it from your fork
against `main`. Version branches (`release/X.Y.Z`) never change, so they
never take pull requests; see [versions and upgrades](docs/versions.md).

Before you open it:

1. Add or update tests for every change in behaviour.
2. Update the guides your change affects (see below).
3. Make sure `make check` passes.

In the description, say:

- What changed, and why.
- How you tested it.
- Which tests and guides you added or updated.
- That your commits are signed off.

## Keep the guides current

A change to a feature that users see, or to a critical part such as sign-in,
uploads, settings or the database, updates the matching guide **in the same
pull request**. A pull request that changes behaviour without its guide is not
finished.

| If you change... | Update |
| --- | --- |
| Features, requirements, screenshots | [README](README.md) |
| Install steps, first start | [Getting started](docs/getting-started.md) |
| A setting, variable, default or validation message | [Configuration reference](docs/configuration.md) |
| Compose files, proxy, sizing, other ways to run | [Deployment](docs/deployment.md) |
| Administration commands, backup, retention, health checks | [Operations](docs/operations.md) |
| An error message or a failure mode | [Troubleshooting](docs/troubleshooting.md) |
| Sign-in, sessions, two-factor, secrets, limits | [Security model](docs/security.md) |
| Trivy upload or the CI script | [GitLab CI](docs/integrations/gitlab-ci.md) |
| GitLab sign-in | [GitLab sign-in](docs/integrations/gitlab-sso.md) |
| The SonarQube integration | [SonarQube](docs/integrations/sonarqube.md) |
| The API | `api/openapi.yaml`, then run `make generate` to rebuild [docs/api.md](docs/api.md) |
| How numbers are counted or shown | [Verifying the numbers](docs/verification.md) |
| Components, data flow, schema | [Architecture](docs/architecture.md) |
| Build, tooling, checks | [Development](docs/development.md) and this page |
| Versioning or the release steps | [Versions and upgrades](docs/versions.md) |

## Licence

Scanopsis is licensed under the [Apache License 2.0](LICENSE). By contributing,
you agree that your contributions are licensed under the same licence.

Every new source file starts with this header (in the comment style of its
language); `make headers` checks it:

```text
// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0
```

## Code of conduct

Everyone who takes part follows the [code of conduct](CODE_OF_CONDUCT.md).

---

[Back to the README](README.md) · [All guides](README.md#documentation) · Found a mistake? [Open an issue](https://github.com/siyamsarker/scanopsis/issues)
