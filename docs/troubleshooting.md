# Troubleshooting

Find the symptom, read the cause, apply the fix. The tables are grouped by the
place where you see the problem. Start with the log:

```sh
docker compose logs --since 10m scanopsis
```

Run `docker compose` commands in `deploy/compose`. Messages are quoted exactly
as Scanopsis prints them. A `...` stands for text that depends on your
installation.

## Start-up

The container restarts in a loop, or `docker compose ps` shows it as
`Restarting`. Read the first lines of the log.

### Configuration errors

A configuration error is printed before anything else, one line for every
problem, and the exit status is 2. Fix all lines, then run `docker compose up -d`.

| Message | Cause | Fix |
|---|---|---|
| `SCANOPSIS_PUBLIC_URL is required, for example https://scanopsis.example.com` | The variable is missing or empty. | Set it in `.env`. |
| `SCANOPSIS_PUBLIC_URL must be an absolute http or https URL, got "..."` | The value has no scheme or host, or a scheme other than `http` or `https`. | Write the full address, for example `https://scanopsis.example.com`. |
| `SCANOPSIS_PUBLIC_URL must not contain a path, query, fragment or credentials, got "..."` | The address has more than scheme, host and port. | Remove the path, query, fragment and user name. A trailing slash is allowed. |
| `SCANOPSIS_DATABASE_URL is required` | Neither `SCANOPSIS_DATABASE_URL` nor `SCANOPSIS_DATABASE_URL_FILE` is set, or the value is empty. This line is also printed after the "both set" line below. | In Compose the file variant is set for you. Check that `secrets/database_url` exists and is not empty. |
| `SCANOPSIS_DATABASE_URL and SCANOPSIS_DATABASE_URL_FILE are both set; set only one` | Both the value and the file variant are set. The same message exists for `SCANOPSIS_SECRET_KEY`, `SCANOPSIS_GITLAB_CLIENT_SECRET`, `SCANOPSIS_SONAR_TOKEN` and `SCANOPSIS_SONAR_WEBHOOK_SECRET` with their own names. | Remove one of them. |
| `SCANOPSIS_DATABASE_URL_FILE cannot be read: open ...: no such file or directory` | The file does not exist inside the container. The same message exists for the other `_FILE` variables. | Check the path and the `secrets:` entry in `compose.yaml`. |
| `SCANOPSIS_SECRET_KEY is required; generate one with: openssl rand -base64 32` | No key. | Create `secrets/secret_key` as in the [getting started guide](getting-started.md#step-2-create-the-secrets). |
| `SCANOPSIS_SECRET_KEY is not valid (key must be 32 bytes, got N); generate one with: openssl rand -base64 32` | The key decodes to a different length. | Create a new one, if no data depends on the old one. Otherwise see [the secret key](configuration.md#the-secret-key). |
| `SCANOPSIS_SECRET_KEY is not valid (key is not valid base64); generate one with: openssl rand -base64 32` | The value is not base64. | Same as above. |
| `SCANOPSIS_LOG_LEVEL must be debug, info, warn or error, got "..."` | Unknown level. | Use one of the four. |
| `SCANOPSIS_TRUSTED_PROXIES contains "...", which is not an IP address or CIDR range` | One entry is not an address such as `172.30.0.10` or a range such as `10.0.0.0/8`. | Correct the entry. Separate entries with commas. |
| `SCANOPSIS_LOCAL_LOGIN_ENABLED must be true or false, got "..."` | Same for `SCANOPSIS_INGEST_PROTECTED_REFS_ONLY`. Accepted: `true`, `false`, `1`, `0` (and `t`, `f`, `T`, `F`, `TRUE`, `FALSE`). | Use `true` or `false`. |
| `SCANOPSIS_SESSION_IDLE_TIMEOUT must be a duration such as 30m or 8h, got "..."` | Same for `SCANOPSIS_SESSION_ABSOLUTE_TIMEOUT` and `SCANOPSIS_SONAR_SYNC_INTERVAL`. A number without a unit (`5`) is not a duration. | Write a unit: `30m`, `8h`, `1h30m`. |
| `SCANOPSIS_SESSION_IDLE_TIMEOUT must be at least 5m` | Idle timeout too short. | Use `5m` or more. |
| `SCANOPSIS_SESSION_ABSOLUTE_TIMEOUT must not be shorter than SCANOPSIS_SESSION_IDLE_TIMEOUT` | The two values contradict each other. | Make the absolute timeout at least as long. |
| `SCANOPSIS_RETENTION_DAYS must be a whole number from 7 to 3650, got "..."` | Not an integer, or out of range. | Use a whole number of days from 7 to 3650. |
| `SCANOPSIS_GITLAB_URL is required when other GitLab settings are set` | A client ID, client secret or allowed groups is set without the GitLab URL. | Set the URL, or remove the others. |
| `SCANOPSIS_GITLAB_CLIENT_SECRET is required when SCANOPSIS_GITLAB_CLIENT_ID is set` | The ID is set without the secret. | Set both or neither. |
| `SCANOPSIS_GITLAB_CLIENT_ID is required when SCANOPSIS_GITLAB_CLIENT_SECRET is set` | The secret is set without the ID. | Set both or neither. |
| `SCANOPSIS_SONAR_URL and SCANOPSIS_SONAR_TOKEN must be set together (the webhook secret needs both)` | Only one of the SonarQube URL and token is set, or the webhook secret is set without them. | Set the URL and the token together. |
| `SCANOPSIS_GITLAB_URL must be an absolute http or https URL, got "..."` | Same rules as the public URL, for `SCANOPSIS_GITLAB_URL` and `SCANOPSIS_SONAR_URL`. | Correct the address. |

### Errors after the configuration was accepted

These appear in the log as `scanopsis stopped with an error`, with the cause in
the `error` field.

| Message | Cause | Fix |
|---|---|---|
| `read SCANOPSIS_CA_FILE: open ...: no such file or directory` | The CA file is not mounted at that path. | Mount it, see [internal certificate authority](deployment.md#internal-certificate-authority). |
| `SCANOPSIS_CA_FILE ... contains no PEM certificates` | The file is empty or is not PEM. | Put the CA certificate in PEM format in the file. |
| `parse database URL: cannot parse ...` | The database URL is malformed. | Check `secrets/database_url`. A password with special characters must be percent-encoded. |
| `connect to database: failed to connect to ... connection refused` (or `no such host`, `password authentication failed`) | The database is not running, not reachable, or the credentials differ. | `docker compose ps db`. Compare `secrets/database_url` with `secrets/postgres_password`. The password is read only when the database volume is first created. |
| `apply migrations: ...` | A schema change failed. | Read the database error in the message. Restore the backup you took before the upgrade, see [roll back](deployment.md#roll-back). |
| `listen on :8080: ... address already in use` | Another program uses the port inside the container, or `SCANOPSIS_LISTEN_ADDR` is wrong. | Check `SCANOPSIS_LISTEN_ADDR`. |
| `setting gitlab.client_secret cannot be decrypted: SCANOPSIS_SECRET_KEY changed or is wrong` | The saved secret was encrypted with another key. The same for `sonar.token` and `sonar.webhook_secret`. | Restore the original key, see [the secret key](configuration.md#the-secret-key). |
| `gitlab.allowed_groups from SCANOPSIS_GITLAB_ALLOWED_GROUPS: Group path "/platform" must not start or end with a slash.` | A group path in the environment is invalid. The message names the setting, the variable and the rule. Another rule: `Group path "a b" must not contain spaces.` | Correct the variable. These checks need the database, so they appear after the connection works. |
| `local sign-in is disabled but GitLab sign-in is not fully configured, so nobody could sign in: ...` | Local sign-in is off (from the environment or the database) and GitLab sign-in is incomplete. The message names the fix. | Follow it, or see [turn local sign-in on again](operations.md#turn-local-sign-in-on-again). |
| `setting ... has an unreadable stored value` or `... has an invalid stored value` | A row in the `settings` table was changed outside Scanopsis. | Delete the row, see [configuration](configuration.md#two-kinds-of-settings), or restore a backup. |

If the container is healthy but sign-in fails with a server error, see
[sign-in and two-factor](#sign-in-and-two-factor). To see what the
container sees:

```sh
docker compose config --quiet && echo config-ok
docker compose logs scanopsis | grep 'scanopsis starting' | tail -1
```

## Sign-in and two-factor

### First-run setup

| What you see | Cause | Fix |
|---|---|---|
| `invalid_setup_token`: "The setup token is wrong. Copy it from the server log written at start." (page: "The setup token is not correct. Copy it again from the server log.") | The token is from an earlier start, or incomplete. Every start creates a new one. | `docker compose logs scanopsis \| grep setup_token`, use the last one. |
| `setup_complete`: "Setup is already complete. Sign in instead." | An administrator exists. | Sign in. To recover access, see [operations](operations.md#recovering-access-when-locked-out). |
| `weak_password`: "The password must be at least 12 characters long." / "The password must be at most 1024 bytes long." / "The password must not be the same as the username." / "The password must not be a single repeated character." | The password breaks a rule. | Choose another password. |
| `invalid_request`: "\"token\", \"username\" and \"password\" are required." and other shape errors | The request body is incomplete. | Send all three fields. |
| The setup page opens, but the log has no `setup_token` | An administrator already exists. | Sign in. |

### Local sign-in

| What you see | Cause | Fix |
|---|---|---|
| "Wrong username or password." (`invalid_credentials`, 401) | The user does not exist, or the password is wrong. Both give the same answer on purpose. A GitLab user has no local password. | Check the spelling. Reset the password with `scanopsis admin reset-password`. |
| `too_many_attempts`, 429, "Too many sign-in attempts. Wait N seconds and try again." | A rate limit was reached. See [rate limits](configuration.md#rate-limits). Behind a proxy without `SCANOPSIS_TRUSTED_PROXIES`, all users share one address and one limit. | Wait the number of seconds in `Retry-After`. Set `SCANOPSIS_TRUSTED_PROXIES`. |
| `busy`, 503, "The server is busy. Try again in a moment." | Many password checks run at once. | Try again. |
| `not_enabled`, 404, "Local sign-in is not enabled on this server." | Local sign-in is turned off. | Sign in with GitLab, or [turn it on again](operations.md#turn-local-sign-in-on-again). |
| `invalid_request`: "Send a JSON object with only \"username\" and \"password\" (at most 8 KiB)." and similar | A client sent a malformed request. | Fix the client. The web page never does this. |
| `internal_error`, 500: "Sign-in failed because of a server problem. Try again, and check the server logs if it persists." | A database or server error. | Read the log. |

### GitLab sign-in

After a failure, the browser opens `/login?error=<code>`.

| Code and message | Cause | Fix |
|---|---|---|
| `access_denied`: "You cancelled the sign-in on GitLab." | The user selected **Deny** on GitLab. | Try again. |
| `group_not_allowed`: "Your GitLab account is not in a group that may use Scanopsis. Ask an administrator to add your group." | The user belongs to none of the allowed groups. | Add the group, with its exact upper and lower case. See [allowed groups](integrations/gitlab-sso.md#allowed-groups). |
| `login_expired`: "The sign-in took too long or was started in another tab. Try again." | The sign-in cookie is missing or older than 10 minutes, or the browser used another host name than `SCANOPSIS_PUBLIC_URL`. | Start again. Open Scanopsis with exactly the host name in the public URL. Check that the proxy passes `Set-Cookie`. |
| `login_failed`: "Sign-in with GitLab failed. Try again, and contact an administrator if it keeps failing." | Any other failure: wrong client secret, GitLab unreachable, an untrusted certificate, a wrong clock. | Read the log line `GitLab sign-in failed`, its `error` field, and the table in the [GitLab sign-in guide](integrations/gitlab-sso.md#troubleshooting). |
| `gitlab_unavailable`, 503: "GitLab could not be reached. Try again in a few seconds, or sign in as the local administrator." | Scanopsis could not read the OpenID Connect configuration of GitLab. The log line `GitLab discovery failed` has the reason. | Check DNS, the route and the certificate from the host. Add an internal CA, see [deployment](deployment.md#internal-certificate-authority). Scanopsis waits 10 seconds after a failure. |
| `not_enabled`, 404, "GitLab sign-in is not enabled on this server." | GitLab sign-in is not fully configured. | Set the GitLab URL, the application ID and the secret. `curl -s <url>/api/v1/auth/methods` must show `"gitlab":true`. |

### Two-factor authentication and passwords

| What you see | Cause | Fix |
|---|---|---|
| "That code is not correct. Wait for a new one in the app and try again." (`invalid_code`, 401: "That code is wrong or has already been used.") | The code expired, was typed wrongly, was already used, or the clock is wrong. A code is accepted for 30 seconds before and after the current time, once. | Wait for the next code. Check the time of the server and of the phone. |
| "That recovery code is not correct, or it was already used." | Wrong code, or used before. Each works once. | Use another code. If none is left, ask an administrator to reset your two-factor authentication. |
| `too_many_attempts`, 429, on the code page | More than 5 code attempts at once for this user, then one every 30 seconds. | Wait. |
| `mfa_enrollment_required`, 403: "Set up two-factor authentication to continue." | The user has not enrolled. | Open the page, scan the code, confirm. |
| `mfa_required`, 403: "Enter your two-factor authentication code to continue." | The session passed the first factor only. | Enter the code. |
| `password_change_required`, 403: "Change your password to continue." | An administrator reset the password from the command line. | Change it on the page that opens. |
| `mfa_already_enrolled`, 409: "Two-factor authentication is already set up." | The user already has a secret. | To start again, an administrator resets two-factor authentication. |
| `mfa_enrollment_not_started`, 409: "Start the enrolment first." | Confirming without a pending secret. | Reload the enrolment page. |
| `mfa_enrollment_required`, 409: "Set up two-factor authentication first." | A code was sent for a user who has not enrolled. | Enrol first. |
| Every code fails, the log says `cannot decrypt TOTP secret; check SCANOPSIS_SECRET_KEY` | The key is not the one that encrypted the secrets, for example after a restore. | Restore the original key. See [the secret key](configuration.md#the-secret-key). |
| `not_local`, 403: "Only local accounts have a password here. Change it where you sign in." | A GitLab user tried to change a password. | Change it in GitLab. |
| `invalid_credentials`, 401: "The current password is wrong." (page: "The current password is not correct. Use the temporary password you were given.") | Wrong current password. | Use the temporary password from `reset-password`. |
| `weak_password`: "The new password must be different from the current one." | New and current are equal. | Choose a different password. |
| `invalid_request`: "Both \"current_password\" and \"new_password\" are required." | Incomplete request. | Fill both fields. |

### Other errors you can see anywhere

| Code | Status | Meaning | Fix |
|---|---|---|---|
| `unauthenticated` | 401 | "Sign in to continue." The session is missing, expired or ended. | Sign in again. If it happens at once after sign-in, see the next row. |
| (sign-in succeeds, the next page asks to sign in again) | | With an `https` public URL the cookie is `Secure` and `__Host-`. A proxy that terminates TLS but is reached over plain `http`, or a different host name, drops it. | Open the site with the exact public URL, over `https`. |
| `forbidden` | 403 | "This action requires the admin role." | Ask an administrator. |
| `cross_origin` | 403 | "Cross-origin request rejected." A browser sent a state-changing request from another site, or a proxy removed or changed `Origin` or `Sec-Fetch-Site` (or the page was opened under another address). | Open Scanopsis under its public URL. Do not rewrite the `Origin` header in the proxy. |
| `method_not_allowed` | 405 | "This method is not supported on this endpoint." | Use the documented method, see the [API reference](api.md). |
| `not_found` | 404 | "No such endpoint." or "No such user." or "Service not found." | Check the address or id. |
| `not_enabled` | 404 | "... is not enabled on this server." | The feature is not set up. |
| `last_admin` | 409 | "At least one administrator must remain. Promote another user first." | Promote another user, then demote. |
| `invalid_request`, `invalid_parameter` | 400 | The request or a query parameter is malformed. The message names the field. | Correct it. |
| `internal_error` | 500 | "Something went wrong. Try again later." The cause is in the log. | Read the log. |
| (page) "Scanopsis could not reach the server. Check your connection and try again." | | The browser got no answer. | Check the proxy, the container and the network. |
| (page) "Too many attempts. Try again in N seconds." | 429 | A rate limit. | Wait. |

## Uploads from CI

Every error code of the upload endpoint, with its message and fix, is listed in
the table in the
[GitLab CI guide](integrations/gitlab-ci.md#troubleshooting). Use it first. The
causes below happen before or around Scanopsis and give no Scanopsis message at
all, or an unexpected one.

| What the job log shows | Cause | Fix |
|---|---|---|
| `HTTP 000` and a curl error above it | curl got no answer: wrong URL, DNS, firewall, or a certificate the runner does not trust. | Run the same `curl` from the runner against `<public URL>/healthz`. Install your CA certificate on the runner. |
| `curl: (60) SSL certificate problem` | The runner does not trust the certificate of the proxy. | Add the CA to the runner image. Do not use `curl -k`. |
| `HTTP 413` and an HTML page that names `nginx` | The proxy refuses the body. nginx accepts 1 MB by default. | Set `client_max_body_size 100m;`, see [deployment](deployment.md#option-2-nginx). |
| `HTTP 502` or `HTTP 504` and an HTML page | The proxy cannot reach Scanopsis, or waited too long. | `docker compose ps`. Check the upstream address. nginx: use the `resolver` form in [deployment](deployment.md#option-2-nginx), or restart nginx after recreating Scanopsis. |
| `HTTP 503`, code `gitlab_unavailable`: "The signature could not be checked: Scanopsis could not fetch the signing keys from ..." | Scanopsis could not download the signing keys from your GitLab (network or an untrusted certificate). This is not the fault of the token. The log line is `cannot fetch GitLab signing keys` and gives no reason. | Make the request from a throwaway container on the same Docker network to see the reason, see [internal certificate authority](deployment.md#internal-certificate-authority). Add the CA with `SCANOPSIS_CA_FILE`. |
| `HTTP 503`, code `ingest_not_configured` | "Uploads are not enabled: the GitLab URL is not configured in Scanopsis." or "...: the Scanopsis public URL is not configured." | Set the GitLab URL (the public URL is required at start). |
| `HTTP 429`, code `too_many_requests` for many jobs at once | Runners behind one NAT address share the limit: 60 at once, then one a second. | Spread the uploads or retry. The script retries twice. |
| The upload hangs, then fails | The link is slower than the read timeout of 30 seconds. | Upload a smaller report, or from a faster network. |
| `HTTP 201` but nothing in the dashboard | The service was created for the project, but the dashboard shows another ref, or the portfolio is filtered. | See [web interface](#web-interface). |
| Rejected uploads that you cannot see in the job log | The job ignores the answer on purpose. | The **Integrations** page lists recent rejections with the reason. The audit log has the action `ingest.rejected`. |

## SonarQube

Open the **Integrations** page (administrators). Its **SonarQube** card shows
the state, and each recent delivery shows an outcome and a detail text.

### The card

| What you see | Cause | Fix |
|---|---|---|
| "Not configured — see Settings" | The URL or the token is not set. | Set them on the Settings page. |
| "Server version unknown" | No sync has succeeded yet. The first sync runs shortly after start. | Wait a minute. If it stays, read the last sync error. |
| "Last webhook never" | No delivery has arrived. | Check the webhook in SonarQube, see below. |
| "Last sync never" | The sync has not completed. | Check that the URL and the token are set. |
| "The last sync failed" with a text such as `payments:api: SonarQube answered 401 on api/measures/component` | The text names the project and what SonarQube answered. 401: the token is wrong. 403 or 404: no Browse permission, or the project key does not exist. 5xx: SonarQube failed. | Fix the token or the permission. The next sync clears the message. |
| The same text, without a status: "the request failed or the answer was unreadable; see the server log" | SonarQube could not be reached: DNS, firewall, TLS certificate, a timeout of 10 seconds, or a redirect to another scheme or host. | Read the log line `SonarQube sync failed for a project`. Configure the final address, not one that redirects. |
| "Listing the services failed." | A database error. | Read the log. |
| Log: `the SonarQube token is sent without TLS; use an https:// SonarQube URL` | The URL starts with `http://`. | Use `https` unless the network is fully trusted. |

### Answers of the webhook endpoint

| Status and code | Message | Cause | Fix |
|---|---|---|---|
| 503 `sonar_not_configured` | "The SonarQube integration is not configured. Set the SonarQube URL, token and webhook secret in the settings." | One of the three is missing. | Set all three. |
| 401 `invalid_signature` | "The X-Sonar-Webhook-HMAC-SHA256 header is missing or does not match the webhook secret." | The secret differs, the header is missing, or a proxy changed the body. | Use the same secret in both places. Do not rewrite the body. |
| 400 `invalid_payload` | "The payload is not a SonarQube webhook (taskId and project.key are required)." or "The request body could not be read." | The body is not a SonarQube webhook. | Check the webhook URL. |
| 413 `payload_too_large` | "The webhook payload is larger than 1 MiB." | Too many `sonar.analysis.*` properties. | Send fewer. |
| 500 `internal_error` | "Something went wrong. Try again later." | A database error. | Read the log. |
| 202, no body | Accepted. Also returned for a delivery that was seen before. | | |

SonarQube's own delivery log shows the status. A timeout or connection error
there means that SonarQube cannot reach Scanopsis, or blocks private addresses.
See [private addresses](integrations/sonarqube.md#private-addresses).

### Delivery outcomes and details

| Outcome | Detail | Cause | Fix |
|---|---|---|---|
| `pending` | (none) | Waiting to be processed. A delivery pending for more than 10 minutes is marked failed by the next sync. | Wait. |
| `linked` | (none) | Stored. | |
| `linked` | "The analysis was already stored by a sync." | The sync was faster. | None. |
| `linked` | "A newer analysis (...) already exists in SonarQube; the measures are from it. The next sync will add it." | A newer analysis finished first. | None. |
| `linked` | "The latest analysis in SonarQube is older than the one in the webhook; the measures may not belong to it." | SonarQube's data lags behind. | The next sync corrects it. |
| `linked` | "SonarQube reports no analysis for the project; the measures may not belong to this analysis." | SonarQube has no analysis record yet. | Check the project in SonarQube. |
| `unlinked` | "No service is linked to SonarQube project "...". Pass -Dsonar.analysis.gitlabProjectId=$CI_PROJECT_ID to the scanner, or set the SonarQube key on the service." | No service has the key. | Set the key on the service, or pass the property. |
| `unlinked` | "The service named by sonar.analysis.gitlabProjectId is linked to a different SonarQube project, so project "..." was not linked to it. Change the SonarQube key on the service if the project was renamed." | The service already has another key. | Change the key on the service. |
| `unlinked` | "The service named by sonar.analysis.gitlabProjectId cannot be linked to SonarQube project "..." because the service or the key is already linked." | The key belongs to another service. | Free the key on the other service. |
| `failed` | "The analysis task ended with status "..."; nothing was stored." | The analysis failed in SonarQube. | Fix the analysis. |
| `failed` | "The webhook has no readable analysedAt." | The payload has no valid time. | Check the webhook source. |
| `failed` | "The SonarQube URL and token are not configured, so the analysis cannot be read." | Settings were cleared. | Set them again. |
| `failed` | "Reading measures failed: SonarQube answered N on api/measures/component. The next sync will retry." | See the status in the card table above. | Fix token or permission. |
| `failed` | "Reading the latest analysis failed: ... The next sync will retry." | Same, for `api/project_analyses/search`. | Same. |
| `failed` | "Looking up the service failed; see the server log." / "Checking stored analyses failed; see the server log." / "Storing the analysis failed; see the server log." | A database error. | Read the log. |
| `failed` | "The processing queue was full; the next sync will read the analysis." | More than 100 deliveries waited. | None. |
| `failed` | "Processing was interrupted. Redeliver the webhook from SonarQube to process it again; analyses of projects linked by key are also picked up by the next sync." | The server stopped while processing. | Redeliver in SonarQube. A project linked by key needs nothing. |

A failed delivery can be redelivered from SonarQube. Scanopsis accepts it again
for the same project, at least a minute after the last attempt, at most five
times.

## Web interface

| What you see | Cause | Fix |
|---|---|---|
| The portfolio says "No services yet" ("Services appear here after the first pipeline uploads a scan.") | No upload has been accepted. A service is created by the first accepted upload, or by an administrator on the Services page. | Open **Integrations**: "Last upload" and the rejections show why. Check the GitLab URL and the CI job. See [uploads](#uploads-from-ci). |
| "No services match" | The filter or the search hides every service. | Clear the filter. |
| A service shows "No scans yet" | The service exists (added by an administrator) but has no accepted upload. | Add the upload step to the pipeline. |
| A service shows "Stale" next to "Last scan" | Its latest scan is older than 7 days. | Run the pipeline, or archive a service that is retired. |
| The numbers do not change after a new pipeline | Dashboards show the latest scan per artifact of the default ref, by the time in the report. The default ref is the ref of the most recent scan. A report with an older `CreatedAt` does not replace a newer one. A feature branch can become the default ref. | Select the ref on the service page. Compare the time of the scan with the report's `CreatedAt`. |
| A count differs from `trivy` output | Scanopsis counts unsuppressed findings only, sums the latest scan of every artifact, and shows secrets separately. | See the [verification guide](verification.md). |
| An artifact is missing from a service | It was not uploaded for this ref, or its latest scan has another artifact name. | Check the artifact name in the report (`ArtifactName`). |
| "No SonarQube project linked" | The service has no SonarQube key. | Set the key on the Services page, or pass the scanner property. See the [SonarQube guide](integrations/sonarqube.md#4-link-projects-to-services). |
| "No SonarQube analysis yet" | The project is linked, but no analysis arrived. | Run an analysis. Check the **Integrations** page. |
| "Not available" for coverage, duplication or a rating | SonarQube did not report that metric for the analysis. Scanopsis never shows 0 for an unknown value. | Check the project in SonarQube, for example that the test step produced a coverage report. |
| "Not available" for a failed quality gate condition | The condition has no value (`NO_VALUE`). | Same. |
| "Not available" instead of a time | The time in the data is missing or unreadable. | Report it as a defect. |
| Old scans are gone | [Data retention](operations.md#data-retention) deleted scans older than the retention period, except the latest per ref and artifact. | Raise `retention.days` if you need more history. |
| A link to GitLab is plain text | The link did not start with `http` or `https`, or the GitLab URL is not set. | Set the GitLab URL. |
| The page is blank, or shows the message "The web interface is not included in this build." | The binary was built without the web interface. | Use the released image or `make image`. |

## Reverse proxy and TLS

| What you see | Cause | Fix |
|---|---|---|
| The browser warns about the certificate | The certificate is self-signed or from an internal CA, or the name does not match. | Install the CA in the browsers, or use a certificate from a trusted authority. |
| Sign-in loops back to the sign-in page | The cookie is not returned: the public URL is `https` but the browser reaches the site over `http`, or under another host name. | Use the public URL exactly. |
| Every user has the same address in the log, and one rate limit | `SCANOPSIS_TRUSTED_PROXIES` is empty. | Set it to the address of the proxy, see [deployment](deployment.md#verify-the-proxy). |
| The log shows the proxy address even though `SCANOPSIS_TRUSTED_PROXIES` is set | The value is not the address Scanopsis sees. `docker compose logs scanopsis` shows `client_ip` of a request made through the proxy. The startup line must show `"trusted_proxies":1` (or the count of entries). | Use that address. Run `docker compose up -d` after editing `.env`. |
| No `Strict-Transport-Security` header | The public URL starts with `http://`. | Use `https://`. |
| GitLab says the redirect URI is invalid | The application's redirect URI is not `<public URL>/api/v1/auth/gitlab/callback`. | Correct it in GitLab. |
| CI tokens are refused with "The token audience (aud) does not contain ..." | `aud` in `id_tokens` differs from the public URL by a character. | Use the exact public URL. |

If nothing here matches, collect the log (`docker compose logs --since 30m scanopsis`),
the output of `curl -s <public URL>/api/v1/version`, and the steps to reproduce,
and open an issue. For a security problem, follow the
[security policy](../SECURITY.md) instead. Remove tokens and addresses you do
not want to share first.
