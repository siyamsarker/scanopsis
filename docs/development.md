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
| Docker | Any current release with BuildKit | Only needed to build the container image. |
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
| `docs/` | Documentation |

## Common tasks

Run `make help` to list every target. The most common ones are:

| Command | What it does |
|---|---|
| `make check` | Runs every check that CI runs, except the image build. Run it before you open a pull request. |
| `make fmt` | Formats the Go code. |
| `make test` | Runs the Go tests with the race detector. |
| `make lint` | Lints the Go code. |
| `make web-test` | Installs the web dependencies and runs the web unit tests. |
| `make web-lint` | Type-checks, lints and checks the formatting of the web code. |
| `make build` | Builds the web interface, then builds `bin/scanopsis` with it embedded. |
| `make image` | Builds the container image `scanopsis:dev`. |
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

3. Start the server:

   ```sh
   ./bin/scanopsis
   ```

   The server listens on port 8080 and writes JSON logs to standard error.
   Open <http://localhost:8080> in a browser. Press `Ctrl+C` to stop it; the
   server shuts down gracefully and logs `scanopsis stopped`.

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

2. Run it with the same restrictions recommended for production:

   ```sh
   docker run --rm -p 8080:8080 --read-only --cap-drop ALL \
     --security-opt no-new-privileges scanopsis:dev
   ```

3. Open <http://localhost:8080>. Press `Ctrl+C` to stop the container.

The image is based on `gcr.io/distroless/static-debian13:nonroot`. It runs as
user `65532`, contains no shell and works with a read-only root file system.

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
  BSD-2-Clause, BSD-3-Clause or ISC licences, plus OFL-1.1 for fonts.
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
