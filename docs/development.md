# Develop Scanopsis

Set up your machine, build and run Scanopsis from source, and run the checks
before you send a change. The repository has no CI service, so every check runs
on your machine.

**Audience:** Developers · **Back to:** [README](../README.md) · [All guides](../README.md#documentation)

---

## Set up your machine

Install these tools:

| Tool | Version | Why |
| --- | --- | --- |
| Go | 1.27.4 | Builds the server. `go.mod` needs 1.27 or later. |
| Node.js | 26.13.1 | Builds the web interface. The exact version is in `.nvmrc`. |
| GNU Make | 3.81 or later | The `Makefile` runs every task. |
| Docker | A current release with BuildKit | Builds the image, and runs PostgreSQL for development and for the integration, end-to-end and performance tests. |
| Git | A current release | The build reads the version from Git. |

You do not install the Go tools that the checks use (golangci-lint, govulncheck,
go-licenses, sqlc). The `Makefile` runs them with `go run` at pinned versions,
so the first run downloads them into the Go module cache.

If you use [nvm](https://github.com/nvm-sh/nvm), run these in the repository
root. They read the version from `.nvmrc`:

```sh
nvm install
nvm use
```

## Find your way around

| Path | What is there |
| --- | --- |
| `cmd/scanopsis/` | The server entry point and the `admin` commands. |
| `internal/` | The server packages: configuration, settings, sign-in, HTTP API, upload, SonarQube, retention, database access. |
| `internal/store/` | Database access: SQL queries, migrations and the code sqlc generates from them. |
| `web/` | The web interface: React, TypeScript, Vite and Tailwind CSS. `web/embed.go` embeds the build into the Go binary. |
| `api/openapi.yaml` | The contract of the HTTP API. |
| `test/e2e/` | End-to-end tests with Playwright. |
| `test/fakegitlab/` | A fake GitLab sign-in provider for the end-to-end tests. |
| `scripts/` | Helper scripts for the checks, and the generator of the API reference. |
| `deploy/compose/` | The Docker Compose deployment. |
| `docs/` | This documentation. |

[Architecture](architecture.md) explains how the parts fit together.

## Build and run from source

This runs the binary on your machine for development only. In production,
Scanopsis runs only as a container, see the
[getting started guide](getting-started.md).

1. Build the binary:

   ```sh
   make build
   ```

   This builds the web interface into `web/dist/app` first, then embeds it into
   `bin/scanopsis`.

2. Check the build:

   ```sh
   ./bin/scanopsis --version
   ```

   It prints the version, the commit and the Go version, for example:

   ```text
   scanopsis v0.4.2 (commit <commit>, <go version>)
   ```

   The commit and the Go version are those of your build.

3. Start a PostgreSQL database for development:

   ```sh
   docker run -d --name scanopsis-dev-db \
     -e POSTGRES_USER=scanopsis -e POSTGRES_PASSWORD=dev -e POSTGRES_DB=scanopsis \
     -p 127.0.0.1:15432:5432 \
     postgres:18.7-alpine@sha256:0115267ab5a509022df9319c77be365cef335adb71556c75f60f5360ee247e71
   ```

   Port 15432 keeps clear of a PostgreSQL that may already run on 5432. If 15432
   is taken too, pick another port here and in the URL of the next step.

4. Start the server. It needs a database URL, its public URL and a secret key:

   ```sh
   export SCANOPSIS_DATABASE_URL='postgres://scanopsis:dev@127.0.0.1:15432/scanopsis?sslmode=disable'
   export SCANOPSIS_PUBLIC_URL='http://localhost:8080'
   export SCANOPSIS_SECRET_KEY="$(openssl rand -base64 32)"
   ./bin/scanopsis
   ```

   The server listens on port 8080 and writes JSON logs to standard error. While
   no administrator exists, a warning line holds the `setup_url` and the
   `setup_token`. Open <http://localhost:8080> and finish the setup with that
   token.

   Press `Ctrl+C` to stop the server. It shuts down gracefully and logs
   `scanopsis stopped`.

   The secret key encrypts secrets in the database, so keep the same
   `SCANOPSIS_SECRET_KEY` for as long as you keep the database. Save it in a
   file, or start over with a fresh database. The [configuration
   reference](configuration.md) lists every variable.

5. When you are done, remove the database:

   ```sh
   docker rm -f scanopsis-dev-db
   ```

### Run the administration commands

The binary has the same `admin` commands as the container. They read only
`SCANOPSIS_DATABASE_URL` (or `SCANOPSIS_DATABASE_URL_FILE`), and the server must
have started once, so that the tables exist:

```sh
./bin/scanopsis admin -h
./bin/scanopsis admin reset-password root
./bin/scanopsis admin reset-2fa local:root
```

[Operations](operations.md) describes each command.

## Change the web interface

The quickest loop is to rebuild the web interface and the binary, then restart
the server. It takes a few seconds:

```sh
(cd web && npm run build) && go build -o bin/scanopsis ./cmd/scanopsis
```

A binary built this way reports the version `dev`. Use `make build` for a binary
with the version stamped in.

`npm run dev` starts the Vite development server on <http://localhost:5173>,
which reloads the page when you save a file. It has no proxy to the API, so
pages that load data do not work there. Use it only for layout work that needs
no data.

The scripts in `web/package.json`:

| Command (in `web/`) | What it does |
| --- | --- |
| `npm ci --ignore-scripts` | Installs the dependencies exactly as locked, like `make web-install`. |
| `npm test` | Runs the unit tests once with Vitest. |
| `npm run lint` | Type-checks with TypeScript, then runs ESLint and the Prettier check. |
| `npm run format` | Formats the web code with Prettier. |
| `npm run build` | Builds into `dist/app`, and fails if the initial JavaScript is larger than 300 KB gzipped. |
| `npm run coverage` | Runs the unit tests with coverage, see [Measure coverage](#measure-coverage). |

## Run the checks

Run `make help` to list every target. These are the ones you need most:

| Command | What it does |
| --- | --- |
| `make check` | Runs the fast checks: formatting, lint, Go unit tests, vulnerabilities, web lint and unit tests, build, licence headers, script tests, licences and the npm audit. Run it before you open a pull request. |
| `make fmt` | Formats the Go code. |
| `make lint` | Lints the Go code. |
| `make test` | Runs the Go unit tests with the race detector. |
| `make test-integration` | Runs the Go tests that need PostgreSQL. See [Run the integration tests](#run-the-integration-tests). |
| `make web-test` | Installs the web dependencies and runs the web unit tests. |
| `make web-lint` | Type-checks, lints and checks the formatting of the web code. |
| `make web-audit` | Audits the runtime npm dependencies and fails on a high or critical advisory. Needs network access. |
| `make generate` | Regenerates the database code, the web API types and the API reference. See [Regenerate code and the API reference](#regenerate-code-and-the-api-reference). |
| `make coverage` | Measures test coverage and enforces the minimums. Needs Docker. |
| `make perf` | Measures the performance targets. Needs Docker. |
| `make e2e` | Runs the end-to-end tests in a browser. Needs Docker. |
| `make image` | Builds the container image `scanopsis:dev`. |
| `make clean` | Removes `bin/` and the web build output. |

`make check` leaves out coverage, performance, the end-to-end tests and the
image scan, so it stays fast. Run those when your change touches what they
measure.

## Run the integration tests

Tests that need a real database carry the Go build tag `integration`. `make
test` skips them. To run them, start Docker and run:

```sh
make test-integration
```

The tests start one PostgreSQL container, the same image as in the Compose file,
and create a fresh database in it for each test. The container is removed when
the tests end. The first run pulls the image.

To run one package or one test, call `go test` with the same tag:

```sh
go test -race -count=1 -tags integration ./internal/auth -run TestSetup
```

If the tests fail with `start PostgreSQL container`, Docker is not running, or
your user cannot reach the Docker socket.

## Run the end-to-end tests

```sh
make e2e
```

This builds Scanopsis and a fake GitLab, starts them with PostgreSQL as the
Docker Compose project `scanopsis-e2e`, and runs the Playwright tests in
`test/e2e` in Chromium. The first run downloads Chromium. The stack publishes
ports 18080 and 19000 on `127.0.0.1`, so those must be free.

The stack is removed at the end. To keep it running for a look in the browser,
run `E2E_KEEP=1 make e2e`, and stop it later with `make e2e-down`.

## Measure coverage

```sh
make coverage
```

Docker must be running, because the Go part includes the integration tests.

- **Go:** the unit and integration tests run once with
  `-coverpkg=./internal/...,./cmd/...` and write `coverage/go.out`. Then
  `scripts/covercheck` prints one line per package and fails if a minimum is
  missed: 85% for `internal/ingest`, `internal/auth`, `internal/domain` and
  `internal/sonar`, and 75% overall. Generated `*.gen.go` files,
  `internal/store/storetest` and `internal/testutil` are not counted. To see the
  lines that are not covered, run `go tool cover -html=coverage/go.out`.
- **Web:** `npm run coverage` in `web/` measures the logic modules only:
  formatting, the visual kit (`src/viz`), `model.ts` and `links.ts` files,
  service conditions, sign-in, theme and the API client. The `include` patterns in
  `web/vite.config.ts` select them, so a new module that matches is measured
  without a change. It requires 75% of lines, functions, branches and
  statements.

## Measure performance

```sh
make perf
```

This builds the web interface and the image, starts PostgreSQL and Scanopsis in
Docker and checks these targets. It removes its containers and network when it
ends, also when it fails.

| Measure | Target | How it is measured |
| --- | --- | --- |
| Initial JavaScript | under 300 KB gzipped | The size check of the web build. |
| Container cold start | under 2 s | From `docker run` until `/readyz` returns 200, with PostgreSQL running and the image built. |
| Idle memory | under 64 MiB | `docker stats --no-stream`, ten seconds after `/readyz` returned 200. |
| Portfolio and read API | p95 under 150 ms | The integration tests `TestPortfolioPerformance` (500 services, 45,000 scans) and `TestReadPerformance`, without the race detector. |
| Upload of a 20 MB report | under 3 s | `TestIntegrationIngest20MB`, without the race detector. |

## Regenerate code and the API reference

Three kinds of files are generated and committed:

- The database code in `internal/store`, generated by sqlc from the SQL in
  `internal/store/queries` and `internal/store/migrations`.
- The TypeScript types of the web client in `web/src/api`, generated from
  `api/openapi.yaml`.
- The API reference [`docs/api.md`](api.md), generated from `api/openapi.yaml`
  by `scripts/apidocs`.

After you change a SQL query, a migration or the OpenAPI document, run:

```sh
make generate
```

It installs the web dependencies first, which bring the TypeScript generator.
The first run also downloads sqlc into the Go module cache. When it is done,
`git status` shows the regenerated files, or nothing if they were already up to
date.

`make generate-check` regenerates everything and fails if a committed file
changes. Run it before you open a pull request, and commit the generated files
with your change.

To change the layout of the API reference itself, edit
`scripts/apidocs/main.go`, then update and review its golden file:

```sh
go test ./scripts/apidocs -update
git diff scripts/apidocs/testdata/golden.md
```

## Build and run the container image

1. Build the image:

   ```sh
   make image
   ```

2. Check that it starts with the same restrictions that the Compose file uses in
   production:

   ```sh
   docker run --rm --read-only --cap-drop ALL \
     --security-opt no-new-privileges scanopsis:dev --version
   ```

   It prints the version line, for example `scanopsis v0.4.2 (commit <commit>,
   <go version>)`, with the commit and Go version of your build.

3. To run the whole stack with a database, use the Compose deployment in
   `deploy/compose` and follow the [getting started guide](getting-started.md).
   Without a database URL, a public URL and a secret key, the container prints
   `configuration error:` with the missing variables, and exits.

The image is based on `gcr.io/distroless/static-debian13:nonroot`. It runs as
user `65532`, has no shell and works with a read-only root file system.

`make image-scan` builds the image and scans it with Trivy, which you install
yourself. It fails on any HIGH or CRITICAL vulnerability and on any secret. Run
it when you change the `Dockerfile` or a dependency.

## Test data

The upload tests in `internal/ingest` read real Trivy reports from
`internal/ingest/testdata`. They were written by Trivy 0.76.2 with the
vulnerability database version 2 downloaded on 2026-11-18, and nothing in them
was edited. The tests compare the numbers Scanopsis stores with an independent
count over the JSON and with the `Total:` lines Trivy prints.

| File | How it was made |
| --- | --- |
| `image-full.json` | `trivy image --scanners vuln --format json --show-suppressed --quiet -o image-full.json python:3.9-slim-buster` |
| `image-suppressed.json` | The same, run in a directory with the `.trivyignore` below, adding `--ignorefile .trivyignore` |
| `secret-fs.json` | `trivy fs --scanners secret --format json --quiet -o secret-fs.json .` in a directory that holds the `config.env` below |
| `*.totals.txt` | `trivy convert --format table --quiet --show-suppressed -o x.txt <file>.json`, then `grep -E '^(Total\|Suppressed )' x.txt` |

The image has OS packages (Debian 10.13) and Python packages, with fixed and
unfixed findings.

The `.trivyignore` for `image-suppressed.json`:

```text
CVE-2019-8457
CVE-2022-40897
CVE-2023-5752
```

The `config.env` for `secret-fs.json` holds fake, non-working values in the
shape of AWS credentials. These commands create it. The values are split so that
secret scanners do not flag the commands themselves:

```sh
printf 'AWS_ACCESS_KEY_ID=%s%s\n' AKIA Z7Q3N5XK2WPLM4RT > config.env
printf 'AWS_SECRET_ACCESS_KEY=%s%s\n' wJalrXUtnFEMI/K7MDENG/ bPxRfiCYzq4mNp8LdT >> config.env
```

When you add a fixture, write down here how you made it.

## Conventions

- **Commits:** use Conventional Commits and sign off every commit with `git
  commit -s`. `make dco` checks the sign-offs of every commit after
  `origin/main`. Set `DCO_BASE` to check another range, for example `make dco
  DCO_BASE=HEAD~3`. `make commits` checks the subjects of the same range: a
  Conventional Commits type, a lowercase description and at most 72
  characters. See [CONTRIBUTING.md](../CONTRIBUTING.md).
- **Licence headers:** every source file starts with the SPDX header shown in
  CONTRIBUTING.md. `make headers` checks it.
- **Pinned dependencies:** dependencies have exact versions (`go.mod`, and
  `web/package.json` with `web/package-lock.json`), and base images are pinned
  by digest in the `Dockerfile`. Update them one at a time, on purpose, and run
  every check afterwards.
- **Licence policy:** distributed dependencies may only use Apache-2.0, MIT,
  BSD-2-Clause, BSD-3-Clause, ISC or 0BSD licences, plus OFL-1.1 for fonts.
  `make licenses` enforces it.

## Troubleshooting

| Symptom | Cause | Fix |
| --- | --- | --- |
| The browser shows "The web interface is not included in this build." | The binary was built with `go build` before the web interface was built. | Run `make build`, or `(cd web && npm run build)` before `go build`. |
| `make image` fails with `VERSION and COMMIT may only contain A-Z a-z 0-9 . _ + -` | A build argument has other characters. | Pass a plain version, for example `make image VERSION=v0.4.2`. |
| `make check` fails in `headers` | A new source file has no SPDX header. | Add the header from CONTRIBUTING.md to the file it names. |
| `make generate` fails with `openapi-typescript: command not found` | The web dependencies are missing. | Run `make web-install`, then `make generate` again. |
| Integration tests fail with `start PostgreSQL container` | Docker is not running, or your user cannot reach the Docker socket. | Start Docker, or fix the socket permissions. |
| The server stops with `configuration error:` | A required variable is missing or wrong. The lines below it name each one. | Set the variables, see [configuration](configuration.md). |

---

[Back to the README](../README.md) · [All guides](../README.md#documentation) · Found a mistake? [Open an issue](https://github.com/siyamsarker/scanopsis/issues)
