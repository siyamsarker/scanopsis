# Development guide

This guide explains how to set up a local development environment, build and
run Scanopsis, and run the same checks as CI.

## Prerequisites

Install the following tools:

| Tool | Version | Notes |
|---|---|---|
| Go | 1.27.4 | `go.mod` declares 1.27 as the minimum. CI uses 1.27.4. |
| Node.js | 26.13.1 (LTS) | The exact version is in `.nvmrc`. |
| GNU Make | 3.81 or later | The `Makefile` is the single entry point for every task. |
| Docker | Any current release with BuildKit | Needed to build the container image, to run a local PostgreSQL and to run the integration tests. |
| Git | Any current release | Version information is read from Git at build time. |

Go tools used by the checks (golangci-lint, govulncheck, go-licenses,
actionlint) are not installed separately. The `Makefile` runs them with
`go run` at pinned versions, so the first run downloads them into the Go
module cache.

If you use [nvm](https://github.com/nvm-sh/nvm), run these commands in the
repository root. They read the version from `.nvmrc`:

```sh
nvm install
nvm use
```

## Repository layout

| Path | Content |
|---|---|
| `cmd/scanopsis/` | Server entry point |
| `web/` | Web interface (React, TypeScript, Vite, Tailwind CSS) |
| `web/embed.go` | Embeds the built web interface into the Go binary |
| `scripts/` | Helper scripts used by CI |
| `.github/` | CI workflow, Dependabot, issue and pull request templates |
| `docs/` | Documentation for operators and developers |
| `api/openapi.yaml` | The HTTP API contract |
| `internal/` | Server packages: configuration, settings, authentication, HTTP API, database access |
| `deploy/compose/` | Docker Compose deployment |

## Common tasks

Run `make help` to list every target. The most common ones are:

| Command | What it does |
|---|---|
| `make check` | Runs every check that CI runs, except the image build. Run it before you open a pull request. |
| `make fmt` | Formats the Go code. |
| `make test` | Runs the Go unit tests with the race detector. |
| `make test-integration` | Runs the Go tests that need PostgreSQL, against a database in Docker. See [integration tests](#integration-tests). |
| `make generate` | Regenerates the database code and the web API types. See [generated code](#generated-code). |
| `make coverage` | Measures test coverage for Go and the web interface and enforces the minimums. Needs Docker. See [coverage](#coverage). |
| `make perf` | Measures the performance targets and fails when one is missed. Needs Docker. See [performance](#performance). |
| `make lint` | Lints the Go code. |
| `make web-test` | Installs the web dependencies and runs the web unit tests. |
| `make web-lint` | Type-checks, lints and checks the formatting of the web code. |
| `make build` | Builds the web interface, then builds `bin/scanopsis` with it embedded. |
| `make image` | Builds the container image `scanopsis:dev`. The Compose deployment in `deploy/compose` runs it. |
| `make clean` | Removes `bin/` and the web build output. |

## Build and run from source

1. Build the binary:

   ```sh
   make build
   ```

   The web interface is built first, into `web/dist/app`, and then embedded
   into `bin/scanopsis`.

2. Check the build:

   ```sh
   ./bin/scanopsis --version
   ```

   Expected output (your version and commit will differ):

   ```text
   scanopsis 42418ad (commit 42418ad, go1.27.4)
   ```

3. Start a PostgreSQL database for development:

   ```sh
   docker run -d --name scanopsis-dev-db \
     -e POSTGRES_USER=scanopsis -e POSTGRES_PASSWORD=dev -e POSTGRES_DB=scanopsis \
     -p 127.0.0.1:15432:5432 \
     postgres:18.7-alpine@sha256:ff64c9e3bdf8e2120107d3559de8db314e8305f7e38c5105903c6295876738ff
   ```

   The database is published on port 15432, so that it does not clash with a
   PostgreSQL that already runs on the default port 5432. If 15432 is taken
   too, choose another port here and in the URL in the next step.

4. Start the server. It needs a database URL, its public URL and a secret key:

   ```sh
   export SCANOPSIS_DATABASE_URL='postgres://scanopsis:dev@127.0.0.1:15432/scanopsis?sslmode=disable'
   export SCANOPSIS_PUBLIC_URL='http://localhost:8080'
   export SCANOPSIS_SECRET_KEY="$(openssl rand -base64 32)"
   ./bin/scanopsis
   ```

   The server listens on port 8080 and writes JSON logs to standard error.
   While no administrator exists, one log line holds the setup URL and token.
   Open <http://localhost:8080> in a browser and complete the setup. Press
   `Ctrl+C` to stop the server; it shuts down gracefully and logs
   `scanopsis stopped`.

   Keep the same `SCANOPSIS_SECRET_KEY` for as long as you keep the
   database. Store it in a file or in your shell profile, or remove the
   database with `docker rm -f scanopsis-dev-db` and start again. The
   [configuration reference](configuration.md) lists every variable.

5. Stop and remove the database when you are done:

   ```sh
   docker rm -f scanopsis-dev-db
   ```

### Administration commands during development

The server binary contains the same administration commands as the container.
They read `SCANOPSIS_DATABASE_URL` (or `SCANOPSIS_DATABASE_URL_FILE`) from the
environment, and the server must have run once so that the tables exist:

```sh
./bin/scanopsis admin reset-password root
./bin/scanopsis admin reset-2fa local:root
./bin/scanopsis admin -h
```

They are described in [operations](operations.md#administration-commands).

## Work on the web interface

For quick feedback while you change the web interface, use the Vite
development server instead of rebuilding the binary:

```sh
cd web
npm ci
npm run dev
```

Vite prints the local address, by default <http://localhost:5173>, and
reloads the page when you save a file.

Other scripts in `web/package.json`:

| Command (in `web/`) | What it does |
|---|---|
| `npm test` | Runs the unit tests once with Vitest. |
| `npm run lint` | Type-checks with TypeScript, then runs ESLint and the Prettier check. |
| `npm run format` | Formats the web code with Prettier. |
| `npm run build` | Builds into `dist/app` and fails if the initial JavaScript exceeds 300 KB gzipped. |

## Build and run the container image

1. Build the image:

   ```sh
   make image
   ```

2. Check that the image starts, with the same restrictions that the Compose
   file applies in production:

   ```sh
   docker run --rm --read-only --cap-drop ALL \
     --security-opt no-new-privileges scanopsis:dev --version
   ```

   Expected output (your version and commit will differ):

   ```text
   scanopsis acabe79 (commit acabe79, go1.27.4)
   ```

3. To run the whole stack, with a database, use the Compose deployment in
   `deploy/compose`. Follow the [getting started guide](getting-started.md).
   Without a database URL, a public URL and a secret key, the container
   prints `configuration error:` and exits.

The image is based on `gcr.io/distroless/static-debian13:nonroot`. It runs as
user `65532`, contains no shell and works with a read-only root file system.

## Integration tests

Tests that need a real database carry the Go build tag `integration`. `make
test` does not run them. `make test-integration` does:

```sh
make test-integration
```

Docker must be running. The tests start one PostgreSQL container, the same
image as the one in the Compose file, and create a fresh database in it for
each test. The container is removed when the tests end. The first run pulls
the image.

To run a single package or test, call `go` directly with the same tag:

```sh
go test -race -count=1 -tags integration ./internal/auth -run TestSetup
```

If the tests fail with `start PostgreSQL container`, Docker is not running or
the current user cannot reach the Docker socket.

## Coverage

`make coverage` is not part of `make check`, which stays fast. CI runs it in
the Coverage job. Docker must be running, because the Go part includes the
integration tests.

```sh
make coverage
```

The Go part runs the unit and integration tests once with
`-coverpkg=./internal/...,./cmd/...` into `coverage/go.out`, then
`scripts/covercheck` prints one line per package and fails when a minimum is
missed. The minimums are 85% for `internal/ingest`, `internal/auth`,
`internal/domain` and `internal/sonar`, and 75% overall. Generated `*.gen.go`
files, `internal/store/storetest` and `internal/testutil` are not counted.
Output of a run at the time of writing:

```text
package                           coverage  required
cmd/scanopsis                        84.2%         -
internal/audit                      100.0%         -
internal/auditlog                   100.0%         -
internal/auth                        95.3%    >= 85%
internal/catalog                     90.1%         -
internal/clientip                   100.0%         -
internal/config                      94.2%         -
internal/domain                     100.0%    >= 85%
internal/findings                    88.0%         -
internal/httpapi                    100.0%         -
internal/ingest                      94.1%    >= 85%
internal/secretbox                   88.9%         -
internal/settings                    98.3%         -
internal/sonar                       91.7%    >= 85%
internal/store                       78.3%         -
overall                              93.2%    >= 75%
```

To see which lines are not covered, run
`go tool cover -html=coverage/go.out`.

The web part runs `npm run coverage` in `web/` with `@vitest/coverage-v8`. It
measures the logic modules only, selected by the `include` patterns in
`web/vite.config.ts` (formatting, charts, `model.ts` and `links.ts` files,
service conditions, authentication, theme and the API client), and requires 75%
for lines, functions, branches and statements. A new module that matches a
pattern is measured without a change to the configuration. The lazily loaded
ECharts wrapper `src/charts/ChartCore.tsx` is excluded because jsdom cannot
draw it. The summary at the time of writing:

```text
File               | % Stmts | % Branch | % Funcs | % Lines
All files          |   96.91 |    89.67 |   95.62 |   97.08
 src/api           |     100 |       95 |     100 |     100
 src/auth          |   96.87 |    93.75 |     100 |   95.83
 src/charts        |    92.8 |    77.65 |   90.16 |   93.39
 src/format        |    98.3 |    98.21 |     100 |     100
 ...outes/findings |   97.67 |    89.58 |     100 |   97.18
 ...utes/portfolio |     100 |    94.11 |     100 |     100
 ...routes/service |     100 |    96.66 |     100 |     100
```

## Performance

`make perf` builds the web interface and the image, starts PostgreSQL and
Scanopsis in Docker, and checks the targets below. Docker must be running. It
removes its containers and network when it ends, also when it fails. CI runs
it in the Performance job.

```sh
make perf
```

| Measure | Target | How it is measured |
|---|---|---|
| Initial JavaScript | under 300 KB gzip | The size check of the web build. |
| Container cold start | under 2 s | Time from `docker run` until `/readyz` returns 200. PostgreSQL is already running and the image is already built. |
| Idle memory | under 64 MiB | `docker stats --no-stream` ten seconds after `/readyz` returned 200. |
| Portfolio and read API | p95 under 150 ms | The integration tests `TestPortfolioPerformance` (500 services, 45,000 scans) and `TestReadPerformance`, without the race detector. |
| Ingest of a 20 MB report | under 3 s | `TestIntegrationIngest20MB`, without the race detector. |

Result of a run on a development laptop:

```text
measure                                                        result        limit  status
------------------------------------------------------ -------------- ------------  ------
initial JavaScript (gzip)                                    105.8 KB     < 300 KB  ok
container cold start                                           454 ms    < 2000 ms  ok
idle memory                                                   9.1 MiB     < 64 MiB  ok
portfolio p95 (500 services)                                  54.9 ms     < 150 ms  ok
read API p95: /api/v1/services/1/overview                     14.2 ms     < 150 ms  ok
read API p95: /api/v1/services/1/vulnerabilities              44.2 ms     < 150 ms  ok
20 MB ingest: full-detail plain                              308.5 ms    < 3000 ms  ok
20 MB ingest: full-detail gzip                               546.3 ms    < 3000 ms  ok
20 MB ingest: dense plain                                    905.2 ms    < 3000 ms  ok
performance tests (go test)                                    ok         pass  ok
```

## Release notes

`scripts/release-notes.sh <version>` prints the body of the `## [<version>]`
section of `CHANGELOG.md`, without the heading and without the link references
at the end of the file. It fails when the section is missing or empty. The
release workflow uses it, and `make test-scripts` tests it. See
[releasing](releasing.md).

```sh
scripts/release-notes.sh 1.2.0 CHANGELOG.md
```

## Generated code

Two kinds of source files are generated and committed:

- The database access code in `internal/store`, generated by sqlc from the SQL
  files in `internal/store/queries` and `internal/store/migrations`.
- The TypeScript types of the web client in `web/src/api`, generated from
  `api/openapi.yaml`.

After you change a SQL query, a migration or the OpenAPI document, regenerate
both with:

```sh
make web-install
make generate
```

`make web-install` installs the web dependencies, which provide the
TypeScript generator. You need it once. Without it, `make generate` stops at
the second step with `openapi-typescript: command not found`. The first run
downloads sqlc into the Go module cache.

Expected result: the command ends with a line like
`../api/openapi.yaml → src/api/schema.gen.ts`, and `git status` shows no
change when the generated files were already up to date.

`make generate-check` runs the generation and fails if it changes any
committed file. CI uses it, so commit the generated files together with your
change.

## Conventions

- **Commits:** follow Conventional Commits and sign off every commit with
  `git commit -s`. See [CONTRIBUTING.md](../CONTRIBUTING.md).
- **Licence headers:** every source file starts with the SPDX header shown in
  CONTRIBUTING.md. `make headers` checks this.
- **Dependency pinning:** dependencies are pinned to exact versions
  (`go.mod`, `web/package.json` with `web/package-lock.json`), base images by
  digest in the `Dockerfile`, and GitHub Actions by commit SHA. Dependabot
  proposes updates weekly.
- **Licence policy:** distributed dependencies may only use Apache-2.0, MIT,
  BSD-2-Clause, BSD-3-Clause, ISC or 0BSD licences, plus OFL-1.1 for fonts.
  `make licenses` enforces this.

### Update the Trivy version used in CI

Dependabot does not update the Trivy version in
`.github/workflows/ci.yml`. To change it:

1. Download `trivy_<version>_Linux-64bit.tar.gz` from the
   [Trivy releases page](https://github.com/aquasecurity/trivy/releases).
2. Compute its SHA-256 with `shasum -a 256` (macOS) or `sha256sum` (Linux),
   and compare it with the line for that file in `trivy_<version>_checksums.txt`
   from the same release.
3. Set `TRIVY_VERSION` and `TRIVY_SHA256` in the workflow to the new values,
   in the same commit.

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| The browser shows "The web interface is not included in this build." | The binary was built with `go build` without building the web interface first. | Build with `make build`. |
| `make image` fails with `VERSION and COMMIT may only contain ...` | A build argument contains characters outside `A-Z a-z 0-9 . _ + -`. | Pass a plain version string, for example `make image VERSION=1.0.0`. |
| `make check` fails in `headers` | A new source file has no SPDX header. | Add the header from CONTRIBUTING.md to the listed file. |
