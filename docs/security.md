# Security

This page helps you decide whether Scanopsis is safe to run in your network:
what it protects, from whom, how, and what it leaves to you. It ends with a
hardening checklist.

**Audience:** Operators · **Back to:** [README](../README.md) · [All guides](../README.md#documentation)

---

To report a vulnerability, see [report a vulnerability](#report-a-vulnerability).
For how the parts fit together, see the [architecture](architecture.md).

## What needs protecting

| Asset | Why it matters | Where it lives |
|---|---|---|
| Scan data | A list of every service's known vulnerabilities shows an attacker where to strike. Treat it as sensitive. | `scans`, `vulnerabilities`, `secret_findings`, `sonar_analyses` tables |
| Credentials | The GitLab application secret, the SonarQube token and the webhook secret. | `settings` table, encrypted |
| Sessions | A stolen session is a signed-in user. | `sessions` table (hashed), browser cookie |
| Two-factor secrets and recovery codes | They are the second factor. | `users` table (encrypted), `recovery_codes` table (keyed hash) |
| Password hashes | They allow offline guessing of local passwords. | `users` table (Argon2id) |
| `SCANOPSIS_SECRET_KEY` | It decrypts every stored secret. | A mounted secret file, or the environment |
| The setup token | It creates the first administrator. | The server log, until setup is done |
| The audit log | Evidence of what happened. | `audit_log` table |
| Backup files | One holds everything above except sessions: password hashes, encrypted secrets, scan data and the audit log. | The `backups` folder, and any copy you made |

## Who can do what

| Actor | Trust | What they can do |
|---|---|---|
| Viewer | Signed in, two-factor verified | Read scan data and SonarQube results. |
| Administrator | Signed in, two-factor verified | Everything a viewer can, plus settings, users, services, the audit log and backups (make, download, upload, delete, restore). |
| CI job | Anyone who can run a job on your GitLab | Upload reports for its own project, if the project is allowed. |
| SonarQube | Your SonarQube server | Send signed webhooks. Scanopsis reads from it with a read-only token. |
| Network attacker | None | Reach the public address, read or change traffic without TLS, send forged requests. |
| Anyone with the database or a backup | Fully trusted by design | Read everything that is not encrypted, but not the encrypted secrets without the key. |
| Anyone with the host, the container or the log | Fully trusted by design | Read the key, the setup token and the database URL. |

Scanopsis does not defend against an attacker who controls the host, the
database and the key at once.

In short, the trust boundaries are: TLS ends at your reverse proxy; the proxy
and Scanopsis share a private network; Scanopsis reaches PostgreSQL over a
private network; GitLab and SonarQube are trusted to say who a user or a job
is, and nothing more.

## How Scanopsis protects it

Each row names where the control lives, so you can check it in the code.

### Sessions and sign-in

| Control | What it does | Where |
|---|---|---|
| Hashed session tokens | The browser holds 32 random bytes; the database stores only their SHA-256. A database leak gives away no sessions. | `internal/auth/sessions.go` |
| Cookie attributes | `HttpOnly` and `SameSite=Lax`. With an `https` public URL also `Secure`, with the `__Host-` prefix, so browsers refuse it for another domain or path. | `internal/auth/sessions.go` |
| Session lifetime | A session ends after the idle timeout (default 30 minutes) or the absolute timeout (default 12 hours), as OWASP ASVS level 2 asks. The cookie has no expiry, so the browser drops it on close. A new sign-in replaces the old session. | `internal/auth/sessions.go`, [runtime settings](configuration.md#runtime-settings) |
| Session list | The **Account** page lists your sessions (start, last use, address, browser) and lets you end any other one, or all. Each is recorded as `session.revoked`. | `internal/auth/mysessions.go`, `web/src/routes/AccountPage.tsx` |
| Cross-site request protection | Changes from a browser must come from the same origin. The server checks `Sec-Fetch-Site` and `Origin` and answers `403 cross_origin` otherwise. Requests without those headers (curl, CI jobs) are not browser requests and pass. | `internal/httpapi/httpapi.go` |
| Mandatory two-factor authentication | Every user, local or GitLab, enrols a TOTP app. A session that has not passed the second factor can only call the sign-in and enrolment endpoints. | `internal/httpapi/middleware.go`, `internal/auth/mfa.go` |
| New token after the second factor | A correct code gives the session a new random token. A token caught between password and code never becomes a full session. | `internal/auth/sessions.go` |
| Lock after ten wrong codes | Ten wrong codes in a row lock the user's second factor (`423 mfa_locked`) and end the user's sessions, until an administrator resets two-factor authentication or the password. A correct code resets the count. | `internal/auth/mfa.go` |
| No code replay | Each accepted TOTP time step is recorded; the same or an older step is refused. A code is valid one step (30 seconds) before or after now. | `internal/auth/mfa.go` |
| Recovery codes | Ten single-use codes of 20 characters (about 99 bits each), shown once. Each is stored as an HMAC-SHA-256 with a key derived from `SCANOPSIS_SECRET_KEY`, so a copy of the database alone cannot test guesses. Shorter codes from version 0.1.0 keep working until used or replaced. | `internal/auth/mfa.go` |
| Rate limits | Sign-in, setup, two-factor codes, password changes and uploads are limited per client address or per user, see [rate limits](configuration.md#rate-limits). After repeated wrong passwords for a name, every password for it is refused for a while, so the limit cannot confirm a guess. A flood of made-up names cannot reset a spent budget. IPv6 clients count by `/48` for sign-in and setup. | `internal/auth/ratelimit.go`, `internal/auth/local.go` |
| Argon2id passwords | 64 MiB, 3 passes, 4 lanes. At most two hashes run at once, so a flood cannot exhaust memory. An unknown user takes as long as a known one. Passwords need at least 12 characters. | `internal/auth/password.go`, `internal/auth/policy.go` |
| GitLab sign-in | Authorization code flow with PKCE, state and nonce, kept in a 10-minute cookie; the ID token is verified. `return_to` is cleaned and checked, so it can only lead to a path on Scanopsis itself. | `internal/auth/gitlab.go`, `web/src/auth/returnTo.ts` |
| Group allow-list | With allowed groups set, users in none of them are refused at sign-in. | `internal/auth/gitlab.go` |
| New GitLab users are viewers | Nobody becomes an administrator automatically. | `internal/store/queries/users.sql` |
| GitLab identity bound to the instance | A GitLab user is stored with the GitLab URL (the OpenID Connect issuer) and the user ID there. A sign-in through another GitLab instance never matches an existing account, even with the same ID. | `internal/auth/gitlab.go`, `cmd/scanopsis/admin.go` |
| The last administrator stays | The last enabled administrator can be neither demoted, disabled nor deleted, even by two changes at once. | `internal/store/store.go`, `internal/store/users.go` |
| Disable and delete users | Disabling refuses every sign-in and ends all sessions at once. A disabled local account is refused only after the right password, so a guesser learns nothing. Administrators cannot disable or delete themselves. | `internal/httpapi/api.go`, `internal/store/users.go`, `cmd/scanopsis/admin.go` |

### Secrets at rest

| Control | What it does | Where |
|---|---|---|
| AES-256-GCM, bound to the purpose | TOTP secrets and secret settings are encrypted with the key. The purpose (such as `totp:42` or `setting:sonar.token`) is authenticated, so a value copied to another row fails to decrypt. | `internal/secretbox/secretbox.go` |
| Secrets are never returned | The API never returns secret settings. The Settings page can only replace or clear them. | `internal/settings` |
| A new URL needs the secret again | When you change the GitLab or SonarQube URL, you must enter the secret sent there again, so a stored secret never goes to a new address by a single edit. | `internal/settings/settings.go` |
| File variants | The database URL, the key and the secrets can come from files, out of the environment and `docker inspect`. | `internal/config/config.go` |
| No secret values in scan data | A secret finding keeps the rule and the location, never the matched text. The stored original report is as Trivy wrote it, and Trivy masks the value there. | `internal/ingest/parse.go` |
| Clean logs | The log never contains passwords, codes, cookies, headers, query strings, the database URL, the key or secret settings. The one exception is the setup token. | `internal/httpapi/middleware.go` |

### Uploads from CI

| Control | What it does | Where |
|---|---|---|
| Token signature | The ID token must be signed with a key your GitLab publishes at `/oauth/discovery/keys`. | `internal/ingest/token.go` |
| Issuer and audience | `iss` must be the configured GitLab URL, and `aud` must contain the Scanopsis public URL. | `internal/ingest/token.go` |
| Lifetime and claims | Expired or not-yet-valid tokens are refused. Project, namespace, pipeline, job, ref, `ref_protected` and commit must be present and well formed. | `internal/ingest/token.go` |
| Allowed namespaces | When set, only projects in the listed groups, or below them, may upload. | `internal/ingest/ingest.go` |
| Protected refs only | When set, only pipelines on protected branches and tags may upload. | `internal/ingest/ingest.go` |
| Identity from the token | Project, job, ref and commit come from the token, never from the report. | `internal/ingest/ingest.go` |
| Bounded input | A report may be 64 MiB decompressed and hold 200,000 findings. | `internal/ingest/ingest.go`, `internal/ingest/parse.go` |
| Bounded work | 60 uploads at once per address, then one a second. Uploads are admitted by the memory they need, from a shared budget; at most a quarter of the database pool stores uploads at once. Each project may upload 100 reports at once, then one every 10 seconds, and each job at most 50 artifacts. See [sizing](deployment.md#sizing). | `internal/ingest/ingest.go`, `cmd/scanopsis/main.go` |
| No internals in answers | Answers never contain the GitLab URL or an upstream error. Both stay in the server log. | `internal/ingest/token.go`, `internal/ingest/ingest.go` |
| Declared SonarQube key needs SonarQube | An upload's `sonar_key` is set on the service only when a SonarQube analysis of that key, delivered by the signed webhook within 7 days, names the uploading GitLab project. It never replaces a key and never takes one from another service; until it is confirmed, it waits, shows on the Services page and blocks other keys. The decision is made on the locked service row, so a webhook link at the same moment cannot slip past it. An administrator's key always wins and cancels a waiting one. | `internal/ingest/store.go`, `internal/sonar/declared.go` |
| Report time | A report time in the future is replaced by the server time, so nobody can keep a scan "current" forever. | `internal/ingest/ingest.go` |
| Audited rejections, with a bound | Rejections go to the audit log with the reason. The ones anyone with a token from your GitLab can cause are recorded at most 10 at once per address, then one a minute, so they cannot flood the log. | `internal/ingest/ingest.go` |
| Warning when uploads are open | While no namespace is allowed, every project of the GitLab instance may upload, and Scanopsis logs a warning. | `internal/ingest/ingest.go` |

### SonarQube webhook

| Control | What it does | Where |
|---|---|---|
| HMAC signature | The body needs a valid HMAC-SHA256 in `X-Sonar-Webhook-HMAC-SHA256`, compared in constant time. | `internal/sonar/webhook.go` |
| Small payload | At most 1 MiB. | `internal/sonar/webhook.go` |
| One delivery, bounded redelivery | A task ID is processed once. A failed one may be redelivered for the same project only, a minute apart, at most five times. | `internal/store/queries/sonar.sql` |
| Values come from SonarQube | The webhook only says which project and task. The measures are read from SonarQube with the configured token. | `internal/sonar/webhook.go` |
| Link by key first | A project key set on a service decides. The `gitlabProjectId` scanner property can only link a service that has no key yet, and never moves a link. An analysis that arrived before its service existed is linked only if the pipeline it names is the pipeline of the service's first upload. A key the pipeline declared in its upload (`sonar_key`) is the only one the property can link. | `internal/sonar/webhook.go`, `internal/sonar/relink.go` |
| No redirects elsewhere | Calls to SonarQube refuse a redirect to another scheme or host, so the token stays put. | `internal/sonar/sonar.go` |

### Browser and transport

| Control | What it does | Where |
|---|---|---|
| Content-Security-Policy | `default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'`. No inline script or style runs. | `internal/httpapi/middleware.go` |
| Other headers | `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, `Cross-Origin-Opener-Policy: same-origin`. | `internal/httpapi/middleware.go` |
| HSTS | With an `https` public URL: `Strict-Transport-Security: max-age=63072000; includeSubDomains`. | `internal/httpapi/middleware.go` |
| Safe links | Only `http` and `https` links from reports become links; `javascript:` or `data:` stays plain text. External links use `rel="noopener noreferrer"`. | `web/src/format/index.ts` |
| Certificate checks | Certificates of GitLab and SonarQube are always checked; there is no switch to turn this off. Add a private CA with `SCANOPSIS_CA_FILE`. | `cmd/scanopsis/main.go` |
| Client address | `X-Forwarded-For` is believed only from `SCANOPSIS_TRUSTED_PROXIES`, read from right to left. | `internal/clientip/clientip.go` |
| Request limits | API bodies up to 1 MiB (uploads have their own limit), headers within 10 seconds, plus read and write timeouts. | `internal/httpapi/middleware.go`, `cmd/scanopsis/main.go` |

### Backups

| Control | What it does |
|---|---|
| Administrators only | Every route under `/api/v1/admin/` (list, make, download, upload, delete and restore backups) needs an administrator with a completed two-factor check. Viewers and CI jobs cannot use them. The routes under `/api/v1/setup/restore` (the setup-screen restore, its uploads and its token check) need no session: they need the setup token, work only while no administrator exists, and are limited per client address. `/api/v1/restore/status` is public and returns only a fixed sentence per state. |
| Signed with a key from the secret key | A backup ends with an HMAC signature that covers its manifest and the checksum of every table. A restore refuses a file with a missing or wrong signature, so a file made or changed by someone without the secret key is not accepted. The fingerprint of the key in the manifest only gives the clearer message when a backup belongs to another key. |
| Not encrypted | A backup holds password hashes (Argon2id), the encrypted secrets, scan data and the audit log. Anyone who reads the file can read all but the encrypted secrets, which need `secrets/secret_key`. Keep backups and the key apart, and encrypt copies that leave the host. |
| Folder permissions | Each backup file has mode 600. The folder should have mode 700 and belong to user 65532, as the guides say; that is an operator step, because Scanopsis neither sets nor checks the permissions of the folder. |
| Restore needs a typed confirmation | An administrator types `RESTORE` and a current two-factor code. Too many wrong codes end the administrator's sessions. On the setup screen, where no account exists, the setup token stands in; checking it is limited per client address. |
| Restore signs everyone out | All sessions end. A restore refuses a backup of another secret key or of a newer version. |
| Audited | See the list below. |

The signature protects against changed or foreign files, not against an old
file of your own installation: anyone who can write into the `backups` folder
could put back an older backup of this installation, and an administrator
could then restore it. Protect the folder as you protect the database.

### Audit log

Sign-ins and failed sign-ins, role changes, disabled, enabled and deleted
users, ended sessions, two-factor events (including `mfa.locked`), setting
changes, service changes, rejected uploads, retention runs that deleted data,
backup events and every administration command go to the `audit_log` table, with actor,
action and a detail that never holds a secret. A `settings.changed` event
lists each setting's value before and after; for a secret it records only
that it changed. A failed sign-in for an unknown name is recorded as
`local:unknown`, because it may be a password typed into the wrong field.
The backup events are `backup.created`, `backup.failed`, `backup.downloaded`
(with the byte range served; for a request with several ranges, the span from
the first to the last byte), `backup.uploaded`, `backup.deleted`,
`backup.restored`, `backup.restore_failed`, `backup.restore_incomplete` and
`backup.schedule_alert_dismissed` (an administrator hid the banner about a
failed scheduled backup).
A scheduled backup has the actor `scheduler`; an upload on the setup screen
has the actor `setup`. A change of the backup schedule is a `settings.changed`
event. Administrators read it on the **Audit log** page. Writing an event never
blocks the action; a failure is logged instead. [Data retention](operations.md#data-retention)
removes events after 365 days at the earliest.

### The container

The image holds only the static binary (a distroless base: no shell, no
package manager) and runs as user 65532. The Compose file drops all
capabilities, sets `no-new-privileges`, mounts the root file system
read-only, publishes the port on the loopback interface only, and puts the
database on a network with no route out.

## Known limits and accepted risks

Each is accepted for the reason given, and each has a way to soften it.

1. **One instance only.** Rate limits, the setup token and the settings live
   in memory per process. A second server would have its own limits and miss
   setting changes until it restarts. Run one server.

2. **The first SonarQube project to name an unlinked service links it.** A
   service without a SonarQube key is linked to the first analysis that
   carries `sonar.analysis.gitlabProjectId` with its GitLab project ID. Anyone
   who can run an analysis in your SonarQube can claim such a service. An
   analysis that arrives before the service exists is linked automatically
   only if the pipeline it names (`sonar.analysis.pipelineId`) is the
   pipeline of the first upload, and it arrived within 7 days. The first
   upload's CI token proves which pipeline uploaded; nothing proves which
   pipeline an analysis came from, since the analysis states it itself.
   Analyses naming other pipelines are never linked this way, and if
   analyses of two SonarQube projects name that pipeline, none is linked.
   Pipeline IDs are predictable, so this narrows the risk but does not close
   it: someone who can run analyses could plant claims for upcoming IDs and
   at worst force the manual-key path, or link a project to a service that
   would otherwise stay without a key. Every link is in the audit log
   (`service.updated`, actor `sonarqube`), and an administrator can change
   it. To rule this out entirely, create each service with its SonarQube key
   before its first upload, or let the pipeline declare the key in every
   upload, from the first one on (`sonar_key`, see
   [link the SonarQube project](integrations/gitlab-ci.md#link-the-sonarqube-project)),
   with `-Dsonar.analysis.gitlabProjectId="$CI_PROJECT_ID"` on the scanner.
   Then only an analysis of the declared key can link the service by itself, and only
   once SonarQube delivers one that names the project. What is left is
   SonarQube's own boundary: a user with the Execute Analysis permission on
   that key can run such an analysis, and nobody without it can. An
   administrator who sets or clears the key cancels a waiting declaration
   (on the Services page: a new key, or **Cancel waiting key**; a new name
   alone keeps it); the project's next upload declares the key again.
   **Cancel waiting key** never unlinks: a key SonarQube confirmed just
   before stays linked.

3. **An `http` SonarQube URL is allowed.** Internal SonarQube servers often
   have no TLS, but then the token and the results cross the network in clear
   text. Scanopsis logs a warning. Use `https` unless the network is fully
   trusted.

4. **The setup token is in the log until setup is done.** While no
   administrator exists, anyone who can read the log can create one. Finish the
   setup right after the first start and limit who reads container logs. The
   token lives only in memory, changes at every start and dies with the setup.

5. **A signed SonarQube webhook can be replayed after retention.** Once
   [retention](operations.md#data-retention) deletes the delivery record, a
   captured, validly signed body could be sent again. It can only add a
   historical analysis point with measures read from SonarQube at that time,
   never change the current state. Capturing a signed body needs access to
   SonarQube's delivery log or the TLS path between the servers.

6. **Repeated failures can lock a user out for a while.** After repeated
   wrong passwords, local sign-in for that account is refused for a short time,
   even with the right password, so the limit never confirms a guess. Someone
   who knows a user name can therefore make that user wait. Ten wrong
   two-factor codes lock the second factor until an administrator resets it;
   that needs the first factor. GitLab sign-in and the
   [administration commands](operations.md#administration-commands) keep
   working.

7. **Anyone who can run a job on your GitLab gets a token Scanopsis
   accepts.** The token proves the job, not the intent. Restrict uploads with
   allowed namespaces and, if you can, protected refs only.

8. **The database plus the key reveal every secret.** There is one key and
   no rotation. Keep the two apart, see [the secret key](configuration.md#the-secret-key).

9. **The original report is kept for a while.** It holds whatever Trivy
   wrote, such as file paths and package names (Trivy masks secret values).
   Retention empties it 30 days after the scan stops being current.
   `scanopsis admin export-report` writes it out, and each export is audited.

10. **Until an administrator chooses a dashboard branch, the latest scanned
    branch is shown.** Anyone who can upload from any branch can then change
    what a dashboard and the portfolio show. Choose the branch of every service
    after its first upload, and use protected refs only to keep other branches
    from uploading at all. Changes of the branch are audited.

## Hardening checklist

Go through this when you deploy, and again after each upgrade.

1. **End TLS at a reverse proxy and use an `https` public URL.** That makes
   the cookie `Secure` with the `__Host-` prefix and turns on HSTS. See
   [reverse proxy and TLS](deployment.md#reverse-proxy-and-tls).
2. **Set `SCANOPSIS_TRUSTED_PROXIES` to your proxy's address and nothing
   wider.** Without it, all users share one address and one rate limit. With a
   range that is too wide, anyone in it can choose their address.
3. **Lock down the secret files.** `deploy/compose/secrets` is mode 700 and
   its files mode 644 (the container user must read them). Keep the
   directory, `.env` and backups out of version control and readable only by
   operators.
4. **Back up the secret key apart from the backup files.** A backup without
   the key cannot be restored; a key next to the backup protects nothing.
5. **Turn on scheduled backups, copy the `backups` folder off the host and
   encrypt the copies**, with a key kept apart from both. See
   [backup and restore](operations.md#backup-and-restore) and
   [encrypt your backups](operations.md#encrypt-your-backups). Keep the
   `backups` folder writable only by Scanopsis and the operators.
6. **After every restore, check users and the audit log.** A restore on the
   Backups page signs everyone out, but it brings back the users as they were.
   After a `pg_restore`, also delete all sessions. See
   [check everything after a restore](operations.md#check-everything-after-a-restore).
7. **Restrict network paths.** Publish the port on loopback only (the Compose
   file does) and let only the proxy reach it. Allow the database port only
   from Scanopsis, and outgoing connections only to GitLab and SonarQube.
8. **Add rate limits at the proxy and lock the SonarQube webhook to
   SonarQube's address (optional).** You can also hide `/api/v1/version`,
   which tells anyone your version. See
   [add rate limits and lock down the webhook](deployment.md#add-rate-limits-and-lock-down-the-webhook-optional).
9. **Limit who may upload.** Set the allowed namespaces; consider protected
   refs only.
10. **Link every service to its SonarQube project by key**, so no analysis
    can claim it.
11. **Use `https` for SonarQube.**
12. **Rotate tokens.** Replace the SonarQube token and webhook secret on a
    schedule, and the GitLab application secret when someone with access
    leaves. Change them on the Settings page; they apply to the next request.
13. **Turn off local sign-in only once GitLab sign-in works**, and keep one
    local administrator you can rescue from the command line. See
    [recovering access](operations.md#recovering-access-when-locked-out).
14. **Set allowed groups** for GitLab sign-in. Empty lets every GitLab user in.
15. **Make a GitLab user an administrator only after they enrolled
    two-factor authentication.** Until then, whoever first signs in as that
    user chooses the authenticator. Check it on the **Users** page.
16. **Plan before you change the GitLab URL.** Every GitLab user then starts
    over as a new viewer, and the old accounts stay behind. If only the address
    of the same GitLab changed, move the accounts first, see
    [GitLab users and the GitLab URL](operations.md#gitlab-users-and-the-gitlab-url).
17. **Read the audit log.** Look for failed sign-ins, `ingest.rejected`, role
    changes, `settings.changed` and `service.updated` by `sonarqube`.
18. **Keep the setup token private** and finish the setup at once.
19. **Limit who reads container logs.** They hold client addresses, project
    paths and, before setup, the setup token.
20. **Keep the resource and log limits** of `compose.yaml` (memory with
    `GOMEMLIMIT`, `pids_limit`, log rotation), and set the same with
    `docker run` or Kubernetes. See [host settings](deployment.md#host-settings).
21. **Alert on the database disk at 70% and 85%.** A full disk stops the
    database. See [watch the database disk](operations.md#watch-the-database-disk).
22. **Stay current.** Build the newest version branch, see
    [upgrade to the newest version](versions.md#upgrade-to-the-newest-version).
    Never deploy `main`.

## Report a vulnerability

Do not report vulnerabilities in public issues. Follow the
[security policy](../SECURITY.md): use GitHub private vulnerability reporting
or send an e-mail, with the affected version, the steps to reproduce and the
impact you expect.

---

[Back to the README](../README.md) · [All guides](../README.md#documentation) · Found a mistake? [Open an issue](https://github.com/siyamsarker/scanopsis/issues)
