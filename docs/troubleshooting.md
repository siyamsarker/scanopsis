# Troubleshooting

Something is wrong and you want it fixed. Find what you see in the tables
below, read the cause, and apply the fix.

**Audience:** Operators · **Back to:** [README](../README.md) · [All guides](../README.md#documentation)

---

Start with the log. Run `docker compose` commands in `deploy/compose`:

```sh
docker compose logs --since 10m scanopsis
```

Messages are quoted exactly as Scanopsis prints them. `...` stands for text
that depends on your installation. The tables are grouped by where you see the
problem.

## Start-up

The container restarts in a loop, or `docker compose ps` shows `Restarting`.
Read the first lines of the log.

### Configuration errors

Scanopsis checks its settings first. It prints `configuration error:`, then
one line per problem, and exits with status 2. Fix every line, then run
`docker compose up -d`.

| Message | Cause | Fix |
|---|---|---|
| `SCANOPSIS_PUBLIC_URL is required, for example https://scanopsis.example.com` | The variable is missing or empty. | Set it in `.env`. |
| `SCANOPSIS_PUBLIC_URL must be an absolute http or https URL, got "..."` | No scheme or host, or a scheme other than `http` or `https`. | Write the full address, such as `https://scanopsis.example.com`. |
| `SCANOPSIS_PUBLIC_URL must not contain a path, query, fragment or credentials, got "..."` | The address has more than scheme, host and port. | Remove the rest. A trailing slash is fine. |
| `SCANOPSIS_DATABASE_URL is required` | No database URL, or an empty one. | In Compose, check that `secrets/database_url` exists and is not empty. |
| `SCANOPSIS_DATABASE_URL and SCANOPSIS_DATABASE_URL_FILE are both set; set only one` | Both forms are set. The same message exists for the key and the other secrets. | Remove one. |
| `SCANOPSIS_DATABASE_URL_FILE cannot be read: open ...: no such file or directory` | The file is not in the container. The same for other `_FILE` variables. | Check the path and the `secrets:` entry in `compose.yaml`. |
| `SCANOPSIS_SECRET_KEY is required; generate one with: openssl rand -base64 32` | No key. | Create `secrets/secret_key`, see [step 2 of the getting started guide](getting-started.md#step-2-create-the-secrets). |
| `SCANOPSIS_SECRET_KEY is not valid (key must be 32 bytes, got 3); ...` or `(key is not valid base64)` | The key is not base64, or not 32 bytes. | Make a new key if no data depends on the old one. Otherwise see [the secret key](configuration.md#the-secret-key). |
| `SCANOPSIS_LOG_LEVEL must be debug, info, warn or error, got "..."` | Unknown level. | Use one of the four. |
| `SCANOPSIS_TRUSTED_PROXIES contains "...", which is not an IP address or CIDR range` | An entry is neither an address (`172.30.0.10`) nor a range (`10.0.0.0/8`). | Fix the entry. Separate entries with commas. |
| `SCANOPSIS_LOCAL_LOGIN_ENABLED must be true or false, got "yes"` | Also for `SCANOPSIS_INGEST_PROTECTED_REFS_ONLY`. | Use `true` or `false`. |
| `SCANOPSIS_SESSION_IDLE_TIMEOUT must be a duration such as 30m or 8h, got "5"` | A number without a unit. Also for the absolute timeout and `SCANOPSIS_SONAR_SYNC_INTERVAL`. | Add a unit: `30m`, `8h`, `1h30m`. |
| `SCANOPSIS_SESSION_IDLE_TIMEOUT must be at least 5m` | Too short. | Use `5m` or more. |
| `SCANOPSIS_SESSION_ABSOLUTE_TIMEOUT must not be shorter than SCANOPSIS_SESSION_IDLE_TIMEOUT` | The two contradict each other. | Make the absolute timeout at least as long. |
| `SCANOPSIS_RETENTION_DAYS must be a whole number from 7 to 3650, got "..."` | Not a whole number, or out of range. | Use 7 to 3650. |
| `SCANOPSIS_GITLAB_URL is required when other GitLab settings are set` | A client ID, secret or groups without the GitLab URL. | Set the URL, or remove the others. |
| `SCANOPSIS_GITLAB_CLIENT_SECRET is required when SCANOPSIS_GITLAB_CLIENT_ID is set` (or the other way round) | Only one half of the GitLab application. | Set both or neither. |
| `SCANOPSIS_SONAR_URL and SCANOPSIS_SONAR_TOKEN must be set together (the webhook secret needs both)` | Only one of URL and token, or a webhook secret without them. | Set URL and token together. |
| `SCANOPSIS_GITLAB_URL must be an absolute http or https URL, got "..."` | Same rules as the public URL. Also for `SCANOPSIS_SONAR_URL`. | Fix the address. |

### Errors after the configuration was accepted

These show up as `scanopsis stopped with an error`, with the cause in the
`error` field.

| Message | Cause | Fix |
|---|---|---|
| `read SCANOPSIS_CA_FILE: open ...: no such file or directory` | The CA file is not mounted at that path. | Mount it, see [trust an internal certificate authority](deployment.md#trust-an-internal-certificate-authority). |
| `SCANOPSIS_CA_FILE ... contains no PEM certificates` | The file is empty or not PEM. | Put the CA certificate in PEM format in it. |
| `parse database URL: ... invalid percent-encoded token ...` | A special character in the password is not encoded. | [Encode the password](deployment.md#step-2-write-the-database-url). |
| `connect to database: ... connection refused` (or `no such host`, `password authentication failed`) | The database is down, unreachable, or the password differs. | `docker compose ps db`. Compare `secrets/database_url` with `secrets/postgres_password`. PostgreSQL reads the password only when `data/postgres` is first filled. |
| `connect to database: ... x509: certificate signed by unknown authority` | Your own PostgreSQL uses a private CA and the URL has `sslmode=verify-full`. `SCANOPSIS_CA_FILE` does not cover the database. | Add `sslrootcert=` to the URL and mount the file, see [step 2](deployment.md#step-2-write-the-database-url). |
| `read migration status: ... permission denied for schema public (SQLSTATE 42501)` | The database role cannot create tables. | Make the role the database owner, or run `GRANT CREATE ON SCHEMA public TO scanopsis;`, see [step 1](deployment.md#step-1-create-the-role-and-the-database). |
| `apply migration <file>: ...` | A schema change failed, for example because it could not lock a table within 10 seconds. | Read the database error. Start again; the next start continues where this one stopped. If it keeps failing, [roll back](deployment.md#roll-back). |
| `listen on :8080: ... address already in use` | Something else uses the port in the container, or `SCANOPSIS_LISTEN_ADDR` is wrong. | Check `SCANOPSIS_LISTEN_ADDR`. |
| `setting gitlab.client_secret cannot be decrypted: SCANOPSIS_SECRET_KEY changed or is wrong` | The saved secret was encrypted with another key. Also for `sonar.token` and `sonar.webhook_secret`. | Put the original key back, see [the secret key](configuration.md#the-secret-key). |
| `gitlab.allowed_groups from SCANOPSIS_GITLAB_ALLOWED_GROUPS: Group path "/platform" must not start or end with a slash.` | An invalid group path in the environment. Another rule: `must not contain spaces.` | Fix the variable. This check needs the database, so it shows up after the connection works. |
| `local sign-in is disabled but GitLab sign-in is not fully configured, so nobody could sign in: ...` | Local sign-in is off and GitLab sign-in is incomplete. The message names the fix. | Follow it, or [turn local sign-in on again](operations.md#turn-local-sign-in-on-again). |
| `setting ... has an unreadable stored value` or `... has an invalid stored value` | A row in the `settings` table was changed by hand. | Delete the row so the setting falls back to its default, or restore a backup. |

To see what the container sees:

```sh
docker compose config --quiet && echo config-ok
docker compose logs scanopsis | grep 'scanopsis starting' | tail -1
```

## Sign-in and two-factor

### First-run setup

| What you see | Cause | Fix |
|---|---|---|
| "The setup token is not correct. Copy it again from the server log." (`invalid_setup_token`) | The token is from an earlier start, or incomplete. Every start makes a new one. | `docker compose logs scanopsis \| grep setup_token` and use the last one. |
| "Setup is already complete. Sign in instead." (`setup_complete`) | An administrator exists. | Sign in. If you cannot, see [recovering access](operations.md#recovering-access-when-locked-out). |
| The log has no `setup_token` line | An administrator already exists. | Sign in. |
| "The password must be at least 12 characters long." (or another `weak_password` rule) | The password breaks a rule: at least 12 characters, at most 1024 bytes, not the user name, not one repeated character. | Choose another password. |

### Local sign-in

| What you see | Cause | Fix |
|---|---|---|
| "Wrong username or password." (`invalid_credentials`, 401) | Unknown user or wrong password; both get the same answer on purpose. GitLab users have no local password. | Check the spelling, or [reset the password](operations.md#reset-a-password). |
| "Too many sign-in attempts. Wait N seconds and try again." (`too_many_attempts`, 429) | A [rate limit](configuration.md#rate-limits). After repeated wrong passwords for one name, every password for it is refused for a while, the right one too. Behind a proxy without `SCANOPSIS_TRUSTED_PROXIES`, all users share one limit. | Wait the seconds in `Retry-After`. Set `SCANOPSIS_TRUSTED_PROXIES`. |
| "The server is busy. Try again in a moment." (`busy`, 503) | Many password checks at once. | Try again. |
| "Local sign-in is not enabled on this server." (`not_enabled`, 404) | Local sign-in is off. | Sign in with GitLab, or [turn it on again](operations.md#turn-local-sign-in-on-again). |
| "Sign-in failed because of a server problem. ..." (`internal_error`, 500) | A database or server error. | Read the log. |

### GitLab sign-in

After a failure, the browser opens `/login?error=<code>`.

| Code and message | Cause | Fix |
|---|---|---|
| `access_denied`: "You cancelled the sign-in on GitLab." | The user selected **Deny** on GitLab. | Try again. |
| `group_not_allowed`: "Your GitLab account is not in a group that may use Scanopsis. ..." | The user is in none of the allowed groups. | Add the group, with its exact upper and lower case, see the [GitLab sign-in guide](integrations/gitlab-sso.md). |
| `account_disabled`: "Your account is disabled. Ask an administrator to enable it." | An administrator disabled the account. | [Enable it](operations.md#disable-enable-or-delete-a-user). |
| `login_expired`: "The sign-in took too long or was started in another tab. Try again." | The sign-in cookie is missing or older than 10 minutes, or the browser used another host name than `SCANOPSIS_PUBLIC_URL`. | Start again under exactly the public URL. Check that the proxy passes `Set-Cookie`. |
| `login_failed`: "Sign-in with GitLab failed. ..." | Anything else: wrong client secret, GitLab unreachable, an untrusted certificate, a wrong clock. | Read the log line `GitLab sign-in failed` and its `error` field. |
| `gitlab_unavailable`, 503: "GitLab could not be reached. Try again in a few seconds, or sign in as the local administrator." | Scanopsis could not read GitLab's OpenID Connect configuration. The log line `GitLab discovery failed` says why. | Check DNS, the route and the certificate. For a private CA, see [trust an internal certificate authority](deployment.md#trust-an-internal-certificate-authority). Scanopsis tries again 10 seconds after a failure. |
| `not_enabled`, 404: "GitLab sign-in is not enabled on this server." | GitLab sign-in is not fully set up. | Set the GitLab URL, application ID and secret. `curl -s <public URL>/api/v1/auth/methods` must show `"gitlab":true`. |

### Two-factor codes and passwords

| What you see | Cause | Fix |
|---|---|---|
| "That code is not correct. Wait for a new one in the app and try again." (`invalid_code`, 401) | The code expired, was mistyped or already used, or a clock is wrong. A code is valid 30 seconds before and after the current time, once. | Wait for the next code. Check the time on the server and the phone. |
| "That recovery code is not correct, or it was already used." | Wrong or used code. Each works once. | Use another one. If none is left, ask an administrator to reset your two-factor authentication. |
| `too_many_attempts`, 429, on the code page | More than 5 code attempts at once for this user; after that, one every 30 seconds. | Wait. |
| `mfa_locked`, 423: "Too many wrong codes. Ask an administrator to reset two-factor authentication." | Ten wrong codes in a row. The second factor is locked and the user's sessions ended. The audit log has `mfa.locked`. | An administrator selects **Reset two-factor** in the user's actions menu on the **Users** page or runs [`reset-2fa`](operations.md#reset-two-factor-authentication). A password reset from the command line also lifts the lock. |
| Every code fails and the log says `cannot decrypt TOTP secret; check SCANOPSIS_SECRET_KEY` | The key is not the one that encrypted the secrets, for example after a restore. | Put the original key back, see [the secret key](configuration.md#the-secret-key). |
| `password_change_required`, 403: "Change your password to continue." | An administrator reset the password from the command line. | Change it on the page that opens. |
| "The current password is not correct. Use the temporary password you were given." | Wrong current password. | Use the temporary password from `reset-password`. |
| `not_local`, 403: "Only local accounts have a password here. Change it where you sign in." | A GitLab user tried to change a password. | Change it in GitLab. |

### Errors you can see anywhere

| What you see | Cause | Fix |
|---|---|---|
| `unauthenticated`, 401: "Sign in to continue." | The session is missing, expired or ended. | Sign in again. |
| Sign-in works, but the next page asks you to sign in again | With an `https` public URL, the cookie is `Secure` and uses the `__Host-` prefix. Opening the site over plain `http`, or under another host name, loses it. | Open the exact public URL over `https`. |
| `forbidden`, 403: "This action requires the admin role." | You are a viewer. | Ask an administrator. |
| `cross_origin`, 403: "Cross-origin request rejected." | A browser sent a change from another site, or a proxy removed or rewrote `Origin` or `Sec-Fetch-Site`. | Open Scanopsis under its public URL. Do not rewrite `Origin` in the proxy. |
| `last_admin`, 409: "At least one enabled administrator must remain. Promote or enable another administrator first." | The change would leave no administrator. | Promote or enable another user first. |
| `busy`, 503: "The server is busy. Try again in a moment." with `Retry-After` | More API requests than database connections, or one client address used more than its quarter of the slots, for 3 seconds. Nothing was done. The log warns `request rejected: too many concurrent requests`. | Retry after `Retry-After`. If it happens often, find what keeps the database busy before raising `pool_max_conns`, see [when the server is busy](deployment.md#when-the-server-is-busy). |
| `request_timeout`, 408 | A request body did not arrive within 2 seconds. | Check the network between client and proxy. |
| `payload_too_large`, 413 (JSON) | An API body over 1 MiB. Uploads have their own limit. | Send less. |
| `internal_error`, 500: "Something went wrong. Try again later." | The cause is in the log. | Read the log. |
| "Scanopsis could not reach the server. Check your connection and try again." | The browser got no answer. | Check the proxy, the container and the network. |

## Uploads from CI

The [GitLab CI guide](integrations/gitlab-ci.md) lists every error code of the
upload with its fix. Use it first. The rows below are problems around
Scanopsis that give no Scanopsis message, or a surprising one.

| What the job log shows | Cause | Fix |
|---|---|---|
| `HTTP 000` and a curl error above it | No answer: wrong URL, DNS, firewall, or a certificate the runner does not trust. | From the runner, `curl <public URL>/healthz`. Install your CA on the runner. |
| `curl: (60) SSL certificate problem` | The runner does not trust the proxy's certificate. | Add the CA to the runner image. Do not use `curl -k`. |
| `HTTP 413` and an HTML page that names `nginx` | nginx accepts 1 MB by default. | Set `client_max_body_size 100m;`, see [use nginx](deployment.md#use-nginx). |
| `HTTP 502` or `HTTP 504` and an HTML page | The proxy cannot reach Scanopsis, or waited too long. | `docker compose ps`. In nginx use the `resolver` form from [use nginx](deployment.md#use-nginx), or restart nginx after Scanopsis was recreated. |
| `HTTP 503`, `gitlab_unavailable`: "The signature could not be checked: Scanopsis could not fetch the signing keys from GitLab. Try again later." | Scanopsis could not get GitLab's signing keys within 10 seconds (network, untrusted certificate). Not the token's fault. The answer hides the details on purpose. | Read the `cause` field of the log line `upload rejected`. For a private CA, set `SCANOPSIS_CA_FILE`, see [trust an internal certificate authority](deployment.md#trust-an-internal-certificate-authority). |
| `HTTP 503`, `busy`: "The server is processing other uploads. Retry later." | Other uploads use the upload memory, and this one did not fit within 30 seconds. | The CI script retries. Spread large uploads out, or raise the container memory and `GOMEMLIMIT` together, see [sizing](deployment.md#sizing). |
| `HTTP 503`, `ingest_not_configured`: "Uploads are not enabled: the GitLab URL is not configured in Scanopsis." | No GitLab URL is set. | Set it on the Settings page. |
| `HTTP 429`, `too_many_requests`, for many jobs at once | Runners behind one NAT address share a limit: 60 at once, then one a second. | Spread the uploads. The script retries. |
| The upload hangs, then `408` (`request_timeout`) | The report did not arrive within 5 minutes. | Upload from a faster network, or send it compressed (the CI script does). |
| `HTTP 201`, but nothing new on the dashboard | The dashboard shows another branch, or the portfolio is filtered. | See [web interface](#web-interface). |
| Rejected uploads you cannot see in the job log | The job ignores the answer on purpose. | The **Integrations** page lists recently refused uploads with the service and the reason; the audit log has `ingest.rejected`. Rejections before the allowed-namespaces check are recorded at most 10 at once per address, then one a minute. The server log has `upload rejected` for every one. |

## SonarQube

Open the **Integrations** page as an administrator. The **SonarQube** band
says whether analyses arrive and, if not, why. Each recent delivery shows the
service, an outcome and a detail. The
[SonarQube guide](integrations/sonarqube.md) explains the setup.

### The status band

| What you see | Cause | Fix |
|---|---|---|
| "SonarQube needs attention" | URL, token or webhook secret missing. | Set them on the Settings page. |
| "Server Unknown" or "Last sync Never" | No sync has finished yet. The first runs shortly after start. | Wait a minute. If it stays, read the last sync error. |
| "Ready, waiting for the first webhook" or "Last webhook Never" | No delivery arrived. | Check the webhook in SonarQube. |
| "The last sync with SonarQube failed" with `payments:api: SonarQube answered 401 on api/measures/component` | The text names the project and SonarQube's answer. 401: wrong token. 403 or 404: no Browse permission, or the project key does not exist. 5xx: SonarQube failed. | Fix the token or the permission. The next sync clears the message. |
| The same, ending in "the request failed or the answer was unreadable; see the server log" | SonarQube could not be reached: DNS, firewall, certificate, a 10-second timeout, or a redirect to another host or scheme. | Read the warning `SonarQube sync finished with failures`. Configure the final address, not one that redirects. |
| "Listing the services failed." | A database error. | Read the log. |
| Log: `the SonarQube token is sent without TLS; use an https:// SonarQube URL` | The URL starts with `http://`. | Use `https` unless the network is fully trusted. |

### Webhook answers

SonarQube's own delivery log shows these. A timeout or connection error there
means SonarQube cannot reach Scanopsis, or blocks private addresses.

| Answer | Cause | Fix |
|---|---|---|
| 202, no body | Accepted (also for a repeated delivery). | None. |
| 503 `sonar_not_configured` | URL, token or webhook secret missing. | Set all three. |
| 401 `invalid_signature` | The secret differs, the `X-Sonar-Webhook-HMAC-SHA256` header is missing, or a proxy changed the body. | Use the same secret in both places. Do not rewrite the body. |
| 400 `invalid_payload` | The body is not a SonarQube webhook. | Check the webhook URL. |
| 413 `payload_too_large` | Over 1 MiB, from too many `sonar.analysis.*` properties. | Send fewer. |
| 403 from the proxy | You [restricted the webhook](deployment.md#add-rate-limits-and-lock-down-the-webhook-optional) and SonarQube calls from another address. | Allow the address the proxy sees for SonarQube. |

### Delivery outcomes

| Outcome and detail | Cause | Fix |
|---|---|---|
| `pending` | Waiting. A delivery pending for more than 10 minutes is marked failed by the next sync. | Wait. |
| `pending`: "The processing queue was full; the next sync will read the analysis." | More than 500 deliveries waited at once. | None; the sync picks it up. |
| `linked` | Stored. Details such as "The analysis was already stored by a sync." are for information. | None. |
| `unlinked`: "No service is linked to SonarQube project "...". ..." | No service has that key. | Set the key on the service, or pass `-Dsonar.analysis.gitlabProjectId=$CI_PROJECT_ID` to the scanner. |
| `unlinked`: "The service named by sonar.analysis.gitlabProjectId is linked to a different SonarQube project, ..." | The service already has another key. | Change the key on the service if the project was renamed. |
| `failed`: "The analysis task ended with status "..."; nothing was stored." | The analysis failed in SonarQube. | Fix the analysis. |
| `failed`: "Reading measures failed: SonarQube answered 403 on api/measures/component. The next sync will retry." | Token or permission, as on the card. | Fix the token or the permission. |
| `failed`: "... failed; see the server log." | A database error. | Read the log. |
| `failed`: "Processing was interrupted. Redeliver the webhook from SonarQube to process it again; ..." | The server stopped while processing. | Redeliver in SonarQube. Projects linked by key need nothing. |

You can redeliver a failed delivery from SonarQube: Scanopsis accepts it again
for the same project, at least a minute after the last try, up to five times.

## Settings page

| What you see | Cause | Fix |
|---|---|---|
| "Enter this value again when you change the URL, so the stored one is never sent to a new address." | You changed the GitLab or SonarQube URL without entering the secret sent there. | Enter the secret again in the same save. |
| "Required when other GitLab sign-in settings are set.", "Must be at least 5m.", "Local sign-in can be disabled only when GitLab sign-in is fully configured." and similar | A rule that spans several settings, see the [configuration guide](configuration.md). | Fix the field the message sits next to. Nothing is saved until every field is valid. |

## Web interface

| What you see | Cause | Fix |
|---|---|---|
| "No services yet" | No upload was accepted yet. A service appears with its first accepted upload, or when an administrator adds it. | On **Integrations**, "Last upload" and the rejections show why. See [uploads from CI](#uploads-from-ci). |
| "No services match" | A filter or search hides everything. | Clear it. |
| "No scans yet" on a service | An administrator added the service, but no upload came. | Add the upload step to the pipeline. |
| "No scans of main yet" | `main` is the dashboard branch and no upload came from it. Other branches are never shown instead. | Run the pipeline on that branch, or [choose another branch](operations.md#choose-the-dashboard-branch-of-a-service). |
| "Stale" next to "Last scan" | The latest scan is older than 7 days. | Run the pipeline, or archive a retired service. |
| The numbers do not change after a pipeline | The dashboard shows one branch: the chosen dashboard branch, or else the branch of the most recent scan. A report with an older `CreatedAt` never replaces a newer one. | Check the branch named under the service title, and compare the scan time with the report's `CreatedAt`. |
| A count differs from Trivy's output | Scanopsis counts unsuppressed findings only, sums the latest scan of every artifact, and shows secrets separately. | Follow the [accuracy check](verification.md). |
| An artifact is missing | It was not uploaded for this branch, or has another `ArtifactName`. | Check `ArtifactName` in the report. |
| "No SonarQube analysis yet" | The project is linked, but no analysis arrived. | Run an analysis and check **Integrations**. |
| "Not available" for coverage, duplication or a rating | SonarQube did not report that value. Scanopsis never shows 0 for an unknown value. | Check the project in SonarQube, for example that tests produced a coverage report. |
| Old scans are gone | [Data retention](operations.md#data-retention) deleted them. | Raise `retention.days` if you need more history. |
| A link to GitLab is plain text | The link is not `http` or `https`, or the GitLab URL is not set. | Set the GitLab URL. |
| A blank page, or "The web interface is not included in this build." | The binary was built without the web interface. | Build the image with `make image`, see [install the newest version](versions.md#install-the-newest-version). |

## Reverse proxy and TLS

| What you see | Cause | Fix |
|---|---|---|
| The browser warns about the certificate | Self-signed or private-CA certificate, or the name does not match. | Install the CA in the browsers, or use a certificate from a public CA. |
| Every user has the same address in the log, and one shared rate limit | `SCANOPSIS_TRUSTED_PROXIES` is empty. | Set it to the proxy's address, see [check the proxy](deployment.md#check-the-proxy). |
| The log still shows the proxy's address after you set `SCANOPSIS_TRUSTED_PROXIES` | The value is not the address Scanopsis sees, or you did not restart. | Use the `client_ip` of a request through the proxy. Run `docker compose up -d`; the start-up line must show `"trusted_proxies":1` (or your count). |
| No `Strict-Transport-Security` header | The public URL starts with `http://`. | Use `https://`. |
| GitLab says the redirect URI is invalid | The application's redirect URI is not `<public URL>/api/v1/auth/gitlab/callback`. | Fix it in GitLab. |
| CI tokens are refused with "The token audience (aud) does not contain ..." | `aud` in `id_tokens` differs from the public URL. | Use the exact public URL. |
| A blank page or an unstyled sign-in page under a path such as `/scanopsis/` | Sub-paths do not work: the interface loads its files from `/assets/...`. | Serve Scanopsis at the root of its own host name, see [reverse proxy and TLS](deployment.md#reverse-proxy-and-tls). |
| `429` before the application's limit is reached | A proxy rate limit (`limit_req`) counts everyone behind one address together. | Raise the proxy limit or its `burst`. |
| Answers are not compressed | No `gzip` (nginx) or `encode` (Caddy), or the client sends no `Accept-Encoding`. | Add it, see [check the proxy](deployment.md#check-the-proxy). |

## Backups and restores

Open the **Backups** page first: it shows the reason in a notice. The same
words are in the server log and in the audit log. See
[backup and restore](operations.md#backup-and-restore).

| What you see | Cause | Fix |
|---|---|---|
| `the backup folder cannot be used: ... is not writable ...` or `... does not exist ...`; **Back up now** is disabled | The folder `backups` is missing, or the user 65532 that runs Scanopsis cannot write to it. | In `deploy/compose`: `sudo install -d -m 700 -o 65532 -g 65532 backups` (or `sudo chown 65532:65532 backups` for an existing folder), then restart Scanopsis: `docker compose restart scanopsis`. Check that the disk has free space and is not read-only. |
| `not enough free space for a backup: a backup needs about ... and the backup folder has ... free` | The disk of `backups` has less than the estimated size plus 512 MiB. | Delete old backups on the Backups page, or free or add disk space. For a restore, tick **Skip the backup of the current state** only if you accept that the restore cannot be undone. |
| `the backup was made with another secret key` | The backup belongs to an installation with another `secrets/secret_key`. | Put the key of the installation that made the backup into `secrets/secret_key`, and restart. On the setup screen, a restart prints a new setup token. |
| `the backup was made by a newer version of Scanopsis` | You run an older version than the one that made the file. | Install the newer version first, see [versions](versions.md), then restore. |
| `the backup was not made by this installation's secret key, or it was changed after it was made` | The signature is wrong: the file was changed, damaged in transfer, or made with another key. | Download the backup again from its source. If it is the right file, check the secret key. |
| `the file is not a Scanopsis backup`, `the file is damaged` | The upload is not a backup, or it is incomplete. | Upload the original file again. |
| An upload stops halfway | The network dropped. | The page offers **Resume upload**, which continues from the last part the server has. Parts are 8 MiB; a proxy that limits the body to less than that refuses them, see [deployment](deployment.md#reverse-proxy-and-tls). |
| A restore is refused with `table ... belongs to role ..., not to ...` | With your own PostgreSQL, a Scanopsis table has another owner than the user in `secrets/database_url`. A group role the user belongs to is not enough. | Make that user the owner of every Scanopsis table, see [use an existing PostgreSQL](deployment.md#use-an-existing-postgresql). |
| A restore is refused with `another database object depends on Scanopsis table ...` | Something else in the database, such as a view or foreign key of another application, uses a Scanopsis table. | Remove it, or restore into a database used only by Scanopsis. |
| After a restore the page says `The backup was restored, but Scanopsis must restart to finish.` | The data was restored, but a step after it failed. The server stays in restore mode and answers `503 restoring`. | `docker compose restart scanopsis`, then sign in. |
| `The restore failed and nothing was changed.` | A step before the commit failed; the database is as it was. | Read the server log (`docker compose logs scanopsis`) for the reason, fix it and try again. |
| Many scans disappeared the day after a restore | The backup was old, and the daily clean-up removed scans older than the retention period. | Raise the retention period on the Settings page right after a restore, before the next daily clean-up. If the clean-up already ran, restore the same backup again and raise the period at once. |
| A scheduled backup failed (banner) | One of the reasons above, most often the folder or the disk. | Fix it; the next scheduled run tries again, or select **Back up now**. |

## Containers and the host

| What you see | Cause | Fix |
|---|---|---|
| `docker compose` warns about `orphan containers` after you moved to your own database | The old `db` container still exists. | `docker compose up -d --remove-orphans`. |
| `docker inspect "$(docker compose ps -q scanopsis)" --format '{{.State.OOMKilled}}'` prints `true` | The container ran out of memory. | Raise `mem_limit` and keep `GOMEMLIMIT` about 20% below it, see [check the memory and process limits](deployment.md#check-the-memory-and-process-limits). |
| Log times are in UTC | The container runs in UTC. | Set `TZ`, see [show local time in the log](deployment.md#show-local-time-in-the-log). |
| Compose says `no configuration file provided: not found` | You are not in `deploy/compose`. | `cd deploy/compose`. |

## Still stuck?

Collect the log (`docker compose logs --since 30m scanopsis`), the output of
`curl -s <public URL>/api/v1/version` and the steps that lead to the problem.
Remove tokens and addresses you do not want to share, then
[open an issue](https://github.com/siyamsarker/scanopsis/issues). For a
security problem, follow the [security policy](../SECURITY.md) instead.

---

[Back to the README](../README.md) · [All guides](../README.md#documentation) · Found a mistake? [Open an issue](https://github.com/siyamsarker/scanopsis/issues)
