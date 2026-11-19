# Configuration

This page describes every way to configure Scanopsis. It is the reference you
use after the [getting started guide](getting-started.md).

## Two kinds of settings

Scanopsis has two kinds of settings.

**Environment-only settings** configure the process: where it listens, which
database it uses, how it logs. You set them as environment variables. In the
Compose deployment they live in `.env` and in the secret files. They are read
once at start, so you restart the container to change them.

**Runtime settings** configure behaviour that administrators change while the
server runs: GitLab sign-in, local sign-in and session timeouts. You change
them on the **Settings** page, which is visible to administrators only.
Changes apply to the next request, without a restart.

Every runtime setting also has an environment variable. The variable only
**seeds** the setting:

1. At start, for each runtime setting that has an environment variable set and
   no saved value, Scanopsis saves the environment value in the database.
2. From then on, the saved value is the source of truth. Changing or removing
   the environment variable has no effect on a setting that is already saved.
3. If you change a setting on the Settings page, the new value replaces the
   saved one.
4. If you select **Reset to environment or default** and save, the saved value
   is deleted. The setting then falls back to the environment variable if it
   is set, and to the default otherwise.

The Settings page shows where each value comes from:

| Source label | Meaning |
|---|---|
| Default | No value is saved and no environment variable is set. The built-in default applies. |
| Environment | The value came from the environment variable and nobody has changed it on the Settings page. |
| Set here | An administrator saved the value on the Settings page. The page shows who changed it and when. |

Use the environment to set up a new deployment from a file. Use the Settings
page for everything after that.

## Environment-only settings

All variables start with `SCANOPSIS_`. An empty value counts as not set.

