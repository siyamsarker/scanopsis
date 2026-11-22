# Security

This page describes what Scanopsis protects, who it protects it from, how, and
what it does not protect against. It is written for the person who deploys
Scanopsis and has to decide whether it is safe to run in their network. To
report a vulnerability, see [how to report a vulnerability](#how-to-report-a-vulnerability).

For how the parts fit together, see the [architecture](architecture.md).

## Threat model

### Assets

| Asset | Why it matters | Where it lives |
|---|---|---|
| Scan data | A list of the known vulnerabilities of every service shows an attacker where to strike. Treat it as sensitive. | `scans`, `vulnerabilities`, `secret_findings`, `sonar_analyses` tables |
| Credentials | The GitLab sign-in application secret, the SonarQube token and the webhook secret. | `settings` table, encrypted |
| Sessions | A stolen session is a signed-in user. | `sessions` table (hashed), browser cookie |
| Two-factor secrets and recovery codes | They are the second factor. | `users` table (encrypted), `recovery_codes` table (hashed) |
| Password hashes | Offline guessing of local passwords. | `users` table (Argon2id) |
| `SCANOPSIS_SECRET_KEY` | Decrypts every stored secret. | Environment or a mounted secret file |
| The setup token | Creates the first administrator. | Server log, until the setup completes |
| The audit log | Evidence of what happened. | `audit_log` table |

### Actors

| Actor | Trust | What they can do |
|---|---|---|
| Viewer | Signed in, two-factor verified | Read scan data and SonarQube results. |
| Administrator | Signed in, two-factor verified | Everything a viewer can, and change settings, users, services, and read the audit log. |
| CI job | Anyone who can run a job on your GitLab | Upload reports for its own project, if the project is allowed. |
| SonarQube | Your SonarQube server | Send signed webhooks. Scanopsis calls it with a read-only token. |
| Network attacker | None | Reach the public address, read or alter traffic that is not protected by TLS, send forged requests. |
| Anyone with access to the database or a backup | Fully trusted by design | Reads everything that is not encrypted. Cannot read the encrypted secrets without the key. |
| Anyone with access to the host, the container or the log | Fully trusted by design | Reads the key, the setup token and the database URL. |

Scanopsis does not defend against an attacker who controls the host, the
database and the key at the same time.

### Trust boundaries

The boundaries are listed in the [architecture](architecture.md#trust-boundaries).
In short: TLS ends at your reverse proxy; the proxy and Scanopsis share a
private network; Scanopsis reaches PostgreSQL over a private network; GitLab
and SonarQube are trusted to say who a user or a job is, and nothing more.

## Controls

Each row names the control and where it is implemented, so that you can check
it in the code.

### Sessions and sign-in

| Control | What it does | Where |
|---|---|---|
| Session tokens are hashed | The browser holds 32 random bytes. The database stores only their SHA-256, so a database leak does not give out sessions. | `internal/auth/sessions.go` |
| Cookie attributes | The cookie is `HttpOnly` and `SameSite=Lax`. With an `https` public URL it is also `Secure` and named with the `__Host-` prefix, which makes browsers refuse a cookie with a domain or a path other than `/`. | `internal/auth/sessions.go` |
| Session lifetime | A session ends after the idle timeout (default 8 hours) or the absolute timeout (default 24 hours). A new sign-in replaces the old session. | `internal/auth/sessions.go`, [configuration](configuration.md#runtime-settings) |
| Cross-site request protection | State-changing requests from a browser must be same-origin. The server checks `Sec-Fetch-Site` and `Origin` with the standard library's cross-origin protection and answers `403 cross_origin` otherwise. Requests without those headers (curl, CI jobs) are not browser requests and pass. | `internal/httpapi/httpapi.go` |
| Mandatory two-factor authentication | Every user, local or GitLab, must enrol a TOTP app. A session that has not passed the second factor can call only the sign-in and enrolment endpoints. | `internal/httpapi/middleware.go`, `internal/auth/mfa.go` |
| New session token after the second factor | When a correct code is accepted, the session gets a new random token and the old one stops working. A token captured between the password step and the code step never becomes a full session. | `internal/auth/sessions.go` |
| Lock after ten wrong codes | Ten wrong codes in a row lock the second factor of that user. Every code, right or wrong, is then refused with `423 mfa_locked`, and all sessions of the user are ended. The lock stays until an administrator resets two-factor authentication or the password (**Reset two-factor** on the Users page, or the administration command). A correct code sets the count back to zero. | `internal/auth/mfa.go`, `internal/store/migrations/00005_mfa_lockout.sql` |
| Code replay protection | Each accepted TOTP time step is recorded, and a code for the same or an older step is refused. A code is accepted one step (30 seconds) before or after the current one. | `internal/auth/mfa.go` |
| Recovery codes | Ten single-use codes, stored as SHA-256 hashes, shown once. | `internal/auth/mfa.go` |
| Rate limits | Local sign-in, setup, two-factor codes, password changes and uploads are limited per client address or per user. See [rate limits](configuration.md#rate-limits). Once the failure budget of a user name is spent, every password for it is refused until the budget refills, the correct one included, so the limit cannot be used to test guesses. The limit tables are bounded in memory. | `internal/auth/ratelimit.go`, `internal/auth/local.go` |
| Argon2id passwords | Argon2id with 64 MiB, 3 passes and 4 lanes. At most two hashes are computed at once, so a flood cannot exhaust memory. An unknown user costs the same time as a known one. Passwords must have at least 12 characters. | `internal/auth/password.go`, `internal/auth/policy.go` |
| GitLab sign-in | Authorization code flow with PKCE, a state value and a nonce, kept in a 10-minute cookie. The ID token is verified. `return_to` accepts only local paths. | `internal/auth/gitlab.go` |
| Group allow-list | When allowed groups are set, the groups are read from GitLab during sign-in. Users in none of them are refused. | `internal/auth/gitlab.go` |
| New GitLab users are viewers | Nobody becomes an administrator automatically. | `internal/store/queries/users.sql` |
| The last administrator cannot be demoted | The check holds row locks, so two concurrent demotions cannot both succeed. | `internal/store/store.go` |

### Secrets at rest

| Control | What it does | Where |
|---|---|---|
| AES-256-GCM, bound to the purpose | TOTP secrets and the secret settings are encrypted with the key. The purpose (for example `totp:42` or `setting:sonar.token`) is authenticated, so a ciphertext copied to another row or setting fails to decrypt. | `internal/secretbox/secretbox.go` |
| Secrets are never returned | The API does not return secret settings. The Settings page can only replace or reset them. | `internal/settings` |
| A new URL needs the secret again | When you change the GitLab URL or the SonarQube URL on the Settings page, you must enter the secret that is sent there (the application secret, or the token) again. A stored secret is never sent to a new address by a single edit of the URL. | `internal/settings/settings.go` |
| File variants | The database URL, the key and the secrets can be read from files, so they stay out of the environment and out of `docker inspect`. | `internal/config/config.go` |
| No secret values in scan data | A secret finding stores the rule and the location. The matched text is never read from the report or stored. The compressed original is kept as Trivy wrote it, and Trivy masks the value in it. | `internal/ingest/parse.go` |
| Logs | The log never contains passwords, codes, cookies, headers, query strings, the database URL, the key or secret settings. The one exception is the setup token. | `internal/httpapi/middleware.go` |

### Uploads from CI

| Control | What it does | Where |
|---|---|---|
| Token signature | The ID token must be signed with a key that your GitLab publishes at `/oauth/discovery/keys`. | `internal/ingest/token.go` |
| Issuer and audience | `iss` must be the configured GitLab URL, and `aud` must contain the Scanopsis public URL. A token made for another service is refused. | `internal/ingest/token.go` |
| Lifetime | Expired tokens and tokens that are not valid yet are refused. | `internal/ingest/token.go` |
| Required claims | Project ID and path, namespace path, pipeline, job, ref, `ref_protected` and a commit hash must be present and well formed. | `internal/ingest/token.go` |
| Allowed namespaces | When set, only projects in the listed groups, or below them, may upload. | `internal/ingest/ingest.go` |
| Protected refs only | When set, only pipelines on protected branches and tags may upload. | `internal/ingest/ingest.go` |
| Identity from the token | The project, job, ref and commit come from the token, never from the report. | `internal/ingest/ingest.go` |
| Bounded input | A report may be 64 MiB decompressed and hold 200,000 findings. The body and the decompressed stream are limited separately. | `internal/ingest/ingest.go`, `internal/ingest/parse.go` |
| Rate limit | 60 uploads at once per client address, then one per second. | `cmd/scanopsis/main.go` |
| Bounded work | At most four uploads are read and stored at the same time, all addresses together, because each can hold a whole report in memory. Others wait while their request lasts, then get `503 busy`. | `internal/ingest/ingest.go` |
| Messages without internals | The answers to the caller never contain the GitLab URL or the text of an upstream error. Both stay in the server log. | `internal/ingest/token.go`, `internal/ingest/ingest.go` |
| Report time | A report time in the future is replaced by the server time, so an uploader cannot keep a scan "current" forever. | `internal/ingest/ingest.go` |
| Rejections are audited, with a bound | A rejected upload is written to the audit log with the reason. Rejections that anyone with an ID token from your GitLab can cause (before the project has passed the allowed-namespaces and protected-ref checks, and always while no namespace is allowed) are recorded at most 10 at once per client address and then one a minute, so they cannot fill the log. Rate-limited requests are not recorded. A rejection of a project that passed the allow-list is always recorded. | `internal/ingest/ingest.go` |
| Warning when uploads are open | While GitLab is configured and no namespace is allowed, every project of the GitLab instance may upload. Scanopsis logs a warning once for each time the settings enter that state. | `internal/ingest/ingest.go` |

### SonarQube webhook

| Control | What it does | Where |
|---|---|---|
| HMAC signature | The body must carry a valid HMAC-SHA256 in `X-Sonar-Webhook-HMAC-SHA256`, compared in constant time. | `internal/sonar/webhook.go` |
| Small payload | The body is limited to 1 MiB. | `internal/sonar/webhook.go` |
| Deduplication and bounded redelivery | A task ID is processed once. A failed delivery may be redelivered only for the same project, at least one minute after the last attempt, and at most five times in all. | `internal/store/queries/sonar.sql` |
| Values come from SonarQube | The webhook says which project and task. The measures are read from the SonarQube Web API with the configured token. | `internal/sonar/webhook.go` |
| Link by key first | A project key linked to a service decides. The `gitlabProjectId` scanner property can only link a service that has no SonarQube key yet, and never moves a link. | `internal/sonar/webhook.go` |
| No redirects to other hosts | Web API calls refuse a redirect to another scheme or host, so the token cannot be sent elsewhere. | `internal/sonar/sonar.go` |

### Browser and transport

| Control | What it does | Where |
|---|---|---|
| Content-Security-Policy | `default-src 'self'` with `script-src 'self'` and `style-src 'self'`, so no inline script or style runs. `object-src 'none'`, `base-uri 'none'`, `form-action 'self'`, `frame-ancestors 'none'`. | `internal/httpapi/middleware.go` |
| Other headers | `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, `Cross-Origin-Opener-Policy: same-origin`. | `internal/httpapi/middleware.go` |
| HSTS | With an `https` public URL: `Strict-Transport-Security: max-age=63072000; includeSubDomains`. | `internal/httpapi/middleware.go` |
| Link scheme filtering | The web interface turns a link into an anchor only if it uses `http` or `https`. A `javascript:` or `data:` URL from a report is shown as plain text. External links use `rel="noopener noreferrer"`. | `web/src/format/index.ts` |
| Certificate checks | Scanopsis verifies the certificate of GitLab and SonarQube. There is no option to turn this off. An internal CA is added with `SCANOPSIS_CA_FILE`. | `cmd/scanopsis/main.go` |
| Client address | `X-Forwarded-For` is believed only from `SCANOPSIS_TRUSTED_PROXIES`, read from right to left. | `internal/clientip/clientip.go` |
| Request limits | JSON bodies are limited to 1 MiB (uploads have their own limit), headers must arrive within 10 seconds, and requests have read and write timeouts. | `internal/httpapi/middleware.go`, `cmd/scanopsis/main.go` |

### Audit log

Sign-ins, failed sign-ins, role changes, two-factor events (including
`mfa.locked`), setting changes, service changes, rejected uploads, every
retention run that deleted data (`retention.deleted`), and the administration
commands are written to the `audit_log` table, with the actor, the action and a detail that never
contains a secret. A failed sign-in for a user name that matches no account is
recorded as `local:unknown`, because it may be a password typed into the wrong
field. Administrators read it on the **Audit** page. Writing an
event never blocks the action: a failure is logged instead. Old events are
removed by [data retention](operations.md#data-retention) after at least 365 days.

### The container

The image holds only the static binary (a distroless base, no shell, no
package manager) and runs as user 65532. The Compose file drops all
capabilities, sets `no-new-privileges`, mounts the root file system read-only,
publishes the port on the loopback interface only, and puts the database on a
network with no route out.

## Known limitations and accepted risks

These are known. They are accepted for the reasons given, and each has a
mitigation.

1. **One instance only.** Rate limits, the setup token and the settings
   snapshot live in memory. A second server would have its own limits and would
   not see setting changes until it restarts. Run one server. See
   [single instance](architecture.md#single-instance).

2. **The first SonarQube project to name an unlinked service links it.** A
   service without a SonarQube key is linked to the project of the first
   analysis that carries `sonar.analysis.gitlabProjectId` with that service's
   GitLab project ID. Anyone who can run an analysis in your SonarQube can
   claim an unlinked service this way. The link is recorded in the audit log
   (action `service.updated`, actor `sonarqube`, with the task ID) and an
   administrator can change or remove it on the Services page. Set the key on
   each service yourself to avoid it.

3. **An `http` SonarQube URL is allowed.** Internal SonarQube servers often
   have no TLS. The token and the results then cross the network in clear text.
   Scanopsis logs a warning when it starts with, or is changed to, such a URL.
   Use `https` unless the network between the two servers is fully trusted.

4. **The setup token is written to the log until the setup completes.** While
   no administrator exists, anyone who can read the log can create the first
   administrator. Complete the setup right after the first start, and restrict
   who reads the container logs. The token exists only in memory, changes at
   every start, and stops working when the setup completes.

5. **A replayed SonarQube webhook after retention.** When
   [retention](operations.md#data-retention) deletes a delivery record, a
   captured, validly signed webhook body could be replayed. It can only add a
   historical analysis point, with the measures read from SonarQube at that
   time. It can never change the current state, which is the latest analysis by
   analysis date. To capture a signed body, an attacker needs access to the
   delivery log of SonarQube or to the TLS path between the two servers.

6. **Local sign-in and two-factor sign-in can be locked by repeated failures.**
   After repeated failed passwords for an account, local sign-in for that
   account is refused for a short time, even with the correct password. This
   prevents the rate limit from revealing a correct guess. An attacker who
   knows a user name can therefore make that user wait. Ten wrong two-factor
   codes in a row lock the second factor until an administrator resets it; an
   attacker needs the first factor to cause this. GitLab sign-in and the
   [administration commands](operations.md#administration-commands) remain
   available while a local sign-in limit applies.

7. **Anyone who can run a job on your GitLab can obtain a token that Scanopsis
   accepts.** The token proves the job, not the intent. Restrict uploads with
   allowed namespaces and, if you can, protected refs only.

8. **Anyone who can read the database and the key reads every secret.** There is
   one key, with no rotation. Protect the two separately. See
   [the secret key](configuration.md#the-secret-key).

9. **The raw report is kept as uploaded for a while.** It can contain whatever
   Trivy wrote, such as file paths and package names. Trivy masks secret values
   in it. Retention clears it 30 days after the scan stops being current, and
   `scanopsis admin export-report` writes it out for checks; each export is
   recorded in the audit log. See [original reports](operations.md#original-reports).

10. **Until an administrator chooses a branch, the latest scanned branch is
    shown.** A dashboard shows one branch of a service. An administrator
    chooses it for each service on the Services page, and no scan of another
    branch can change it. For a service where none is chosen, the dashboard
    shows the branch with the most recent scan, so anyone who can upload a
    scan from any branch can change what is shown (and with it the numbers on
    the portfolio) until a branch is chosen. Choose the branch of every
    service after its first upload, and use protected refs only to keep
    unprotected branches from uploading at all. A change of the branch is
    recorded in the audit log (`service.updated`, with the previous and the new
    value).

## Hardening checklist

Go through this list when you deploy, and again after each upgrade.

1. **Terminate TLS at a reverse proxy and use an `https` public URL.** Then the
   cookie is `Secure`, the `__Host-` prefix applies and HSTS is sent. See
   [deployment](deployment.md#reverse-proxy-and-tls).
2. **Set `SCANOPSIS_TRUSTED_PROXIES` to the address of your proxy, and nothing
   wider.** Without it, all users share the proxy's address and one rate limit.
   With a range that is too wide, anyone in it can choose the client address.
3. **Set file permissions on the secrets.** `deploy/compose/secrets` is mode 700
   and the files are mode 644 (the container user must read them). Keep the
   directory, `.env` and backups out of version control and readable only by the
   operators.
4. **Back up `SCANOPSIS_SECRET_KEY` separately from the database.** A backup
   without the key is useless, and a key next to the backup protects nothing.
5. **Encrypt backups at rest.** Keep the key apart from `SCANOPSIS_SECRET_KEY` and from
   the backups. See [encrypt backups at rest](operations.md#encrypt-backups-at-rest).
6. **After every restore, delete all sessions and check the users and the audit
   log.** A restore brings back the sessions of the backup. See
   [after a restore](operations.md#after-a-restore).
7. **Restrict network paths.** Publish the port on the loopback interface only
   (the Compose file does). Let only the reverse proxy reach it. Allow the
   database port only from Scanopsis. Allow outgoing connections only to GitLab
   and SonarQube.
8. **Limit rates at the proxy, and restrict the SonarQube webhook (optional).**
   Rate limits for sign-in, second factor and uploads add a layer in front of the
   limits in Scanopsis. Let only the address of SonarQube call
   `/api/v1/hooks/sonarqube`. You can also keep `/api/v1/version` (it shows the
   version you run, to anyone) to your own networks. See
   [limit rates and restrict the webhook](deployment.md#limit-rates-and-restrict-the-webhook-optional).
9. **Limit who may upload.** Set the allowed namespaces. Consider protected refs
   only.
10. **Link services by key.** Set the SonarQube key on each service so that no
    analysis can claim it.
11. **Use `https` for SonarQube.**
12. **Rotate tokens.** Replace the SonarQube token and the webhook secret on a
    schedule, and the GitLab application secret when someone with access leaves.
    Change them on the Settings page. They apply to the next request.
13. **Turn off local sign-in only when GitLab sign-in works**, and keep one
    local administrator you can recover with the command line. Scanopsis refuses
    to turn local sign-in off while GitLab sign-in is incomplete. See
    [recovering access](operations.md#recovering-access-when-locked-out).
14. **Set allowed groups** for GitLab sign-in. Empty allows every GitLab user.
15. **Promote a GitLab user to administrator only after the user has enrolled
    two-factor authentication.** Until then, whoever first signs in as that
    GitLab user chooses the authenticator. Check the user on the **Users** page.
16. **Never point `gitlab.url` at another GitLab instance while GitLab users
    exist.** Scanopsis identifies a GitLab user by the numeric user id, not by the
    instance. Another instance can give the same id to a different person, who
    then signs in as the existing user, administrators included. Remove the
    GitLab users first, or start with a new database.
17. **Read the audit log.** Look for failed sign-ins, `ingest.rejected`, role
    changes, `settings.changed` and `service.updated` by `sonarqube`.
18. **Keep the setup token private** and finish the setup at once.
19. **Limit who reads the container logs.** They hold client addresses, project
    paths and, before the setup, the setup token.
20. **Keep the resource and log limits.** `compose.yaml` sets a memory limit with
    `GOMEMLIMIT`, process limits (`pids_limit`) and log rotation for both
    services. Keep them when you edit the file, and set the same limits with
    `docker run` or in Kubernetes. See [host settings](deployment.md#host-settings).
21. **Alert on the disk of the database at 70% and 85%.** A full disk stops the
    database. See [watch the disk of the database](operations.md#watch-the-disk-of-the-database).
22. **Update.** Build the newest version branch and apply it. See
    [versions and branches](versions.md#upgrade-to-a-newer-version) and
    [upgrades](deployment.md#upgrades-and-rollback). Never deploy `main`.

## How to report a vulnerability

Do not report vulnerabilities in public issues. Follow the
[security policy](../SECURITY.md): use GitHub private vulnerability reporting
or send an e-mail, and include the affected version, the steps to reproduce and
the impact you expect.
