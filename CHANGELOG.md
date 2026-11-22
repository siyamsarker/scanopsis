# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Project scaffold: Go server entry point, embedded web interface, build tooling and project documentation.
- Sign-in with GitLab (OpenID Connect) with an optional restriction to GitLab groups, and sign-in with a local username and password, with rate limits on failed attempts.
- Two-factor authentication with an authenticator app and ten single-use recovery codes. Every user must enrol before using the application.
- First-run setup that creates the first administrator, protected by a one-time token written to the server log.
- Settings page for administrators. GitLab sign-in, local sign-in and session timeouts change without a restart. Environment variables seed the settings on first start.
- Administration commands `scanopsis admin reset-password` and `scanopsis admin reset-2fa` for recovering access.
- Docker health check, using the new `scanopsis healthcheck` command, so the image works without a shell or curl.
- Docker Compose deployment in `deploy/compose`, with documentation for installation, configuration, GitLab sign-in, operations, backup and restore.
- Upload of Trivy JSON reports (image vulnerabilities and secrets) from GitLab CI jobs, authenticated with GitLab CI/CD ID tokens instead of a shared secret. Services are created by the first upload or by an administrator; uploads can be limited to GitLab groups and protected refs.
- Read API for the portfolio, per-service overview, scans, new and fixed findings between scans, vulnerabilities, secret findings, trends and a cross-service vulnerability search.
- SonarQube integration: signed webhooks, quality gate, ratings, coverage and duplication read with a read-only token, a periodic reconciliation, support for MQR and Standard modes, and integration health pages for administrators.
- Guides for uploading from GitLab CI (including a scan script that keeps an existing pipeline gate) and for connecting SonarQube.
- Web interface for the results: a portfolio of all services (grid and treemap), a dashboard per service with Trivy and SonarQube posture, trends and recent scans, a findings explorer with a "fix first" list and secret findings, a scan page with the changes since the previous scan, and a cross-service CVE search. Dark and light themes; works on phones.
- Administration pages for services, users, integration health and the audit log.
- A dashboard branch for each service. An administrator chooses, on the Services page, the branch (ref) that the portfolio, the service dashboard and the findings explorer show, so a scan of a developer's branch can no longer replace what everyone sees. Until one is chosen, the branch with the most recent scan is shown, as before. A chosen branch without scans shows "No scans of that branch yet". The API returns `default_ref` on services and `ref_source` on the overview, trends and portfolio items, and `PATCH /api/v1/services/{id}` accepts `default_ref`.
- Data retention: scan history, SonarQube analyses and deliveries are kept for a configurable number of days (`retention.days`, default 90), always keeping the latest scan of every service, ref, artifact and kind; audit events are kept for 365 days. Each run that deletes data is recorded in the audit log.
- Coverage thresholds for the backend and the web interface (`make coverage`), a performance check of start-up time, memory, bundle size and response times (`make perf`), an image scan (`make image-scan`), a sign-off check (`make dco`).

- Versions are published as `release/X.Y.Z` branches with `vX.Y.Z` tags. `main` holds development code and is not for deployment. There is no published container image: you build the image from a version branch. See `docs/versions.md`.
- `make web-audit`, part of `make check`, audits the runtime npm dependencies for known vulnerabilities.

### Security

- Ten wrong second-factor codes in a row lock two-factor sign-in for the account until an administrator resets it, and sign the account out everywhere.
- A session gets a new token once the second factor passes.
- After repeated wrong passwords for an account, local sign-in is refused for a short time even with the right password, so the limit does not reveal a correct guess.
- Failed sign-ins for a username that matches no account no longer record the typed name.
- Changing the GitLab or SonarQube URL requires entering the matching secret again, so a stored secret is never sent to a new address.
- Rejected uploads that need no allowed project are recorded in the audit log at most a few times per client address, and rate-limited uploads are not recorded.
- At most four uploads are processed at once, and the server warns at start when every project of the GitLab instance may upload.
- Upload error responses no longer contain the GitLab URL or upstream error text.
- npm dependencies are installed without running their install scripts.

[Unreleased]: https://github.com/siyamsarker/scanopsis/commits/main