| Variable | Type | Default | Example | Effect and validation |
|---|---|---|---|---|
| `SCANOPSIS_DATABASE_URL` | secret | none, required | `postgres://scanopsis:pw@db:5432/scanopsis?sslmode=disable` | PostgreSQL connection URL. Scanopsis creates and updates its tables at start. Use `SCANOPSIS_DATABASE_URL_FILE` instead in the Compose deployment. |
| `SCANOPSIS_LISTEN_ADDR` | `host:port` | `:8080` | `:8080` | Address the server listens on. The image health check reads the port from this variable. An address that cannot be bound stops the server at start. |
| `SCANOPSIS_PUBLIC_URL` | URL | none, required | `https://scanopsis.example.com` | The address users and GitLab use to reach Scanopsis. Must be an absolute `http` or `https` URL with no path, query, fragment or credentials. A trailing slash is removed. With `https`, session cookies get the `Secure` attribute and the server sends `Strict-Transport-Security`. The GitLab redirect URI is this URL plus `/api/v1/auth/gitlab/callback`. |
| `SCANOPSIS_TRUSTED_PROXIES` | list | empty | `172.18.0.1,10.0.0.0/8` | Comma-separated IP addresses or CIDR ranges of reverse proxies. Scanopsis believes `X-Forwarded-For` only when the request comes from one of them. An entry that is not an address or range stops the server at start. See [reverse proxy and TLS](#reverse-proxy-and-tls). |
| `SCANOPSIS_CA_FILE` | path | empty | `/etc/scanopsis/ca.pem` | A PEM file with certificate authorities to trust in addition to the system roots when Scanopsis connects to GitLab. The file must exist and contain at least one certificate, otherwise the server stops at start. |
| `SCANOPSIS_LOG_LEVEL` | text | `info` | `debug` | Log verbosity: `debug`, `info`, `warn` or `error`. |
| `SCANOPSIS_SECRET_KEY` | secret | none, required | output of `openssl rand -base64 32` | Base64 encoding of exactly 32 random bytes. Encrypts secrets in the database. Use `SCANOPSIS_SECRET_KEY_FILE` instead in the Compose deployment. See [the secret key](#the-secret-key). |

The database URL, the secret key and the GitLab client secret can also be
given as files. See [file variants and Docker secrets](#file-variants-and-docker-secrets).

If any variable is invalid, the server does not start. It prints
`configuration error:` followed by one line for every problem, so you can fix
them all at once. Example:

```text
configuration error:
SCANOPSIS_PUBLIC_URL must not contain a path, query, fragment or credentials, got "https://scanopsis.example.com/app"
```

## Runtime settings

The columns show the key as it appears on the Settings page, the variable that
seeds it, the type, the default and the rules. The value is validated when you
save it, and the whole change is rejected, with nothing saved, if any value is
invalid.

| Key | Seeding variable | Type | Default | Example | Effect and validation |
|---|---|---|---|---|---|
| `gitlab.url` | `SCANOPSIS_GITLAB_URL` | URL | empty | `https://gitlab.example.com` | Base URL of the GitLab instance. Also the OpenID Connect issuer and the issuer of CI job tokens: set it to accept scan uploads, even without GitLab sign-in. Same rules as the public URL: absolute `http` or `https`, no path, query, fragment or credentials. |
| `gitlab.client_id` | `SCANOPSIS_GITLAB_CLIENT_ID` | text | empty | `d4c3...` | Application ID of the GitLab OAuth application. |
| `gitlab.client_secret` | `SCANOPSIS_GITLAB_CLIENT_SECRET` | secret | not set | | Secret of the GitLab OAuth application. Stored encrypted and never shown again. It cannot be empty; reset it instead. |
| `gitlab.allowed_groups` | `SCANOPSIS_GITLAB_ALLOWED_GROUPS` | list | empty | `platform,security/appsec` | GitLab groups, by full path, whose members may sign in. Empty allows every GitLab user. Each path must not be empty, must not start or end with `/` and must not contain spaces. In the environment, separate entries with commas. On the Settings page, write one entry per line. See the [GitLab sign-in guide](integrations/gitlab-sso.md#allowed-groups). |
| `auth.local_login_enabled` | `SCANOPSIS_LOCAL_LOGIN_ENABLED` | boolean | `true` | `false` | Whether users can sign in with a local username and password. In the environment, use `true`, `false`, `1` or `0`. |
| `session.idle_timeout` | `SCANOPSIS_SESSION_IDLE_TIMEOUT` | duration | `8h` | `30m` | How long a session may stay unused before the user must sign in again. At least `5m`. |
| `session.absolute_timeout` | `SCANOPSIS_SESSION_ABSOLUTE_TIMEOUT` | duration | `24h` | `12h` | Maximum lifetime of a session, however active. Not shorter than the idle timeout. |
| `ingest.allowed_namespaces` | `SCANOPSIS_INGEST_ALLOWED_NAMESPACES` | list | empty | `platform,security` | GitLab groups, by full path, whose projects may upload scan reports; projects in subgroups are included. Empty accepts every project whose CI job token is valid. Same path rules as the allowed groups. See the [GitLab CI guide](integrations/gitlab-ci.md). |
| `ingest.protected_refs_only` | `SCANOPSIS_INGEST_PROTECTED_REFS_ONLY` | boolean | `false` | `true` | Accept scan reports only from pipelines on protected branches and tags (the token's `ref_protected` claim). |
| `sonar.url` | `SCANOPSIS_SONAR_URL` | URL | empty | `https://sonarqube.example.com` | Base URL of the SonarQube server. Same rules as the GitLab URL. Prefer `https`: the token is sent with every request. See the [SonarQube guide](integrations/sonarqube.md). |
| `sonar.token` | `SCANOPSIS_SONAR_TOKEN` | secret | not set | | Token of a SonarQube user with Browse permission on the projects. Stored encrypted and never shown again. |
| `sonar.webhook_secret` | `SCANOPSIS_SONAR_WEBHOOK_SECRET` | secret | not set | | Secret of the SonarQube webhook. Deliveries without a valid signature are rejected. Stored encrypted and never shown again. |
| `sonar.sync_interval` | `SCANOPSIS_SONAR_SYNC_INTERVAL` | duration | `15m` | `5m` | How often linked SonarQube projects are refreshed, in case a webhook was missed. At least `1m`. |
| `retention.days` | `SCANOPSIS_RETENTION_DAYS` | integer | `90` | `180` | How many days scan history, SonarQube analyses and SonarQube deliveries are kept. A whole number from `7` to `3650`. The latest scan of every service, ref, artifact and kind, and the latest analysis of every service, are kept however old they are. See [data retention](operations.md#data-retention). |

Durations are written with units, such as `30m`, `8h` or `1h30m`. Integers are
written as plain whole numbers, such as `90`; a value with a fraction, with
units or outside the range is rejected.

### Rules that span several settings

Scanopsis checks these rules every time you save on the Settings page. At
start it checks only the GitLab rule and the two session rules, and only for
values that come from the environment. It does not check the local sign-in
rule at start. See the warning below.

- The GitLab URL alone is valid: it is enough to accept scan uploads. GitLab
  sign-in additionally needs the client ID and the client secret; if you set
  one of these two, you must set the URL and the other one. Otherwise the page
  shows "Required when other GitLab sign-in settings are set." next to the
  missing field.
- The SonarQube integration needs the URL and the token together; the
  webhook secret needs both. Otherwise the page shows "Required when other
  SonarQube settings are set." The sync interval is at least one minute
  ("Must be at least 1m.").
- When you change the GitLab URL while a client secret is stored, or the
  SonarQube URL while a token is stored, enter the client secret or the token
  again in the same save. Scanopsis sends these values to the URL, so it never
  sends a stored one to a new address. Otherwise the page shows "Enter this
  value again when you change the URL, so the stored one is never sent to a
  new address." next to the secret.
- Local sign-in cannot be disabled unless GitLab sign-in is fully configured.
  The page shows "Local sign-in can be disabled only when GitLab sign-in is
  fully configured." This rule prevents you from locking everyone out.
- The idle timeout is at least five minutes. The page shows "Must be at least
  5m."
- The absolute timeout is not shorter than the idle timeout. The page shows
  "Must not be shorter than the idle timeout."
- The retention period is a whole number of days from 7 to 3650. The page
  shows "Must be a whole number from 7 to 3650." In the environment, an invalid
  `SCANOPSIS_RETENTION_DAYS` stops the server at start with the same range in
  the message.

If GitLab sign-in is not fully configured, Scanopsis does not offer it on the
sign-in page and does not contact GitLab.

> **Note:** local sign-in can only be off while GitLab sign-in is fully
> configured. If the settings at start would leave no way to sign in (for
> example `SCANOPSIS_LOCAL_LOGIN_ENABLED=false` without GitLab sign-in), the
> server does not start and its log names the fix:
>
> ```text
> local sign-in is disabled but GitLab sign-in is not fully configured, so nobody could sign in: remove SCANOPSIS_LOCAL_LOGIN_ENABLED=false from the environment, or configure GitLab sign-in, then start again
> ```
>
> If you turn local sign-in off and GitLab later stops working (GitLab is
> down, or the client secret is wrong), nobody can sign in. To recover, see [recovering access](operations.md#recovering-access-when-locked-out).

### When changes apply

- GitLab settings and the local sign-in switch apply to the next request.
- The idle timeout applies at the next request of every session, including
  sessions that already exist.
- The absolute timeout is fixed when a session starts. A change affects
  sessions that start afterwards.
- The retention period is read at the start of each cleanup run, so a change
  applies from the next run. It does not start a run. See
  [data retention](operations.md#data-retention).

## File variants and Docker secrets

Three variables accept a `_FILE` variant. The variant holds the path of a file
whose content is the value:

| Variable | File variant |
|---|---|
| `SCANOPSIS_DATABASE_URL` | `SCANOPSIS_DATABASE_URL_FILE` |
| `SCANOPSIS_SECRET_KEY` | `SCANOPSIS_SECRET_KEY_FILE` |
| `SCANOPSIS_GITLAB_CLIENT_SECRET` | `SCANOPSIS_GITLAB_CLIENT_SECRET_FILE` |
| `SCANOPSIS_SONAR_TOKEN` | `SCANOPSIS_SONAR_TOKEN_FILE` |
| `SCANOPSIS_SONAR_WEBHOOK_SECRET` | `SCANOPSIS_SONAR_WEBHOOK_SECRET_FILE` |

Rules:

- Set only one of the two. If both are set, the server stops with
  `SCANOPSIS_DATABASE_URL and SCANOPSIS_DATABASE_URL_FILE are both set; set only one`
  (and the same for the other two).
- Line breaks at the end of the file are removed. Other whitespace is kept.
- If the file cannot be read, the server stops with `cannot be read` and the
  reason.

The Compose file uses this for the database URL and the secret key, with
Docker secrets mounted under `/run/secrets`. Docker mounts each secret file
read-only. The value never appears in `docker inspect`, in the environment or
in process listings. To seed the GitLab client secret from a file, add a
fourth secret to `compose.yaml` in the same way and set
`SCANOPSIS_GITLAB_CLIENT_SECRET_FILE=/run/secrets/gitlab_client_secret`. It
is simpler to type the secret on the Settings page, where it is stored
encrypted in the database.

## The secret key

`SCANOPSIS_SECRET_KEY` is a 32-byte key that Scanopsis uses to encrypt two
kinds of data in the database:

- The two-factor (TOTP) secret of every user.
- Secrets you enter on the Settings page, which today is the GitLab client
  secret.

Everything else is stored as it is, and passwords are stored as Argon2id
hashes that do not use the key.

Create the key once with `openssl rand -base64 32`. Do not change it
afterwards.

### Back up the key

Back up the key together with your database backups, but store it
separately. A database backup alone cannot be read without the key. A key
stored next to the backup protects nothing. Keep a copy in your password
manager or secrets vault, and write down which backup it belongs to.

### What happens if the key is lost or changed

- If a secret setting is saved, the server refuses to start. It logs an error
  that names the setting, and the container restarts in a loop:

  ```text
  setting gitlab.client_secret cannot be decrypted: SCANOPSIS_SECRET_KEY changed or is wrong
  ```

- If no secret setting is saved, the server starts, but every two-factor
  code fails with a server error, because the stored secrets cannot be
  decrypted. The log says `cannot decrypt TOTP secret; check SCANOPSIS_SECRET_KEY`.

### Recovery

1. **Restore the original key.** Put the backed-up key in `secrets/secret_key`
   and run `docker compose up -d --force-recreate scanopsis`. This is the
   only way that loses nothing.
2. **If the key is gone for good,** you must discard the data it protected:
   1. Delete the saved GitLab client secret. The server cannot start while it
      is unreadable, so remove it in the database:

      ```sh
      docker compose exec db psql -U scanopsis -d scanopsis \
        -c "DELETE FROM settings WHERE key = 'gitlab.client_secret'"
      ```

   2. Put the new key in `secrets/secret_key` and start the server with
      `docker compose up -d --force-recreate scanopsis`.
   3. Reset two-factor authentication for every local and GitLab user with
      the [administration command](operations.md#reset-two-factor-authentication),
      one account at a time. Each user enrols again at the next sign-in.
   4. Sign in as the local administrator, enrol two-factor authentication
      again, and enter the GitLab client secret again on the Settings page.
      GitLab sign-in stays off until you do, because the secret is missing.

## Rate limits

Scanopsis limits repeated attempts to make password and code guessing slow.
A limit allows a burst of attempts and then one more attempt at a fixed
interval. When you exceed it, the server answers `429` with the code
`too_many_attempts` and a `Retry-After` header, and the page shows "Too many
sign-in attempts. Wait N seconds and try again."

| Action | Counted for | Burst | Then |
|---|---|---|---|
| Local sign-in, every attempt | Client address | 10 | one every 12 seconds (5 per minute) |
| Local sign-in, failed attempts | Username | 10 | one every 12 seconds |
| First-run setup | Client address | 5 | one every 12 seconds |
| Two-factor code and enrolment confirmation | User | 5 | one every 30 seconds |
| Password change | User | 10 | one every 12 seconds |

A client address is an IPv4 address or an IPv6 `/64` range. The limits are
kept in memory, so a restart clears them.

Behind a reverse proxy, Scanopsis can tell clients apart only if you set
`SCANOPSIS_TRUSTED_PROXIES`. Without it, all users share the proxy's address
and share one limit.

Two further limits are fixed:

- After a failed attempt to reach GitLab, Scanopsis waits 10 seconds before it
  tries again.
- The state of a GitLab sign-in is kept in a cookie for 10 minutes. A user who
  takes longer sees `login_expired`.

## Outgoing TLS connections

Scanopsis checks the certificate of every server it connects to, which today
is GitLab. There is no option to turn this check off.

If your GitLab uses a certificate from an internal certificate authority:

1. Put the CA certificate, in PEM format, in a file, for example `ca.pem` in
   `deploy/compose`.
2. Add a `compose.override.yaml` next to `compose.yaml`. Compose reads it
   automatically:

   ```yaml
   services:
     scanopsis:
       volumes:
         - ./ca.pem:/etc/scanopsis/ca.pem:ro
   ```

3. Set `SCANOPSIS_CA_FILE=/etc/scanopsis/ca.pem` in `.env` and run
   `docker compose up -d`.

The startup log line `scanopsis starting` then shows `"custom_ca":true`. If
the file is missing, the server stops with `read SCANOPSIS_CA_FILE:` followed
by the reason. If it holds no certificate, the server stops with
`SCANOPSIS_CA_FILE /etc/scanopsis/ca.pem contains no PEM certificates`.

## Reverse proxy and TLS

Scanopsis speaks plain HTTP. In production, put a reverse proxy in front of
it that terminates TLS, and set `SCANOPSIS_PUBLIC_URL` to the HTTPS address of
the proxy. Then Scanopsis marks its cookies `Secure` and sends
`Strict-Transport-Security`.

The proxy must:

- Forward requests for `https://scanopsis.example.com` to `127.0.0.1:8080` on
  the Docker host.
- Set the `X-Forwarded-For` header to the address of the client.
- Pass the `Host` header and the request path and query unchanged.

The following nginx server block is an example to adapt. It was not run for
this guide, and the certificate paths are placeholders.

```nginx
server {
    listen 443 ssl;
    server_name scanopsis.example.com;
    ssl_certificate     /etc/ssl/scanopsis.example.com.pem;
    ssl_certificate_key /etc/ssl/scanopsis.example.com.key;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $remote_addr;
    }
}
```

### Tell Scanopsis which proxy to trust

Scanopsis sees the address the proxy connects from, which is not always the
address of the proxy host. With the published port in this Compose file, the
connection arrives from the gateway of the Compose network. Find the address by sending a request from the Docker host and reading the
log:

```sh
curl -s http://127.0.0.1:8080/healthz
docker compose logs scanopsis | tail -1
```

Read `client_ip` in the last line, for example:

```text
scanopsis-1  | {"time":"2026-11-19T04:08:24.795750469Z","level":"INFO","msg":"request","method":"GET","path":"/healthz","status":200,"duration_ms":0,"client_ip":"172.18.0.1"}
```

Lines with `"client_ip":"127.0.0.1"` come from the container's own health check,
so ignore them. Set the address you found in `.env`:

```text
SCANOPSIS_TRUSTED_PROXIES=172.18.0.1
```

Run `docker compose up -d`. Scanopsis then reads the client address from
`X-Forwarded-For` for requests that come from that address. For each
`X-Forwarded-For` it reads the entries from right to left and stops at the
first address that is not a trusted proxy. Entries further left are ignored,
because clients can forge them.

Verify with a request that carries a forwarded address:

```sh
curl -s -H 'X-Forwarded-For: 203.0.113.9' http://127.0.0.1:8080/healthz
docker compose logs scanopsis | tail -1
```

Expected result: the last log line shows `"client_ip":"203.0.113.9"`.

```text
scanopsis-1  | {"time":"2026-11-19T04:08:30.23870043Z","level":"INFO","msg":"request","method":"GET","path":"/healthz","status":200,"duration_ms":0,"client_ip":"203.0.113.9"}
```

If the address is still the gateway, the value in `SCANOPSIS_TRUSTED_PROXIES`
is wrong, or you did not run `docker compose up -d` after editing `.env`.
The startup line `scanopsis starting` shows `"trusted_proxies":1` when one
entry was read.

Do not trust a range that includes untrusted hosts. Anyone in a trusted range
can choose the client address that Scanopsis logs and rate-limits.
