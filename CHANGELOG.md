# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `scanopsis admin export-report <scan-id>` writes the original Trivy report of a scan to standard output, for checking a stored scan with `trivy convert`. It works in the container without a shell: `docker compose exec scanopsis /scanopsis admin export-report 42 > report.json`. A successful export is recorded in the audit log as `report.exported`; a report cleared by data retention fails with a clear message.
- Rejected SonarQube webhooks (wrong signature, oversized or invalid body) are recorded in the audit log as `webhook.rejected` with the reason and client address, at most 10 per address and then one per minute.
- An **Account** page, opened from your name in the header, lists your sessions: when each started, when it was last used, the client address and the browser. You can end any other session, or all of them at once. The API is `GET` and `DELETE /api/v1/me/sessions` and `DELETE /api/v1/me/sessions/{id}`, and ending sessions is recorded in the audit log as `session.revoked`.
- Administrators can disable a user, which refuses every sign-in (local and GitLab) and ends all sessions of the user, enable the user again, and delete a user. The **Users** page has **Disable**, **Enable** and **Delete** with a confirmation, the API accepts `disabled` in `PATCH /api/v1/users/{id}` and has `DELETE /api/v1/users/{id}`, and the administration commands `disable-user`, `enable-user` and `delete-user` do the same. Nobody can disable or delete their own account or the last enabled administrator. The audit log records `user.disabled`, `user.enabled` and `user.deleted`. Users have a new `disabled_at` field.
- Upload limits per GitLab project (100 reports at once, then one every 10 seconds) and per CI job (50 artifacts), answered with `429` and a `Retry-After` header.
- Deployment guide sections for an existing or managed PostgreSQL (with a tested Compose override and certificate-verified TLS), plain `docker run`, Podman and Kubernetes, and proxy examples with compression and optional rate limits.
- The Compose file sets a memory limit with a matching `GOMEMLIMIT`, a process limit, log rotation, a stop grace period, a read-only file system for PostgreSQL, and variables for the bind address and port.

### Changed

- SonarQube webhook deliveries are processed by four workers that run apart from the periodic sync, so a slow SonarQube or a burst of webhooks no longer delays the other. The queue holds 500 deliveries, and a delivery that still finds it full stays pending and is read by the next sync instead of being marked failed.
- A SonarQube sync stops early when the version call fails or after three calls in a row get no answer, holds no pooled database connection while it waits for SonarQube, and logs one summary warning per sync instead of one per project.
- Data retention now keeps the latest scan of a ref forever only when the ref is the service's dashboard branch, was protected, or is the ref the dashboards show when no branch is chosen. The latest scan of any other unprotected branch expires after the retention period, so storage no longer grows with short-lived branches.
- Data retention empties the stored original report of scans that are not current after 30 days (fixed). The report was never read and was most of the database and of every backup.
- The `settings.changed` audit event records, besides the keys, the value before and after for each changed setting (`changes`), and for a secret only that it changed, so the audit log shows what a change did.
- Demoting an administrator is refused when no other enabled administrator would remain; a disabled administrator no longer counts.
- Sessions now end after 30 minutes without use (was 8 hours) and 12 hours after sign-in at the latest (was 24 hours), as OWASP ASVS level 2 asks. Values stored in the database before the upgrade are kept. The session cookie is now a browser-session cookie without an expiry date, so closing the browser ends it; the server still enforces both timeouts.
- Uploads in their database phase are limited to a quarter of the connection pool (at least 2), taken after the body is read and checked, so a burst of small uploads can no longer use every connection. An upload that cannot get a place within 30 seconds is answered `503 busy` with `Retry-After`.
- When GitLab's signing keys cannot be fetched (the request hangs, fails or returns a bad answer), uploads that need them at the same time now share one request and are all answered with `503 gitlab_unavailable` when it ends, and the failure is remembered for 10 seconds, so a hanging GitLab no longer makes uploads queue behind one request each (503 after 10, 20, 30 seconds and more). Cached keys stay in use, and a recovered GitLab is used after the 10 seconds.
- A small upload that fits the free memory is no longer held behind a large one that waits for memory: it is admitted at once, unless the oldest waiting upload has already waited 5 seconds, then newer uploads queue behind it so large uploads cannot be starved. The wait for memory is still at most 30 seconds.
- Pages stay fast with 90 days of history at 500 services: new indexes and queries bring the portfolio, findings page, scan comparison and search to well under 150 ms at that volume, and the performance tests now seed that volume. Search results and scan comparisons are capped, most severe first, and report the true totals.
- The database pool has 16 connections and statements end after 30 seconds by default; both can be set in the database URL. Migrations no longer run under the start-up timeout, build their indexes without blocking writes, and log each step.
- Uploads stream findings into the database and are admitted by an estimate of the memory they need, so one maximum report peaks at about 80 MiB instead of about 280 MiB, and memory returns to idle after a burst. The image sets `GOMEMLIMIT=400MiB` for the recommended 512 MiB container.
- An upload may take 5 minutes to arrive and is answered `408` when the body is late; a database outage is answered `503` with `Retry-After` instead of `500`. The CI script retries these answers for about four minutes.

