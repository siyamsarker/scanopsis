# Scanopsis

Scanopsis is a small, self-hosted dashboard that shows SonarQube and Trivy
results from your GitLab CI/CD pipelines in one view.

> **Status:** early development. The server and web interface build and run,
> but Scanopsis does not ingest or display scan results yet. Do not deploy it.

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

## Build from source

You need Go 1.27, Node.js 26 LTS and GNU Make.

```sh
make build
./bin/scanopsis
```

Open <http://localhost:8080>. See the [development guide](docs/development.md)
for details, the container image, and how to run the checks.

## Documentation

- [Development guide](docs/development.md)
- [Contributing](CONTRIBUTING.md)
- [Security policy](SECURITY.md)
- [Code of conduct](CODE_OF_CONDUCT.md)
- [Changelog](CHANGELOG.md)

## Licence

Scanopsis is licensed under the [Apache License 2.0](LICENSE).
