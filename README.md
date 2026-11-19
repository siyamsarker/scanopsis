# Scanopsis

Scanopsis is a small, self-hosted dashboard that shows SonarQube and Trivy
results from your GitLab CI/CD pipelines in one view.

> **Status:** early development. Sign-in with two-factor authentication,
> first-run setup, user management, the settings page, Trivy uploads from
> GitLab CI and the SonarQube integration work, and the API serves the
> results. The web pages that display scan results are not built yet, so do
> not rely on it for production use.

## Planned features

- Sign-in with GitLab (OpenID Connect) and a local administrator account for
  recovery.
- Trivy image and secret scan results uploaded from GitLab CI jobs and
  authenticated with GitLab CI/CD ID tokens, so no shared secret is needed.
- SonarQube quality gate, ratings, coverage and duplication, received through
  signed webhooks and the SonarQube Web API.
- A portfolio of all services, a dashboard per service with trends across
  pipelines, and a findings explorer with cross-service CVE lookup.
- One container plus PostgreSQL.

Every number on the dashboard will be traceable to the pipeline, job and commit
that produced it.

Scanopsis works with SonarQube and Trivy. It is not affiliated with or endorsed
by SonarSource, Aqua Security or GitLab.

## Quick start

No release image is published yet, so you build the image first and then start
Scanopsis and PostgreSQL with Docker Compose:

```sh
make image
cd deploy/compose
```

Then create the secrets and the configuration file, run `docker compose up -d`
and finish the setup in the browser. The
[getting started guide](docs/getting-started.md) explains each step, how to
check it and what to do when it fails.

## Build from source

You need Go 1.27, Node.js 26 LTS, GNU Make and a PostgreSQL database. The
[development guide](docs/development.md) shows how to build the binary, start
a database, run the server and run the checks.

## Documentation

For operators:

- [Getting started](docs/getting-started.md)
- [Configuration reference](docs/configuration.md)
- [Sign in with GitLab](docs/integrations/gitlab-sso.md)
- [Upload Trivy reports from GitLab CI](docs/integrations/gitlab-ci.md)
- [Connect SonarQube](docs/integrations/sonarqube.md)
- [Operations](docs/operations.md): health checks, logs, administration
  commands, recovering access, backup and restore
- [Docker Compose files](deploy/compose/README.md)

For contributors and project policy:

- [Development guide](docs/development.md)
- [Contributing](CONTRIBUTING.md)
- [Security policy](SECURITY.md)
- [Code of conduct](CODE_OF_CONDUCT.md)
- [Changelog](CHANGELOG.md)

## Licence

Scanopsis is licensed under the [Apache License 2.0](LICENSE).
