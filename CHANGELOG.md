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

[Unreleased]: https://github.com/siyamsarker/scanopsis/commits/main