### Fixed

- Reads are no longer 2 to 5 times slower for hours after data retention cleared or deleted many rows. Autovacuum now starts at 5% dead rows and refreshes statistics at 2% changed rows on `scans` and `vulnerabilities` (the defaults are 20% and 10%), and a retention run that deleted scans or cleared reports in the number of 10,000 or more ends with `ANALYZE` of `scans` (and of `vulnerabilities` when it deleted scans). The retention log line has a new `analyzed` field.
- Under heavy load the server no longer lets requests queue for a database connection for seconds. It serves as many API requests at once as the pool has connections (16 by default), makes the others wait at most 3 seconds and then answers `503` with the code `busy` and a `Retry-After` header. The probes, the web interface and the Trivy upload are not counted. The API reference documents the new answer on every operation.
- `/readyz` checks the database over a connection of its own instead of one from the pool, so a server that is busy with requests stays ready and an orchestrator no longer restarts it. The connection is opened again after a failure and counts against `max_connections` of the database in addition to the pool.
- Stopping the server with an upload still running no longer logs an error and exits with status 1 after the 15-second wait. The deadline is expected on a deliberate stop: the server logs a warning with the number of requests it cut off and exits with status 0. A real failure still exits with status 1.

### Security

- Sign-in rate limits can no longer be reset by flooding the server with made-up user names: a name whose failure budget is spent is kept apart from the bounded list that new names push old ones out of. IPv6 clients are now counted by `/48` instead of `/64` for sign-in and setup.
- GitLab users are identified by the GitLab URL and the user id there, instead of the user id alone, so a sign-in through another GitLab instance never takes over an existing account. Existing GitLab users are assigned to the GitLab URL stored in the settings. After a change of the GitLab URL, GitLab users sign in as new viewers (see the operations guide). The administration commands refuse a `gitlab:<name>` that matches accounts of several instances and accept `gitlab-id:<n>` and `id:<n>`.
- New recovery codes have 20 characters in four groups of five (about 99 bits, was about 50) and are stored as an HMAC-SHA-256 over the user id and the code, keyed from `SCANOPSIS_SECRET_KEY`, instead of a plain SHA-256. Codes issued before keep working until they are used or replaced by a new enrolment.
- GitLab sign-in no longer redirects to another site when `return_to` hides a backslash behind a dot segment, such as `/./%5Cevil.example`. The value is now cleaned before it is checked, any backslash or control character (raw or percent-encoded) is refused, and the redirect is built from the path and query only. The web interface applies the same rule.
- Upload tokens whose `nbf` or `iat` lie in the future are refused, and forged tokens no longer make Scanopsis fetch GitLab's signing keys again and again.
- A service created by an upload is recorded in the audit log.

## [0.1.0] - 2026-11-22

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
- The server warns at start when every project of the GitLab instance may upload.
- Upload error responses no longer contain the GitLab URL or upstream error text.
- npm dependencies are installed without running their install scripts.

[Unreleased]: https://github.com/siyamsarker/scanopsis/compare/v0.1.0...main
[0.1.0]: https://github.com/siyamsarker/scanopsis/tree/release/0.1.0
