# Configuration reference

This page lists every setting of Scanopsis: what it does, its default, and how to change it. Use it after the [getting started guide](getting-started.md), whenever you need to look something up.

**Audience:** Operators · **Back to:** [README](../README.md) · [All guides](../README.md#documentation)

---

## Two kinds of settings

**Environment settings** configure the process: where it listens, which
database it uses, how it logs. You set them as environment variables; in the
Compose deployment they live in `.env` and in the secret files. Scanopsis
reads them once at start, so restart the container after a change
(`docker compose up -d`).

**Runtime settings** configure what administrators change while the server
runs: GitLab sign-in, local sign-in, session timeouts, scan uploads,
SonarQube and data retention. You change them on the **Settings** page, which
only administrators see. A change applies without a restart.

Every runtime setting also has an environment variable, but that variable
only **seeds** it:

1. At start, Scanopsis saves the environment value of each runtime setting
   that has no saved value yet.
2. From then on, the saved value wins. Changing or removing the variable has
   no effect on a setting that is already saved.
3. A change on the Settings page replaces the saved value.
4. **Reset to environment or default**, then save, deletes the saved value.
   The setting falls back to the variable if it is set, else to the default.

The Settings page shows where each value comes from:

| Source | Meaning |
| --- | --- |
| Default | Nothing is saved and no variable is set. The built-in default applies. |
| Environment | The value came from the variable and nobody changed it on the Settings page. |
| Set here | An administrator saved it on the Settings page. The page shows who and when. |

In short: use the environment to set up a new deployment from a file, and the
Settings page for everything after that.

## Environment settings

All variables start with `SCANOPSIS_`. An empty value counts as not set.

| Variable | Default | Example | What it does |
| --- | --- | --- | --- |
| `SCANOPSIS_DATABASE_URL` | required | `postgres://scanopsis:pw@db:5432/scanopsis?sslmode=disable` | PostgreSQL connection URL. Scanopsis creates and updates its tables at start. Compose uses the file variant instead, see [file variants](#file-variants-and-docker-secrets). |
| `SCANOPSIS_SECRET_KEY` | required | output of `openssl rand -base64 32` | Base64 of exactly 32 random bytes. Encrypts secrets in the database. Compose uses the file variant. See [the secret key](#the-secret-key). |
| `SCANOPSIS_PUBLIC_URL` | required | `https://scanopsis.example.com` | The address users and pipelines use to reach Scanopsis. See the rules below. |
| `SCANOPSIS_LISTEN_ADDR` | `:8080` | `:8080` | The address the server listens on. The image health check reads the port from it. |
| `SCANOPSIS_TRUSTED_PROXIES` | empty | `172.18.0.1,10.0.0.0/8` | Reverse proxies whose `X-Forwarded-For` header Scanopsis believes: a comma-separated list of IP addresses or CIDR ranges. See [trust your reverse proxy](#trust-your-reverse-proxy). |
| `SCANOPSIS_CA_FILE` | empty | `/etc/scanopsis/ca.pem` | A PEM file with extra certificate authorities to trust for GitLab and SonarQube. See [outgoing TLS connections](#outgoing-tls-connections). |
| `SCANOPSIS_LOG_LEVEL` | `info` | `debug` | Log detail: `debug`, `info`, `warn` or `error`. |

`SCANOPSIS_PUBLIC_URL` must be an absolute `http` or `https` URL without a
path, query, fragment or credentials. A port is fine
(`https://scanopsis.example.com:8443`), and a trailing slash is removed.
Scanopsis must run at the root of its own host name: a sub-path such as
`https://example.com/scanopsis` does not work. With `https`, cookies get the
`Secure` flag and the server sends `Strict-Transport-Security`. The GitLab
redirect URI is this URL plus `/api/v1/auth/gitlab/callback`.

The Compose deployment also reads `SCANOPSIS_IMAGE` (required: the image to
run), `SCANOPSIS_BIND` and `SCANOPSIS_PORT` (where the port is published,
default `127.0.0.1` and `8080`) and `TZ` (the time zone of log timestamps,
default UTC) from `.env`. Scanopsis itself does not read them.

### When a value is wrong

If any variable is invalid, the server does not start. It prints
`configuration error:` and one line per problem, so you can fix them all at
once:

```text
configuration error:
SCANOPSIS_PUBLIC_URL must not contain a path, query, fragment or credentials, got "https://scanopsis.example.com/app"
SCANOPSIS_TRUSTED_PROXIES contains "foo", which is not an IP address or CIDR range
SCANOPSIS_SECRET_KEY is not valid (key must be 32 bytes, got 2); generate one with: openssl rand -base64 32
```

Run `docker compose logs scanopsis` to see it.

### Database URL parameters

Scanopsis sets three defaults for its database connections. Add the parameter
to the database URL to change one, for example
`postgres://scanopsis:pw@db:5432/scanopsis?sslmode=disable&pool_max_conns=24`.

| Parameter | Default | What it does |
| --- | --- | --- |
| `pool_max_conns` | `16` | The most connections one Scanopsis process opens. It also caps how many API requests run at once: more requests wait up to 3 seconds for a slot, then get `503 busy`. Raise it only if the database allows enough `max_connections`: Scanopsis needs one more for the readiness probe, and keep a few spare for backups and administration. |
| `statement_timeout` | `30000` (milliseconds) | PostgreSQL cancels a statement that runs longer, and the request fails. `0` turns the limit off. An `options=-c statement_timeout=...` parameter counts too. |
| `jit` | `off` | PostgreSQL's just-in-time compilation. Scanopsis turns it off because its statements are short: in one measurement, PostgreSQL spent 250 ms compiling a search query that runs in 17 ms. |

Schema migrations ignore `statement_timeout` and the 30-second start-up limit,
because an upgrade can build indexes on large tables. The index builds do
not block writes, so scans keep arriving while they run. The log shows when
each migration starts and ends. A migration that cannot get a table lock
within 10 seconds stops the start; the next start continues where it stopped.

## Runtime settings

The key is the name on the Settings page. Every value is checked when you
save, and if one is invalid, nothing is saved. Each saved change is recorded
in the audit log as `settings.changed`, with the old and new value of each
setting (for a secret, only that it changed).

### GitLab and sign-in

| Key | Seeding variable | Default | What it does |
| --- | --- | --- | --- |
| `gitlab.url` | `SCANOPSIS_GITLAB_URL` | empty | Base URL of your GitLab, for example `https://gitlab.example.com`. Scanopsis uses it to verify CI job tokens and for GitLab sign-in, so set it even if you only want scan uploads. Same URL rules as the public URL. GitLab users are tied to this URL: after a change, they sign in as new viewers. |
| `gitlab.client_id` | `SCANOPSIS_GITLAB_CLIENT_ID` | empty | Application ID of the GitLab OAuth application. |
| `gitlab.client_secret` | `SCANOPSIS_GITLAB_CLIENT_SECRET` | not set | Secret of the GitLab OAuth application. Stored encrypted and never shown again. It cannot be empty; reset it instead. |
| `gitlab.allowed_groups` | `SCANOPSIS_GITLAB_ALLOWED_GROUPS` | empty | GitLab groups, by full path, whose members may sign in, for example `platform,security/appsec`. Empty lets every GitLab user in. |
| `auth.local_login_enabled` | `SCANOPSIS_LOCAL_LOGIN_ENABLED` | `true` | Whether users can sign in with a local username and password. In the environment, write `true` or `false` (`1` and `0` work too). |
| `session.idle_timeout` | `SCANOPSIS_SESSION_IDLE_TIMEOUT` | `30m` | How long a session may stay unused before the user signs in again. At least `5m`. |
| `session.absolute_timeout` | `SCANOPSIS_SESSION_ABSOLUTE_TIMEOUT` | `12h` | The longest a session lives, however active. Not shorter than the idle timeout. |

The [GitLab sign-in guide](integrations/gitlab-sso.md) walks you through
these settings.

### Scan uploads

| Key | Seeding variable | Default | What it does |
| --- | --- | --- | --- |
| `ingest.allowed_namespaces` | `SCANOPSIS_INGEST_ALLOWED_NAMESPACES` | empty | GitLab groups, by full path, whose projects may upload Trivy reports; subgroups are included. Empty accepts every project with a valid CI job token. |
| `ingest.protected_refs_only` | `SCANOPSIS_INGEST_PROTECTED_REFS_ONLY` | `false` | Accept reports only from pipelines on protected branches and tags. |

The [GitLab CI guide](integrations/gitlab-ci.md) shows the pipeline side.

### SonarQube

| Key | Seeding variable | Default | What it does |
| --- | --- | --- | --- |
| `sonar.url` | `SCANOPSIS_SONAR_URL` | empty | Base URL of your SonarQube server. Same URL rules as the GitLab URL. Prefer `https`: the token travels with every request. |
| `sonar.token` | `SCANOPSIS_SONAR_TOKEN` | not set | Token of a SonarQube user with Browse permission on the projects. Stored encrypted and never shown again. |
| `sonar.webhook_secret` | `SCANOPSIS_SONAR_WEBHOOK_SECRET` | not set | Secret of the SonarQube webhook. Deliveries without a valid signature are rejected. Stored encrypted and never shown again. |
| `sonar.sync_interval` | `SCANOPSIS_SONAR_SYNC_INTERVAL` | `15m` | How often linked SonarQube projects are refreshed, in case a webhook was missed. At least `1m`. |

The [SonarQube guide](integrations/sonarqube.md) shows how to connect it.

### Data retention

| Key | Seeding variable | Default | What it does |
| --- | --- | --- | --- |
| `retention.days` | `SCANOPSIS_RETENTION_DAYS` | `90` | How many days scan history, SonarQube analyses and SonarQube deliveries are kept: a whole number from `7` to `3650`. The latest scans of the branch a service's dashboards show and of protected branches and tags, and the latest analysis of every service, are kept however old they are. |

The [operations guide](operations.md) explains data retention in full.

### How to write values

- **Durations** have units: `30m`, `8h`, `1h30m`.
- **Whole numbers** are plain: `90`. A fraction, a unit or a value outside the
  range is rejected.
- **Lists** in the environment are comma-separated. On the Settings page, write
  one entry per line. A group path must not be empty, must not start or end
  with `/`, and must not contain spaces.

### Rules that span several settings

The Settings page checks these rules every time you save, and shows the
message next to the field:

| Rule | Message on the Settings page |
| --- | --- |
| The GitLab URL alone is enough for scan uploads. GitLab sign-in also needs the client ID and secret: set one of them and you must set the URL and the other one. | "Required when other GitLab sign-in settings are set." |
| SonarQube needs the URL and the token together. The webhook secret needs both. | "Required when other SonarQube settings are set." |
| The sync interval is at least one minute. | "Must be at least 1m." |
| When you change the GitLab URL while a client secret is stored, or the SonarQube URL while a token is stored, type the secret again in the same save. Scanopsis never sends a stored secret to a new address. | "Enter this value again when you change the URL, so the stored one is never sent to a new address." |
| Local sign-in can be off only while GitLab sign-in is fully set up, so nobody is locked out. | "Local sign-in can be disabled only when GitLab sign-in is fully configured." |
| The idle timeout is at least five minutes. | "Must be at least 5m." |
| The absolute timeout is not shorter than the idle timeout. | "Must not be shorter than the idle timeout." |
| The retention period is a whole number of days from 7 to 3650. | "Must be a whole number from 7 to 3650." |

At start, Scanopsis checks the environment values against the GitLab,
SonarQube, session, retention and local sign-in rules, and refuses to start
when one is broken. Until GitLab sign-in is fully set up, the sign-in page
does not offer it and Scanopsis does not contact GitLab for sign-in.

> **Do not lock yourself out.** If the settings at start would leave no way to
> sign in, for example `SCANOPSIS_LOCAL_LOGIN_ENABLED=false` without GitLab
> sign-in, the server does not start and its log names the fix:
>
> ```text
> local sign-in is disabled but GitLab sign-in is not fully configured, so nobody could sign in: remove SCANOPSIS_LOCAL_LOGIN_ENABLED=false from the environment, or configure GitLab sign-in, then start again
> ```
>
> If local sign-in is off and GitLab later stops working (GitLab is down, or
> the client secret is wrong), nobody can sign in. The
> [operations guide](operations.md) explains how to recover access.

### When a change takes effect

- GitLab settings and the local sign-in switch: at the next request.
- The idle timeout: at the next request of every session, including existing
  ones.
- The absolute timeout: for sessions that start after the change. It is fixed
  when a session starts.
- The retention period: at the next cleanup run. Saving it does not start a
  run.

The session defaults, 30 minutes idle and 12 hours in total, follow OWASP ASVS
level 2. The session cookie has no expiry date of its own: the browser deletes
it when it closes, and the server ends the session after either timeout even
if the browser stays open. Version 0.1.0 used 8 and 24 hours. A database that
already stores a value for either setting keeps it; reset the setting on the
Settings page to use the new default.

## File variants and Docker secrets

Five variables also accept a `_FILE` variant: the path of a file whose content
is the value. This keeps secrets out of the environment, `docker inspect` and
process listings.

| Variable | File variant |
| --- | --- |
| `SCANOPSIS_DATABASE_URL` | `SCANOPSIS_DATABASE_URL_FILE` |
| `SCANOPSIS_SECRET_KEY` | `SCANOPSIS_SECRET_KEY_FILE` |
| `SCANOPSIS_GITLAB_CLIENT_SECRET` | `SCANOPSIS_GITLAB_CLIENT_SECRET_FILE` |
| `SCANOPSIS_SONAR_TOKEN` | `SCANOPSIS_SONAR_TOKEN_FILE` |
| `SCANOPSIS_SONAR_WEBHOOK_SECRET` | `SCANOPSIS_SONAR_WEBHOOK_SECRET_FILE` |

- Set only one of the pair. With both, the server stops with, for example,
  `SCANOPSIS_DATABASE_URL and SCANOPSIS_DATABASE_URL_FILE are both set; set only one`.
- Line breaks at the end of the file are removed. Other whitespace is kept.
- If the file cannot be read, the server stops with `cannot be read` and the
  reason.

The Compose file mounts the database URL and the secret key as Docker secrets
under `/run/secrets`. You can add a secret for the GitLab client secret the
same way and set
`SCANOPSIS_GITLAB_CLIENT_SECRET_FILE=/run/secrets/gitlab_client_secret`, but
typing it on the Settings page is simpler: it is stored encrypted there.

## The secret key

`SCANOPSIS_SECRET_KEY` is a 32-byte key that encrypts two kinds of data in the
database:

- The two-factor (TOTP) secret of every user.
- The secret settings: the GitLab client secret, the SonarQube token and the
  SonarQube webhook secret, wherever they came from.

Passwords are stored as Argon2id hashes and do not use the key. Everything
else is stored as it is.

Create the key once with `openssl rand -base64 32` and never change it.

### Back up the key

Keep a copy of the key in your password manager or secrets vault, apart from
your database backups. A database backup cannot be fully used without the
key, and a key stored next to the backup protects nothing. Note which backup
the key belongs to.

### If the key is lost or changed

- If a secret setting is saved, the server refuses to start and restarts in a
  loop. The log names the setting:

  ```text
  setting gitlab.client_secret cannot be decrypted: SCANOPSIS_SECRET_KEY changed or is wrong
  ```

- If no secret setting is saved, the server starts, but every two-factor code
  fails with a server error. The log says
  `cannot decrypt TOTP secret; check SCANOPSIS_SECRET_KEY`.

### Recover

1. **Put the original key back.** Copy it into `secrets/secret_key` and run
   `docker compose up -d --force-recreate scanopsis`. This is the only way
   that loses nothing.
2. **If the key is gone for good,** discard the data it protected:
   1. Delete the saved secret settings, because the server cannot start while
      one of them is unreadable:

      ```sh
      docker compose exec db psql -U scanopsis -d scanopsis \
        -c "DELETE FROM settings WHERE secret_enc IS NOT NULL"
      ```

   2. Put a new key in `secrets/secret_key` and run
      `docker compose up -d --force-recreate scanopsis`.
   3. Reset two-factor authentication for every user, one at a time, with
      `scanopsis admin reset-2fa` (see the [operations guide](operations.md)).
      Each user enrols again at the next sign-in.
   4. Sign in as the local administrator, enrol again, and type the GitLab
      client secret, the SonarQube token and the SonarQube webhook secret again
      on the Settings page. GitLab sign-in and SonarQube stay off until you do.
      If these values are still set in the environment, the server seeds them
      again at start instead.

## Rate limits

Scanopsis slows down password and code guessing. Each limit allows a burst of
attempts, then one more attempt at a fixed interval. Above the limit, the
server answers `429` with the code `too_many_attempts` and a `Retry-After`
header, and the page says "Too many sign-in attempts. Wait N seconds and try
again."

| Action | Counted per | Burst | Then |
| --- | --- | --- | --- |
| Local sign-in, every attempt | Client address | 10 | One every 12 seconds |
| Local sign-in, failed attempts | Username | 10 | One every 12 seconds |
| First-run setup | Client address | 5 | One every 12 seconds |
| Two-factor code and enrolment confirmation | User | 5 | One every 30 seconds |
| Password change | User | 10 | One every 12 seconds |

For sign-in and setup, a client address is an IPv4 address or an IPv6 `/48`
range, because one client often holds many `/64` ranges. Scan uploads count
an IPv6 client by its `/64`. The limits live in memory, so a restart clears
them. Flooding the server with made-up user names does not reset the budget
of a real one.

Behind a reverse proxy, Scanopsis tells clients apart only when you set
`SCANOPSIS_TRUSTED_PROXIES`. Without it, every user shares the proxy's address
and one limit.

Two more limits are fixed:

- After a failed attempt to reach GitLab, Scanopsis waits 10 seconds before it
  tries again.
- A GitLab sign-in must finish within 10 minutes. A user who takes longer
  sees `login_expired`.

## Outgoing TLS connections

Scanopsis checks the certificate of GitLab and SonarQube on every connection.
You cannot turn this check off. If one of them uses a certificate from an
internal certificate authority:

1. Save the CA certificate in PEM format as `ca.pem` in `deploy/compose`.
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

The log line `scanopsis starting` now shows `"custom_ca":true`. If the file is
missing, the server stops with `read SCANOPSIS_CA_FILE:` and the reason. If it
holds no certificate, it stops with
`SCANOPSIS_CA_FILE /etc/scanopsis/ca.pem contains no PEM certificates`.

The CA file does not apply to the database connection. The
[deployment guide](deployment.md) covers TLS to an existing PostgreSQL.

## Trust your reverse proxy

In production, a reverse proxy in front of Scanopsis terminates TLS. The
[deployment guide](deployment.md) has tested proxy examples. The proxy must
pass the `Host` header unchanged and set `X-Forwarded-For` to the client's
address. Then tell Scanopsis which proxy to believe:

1. Find the address the proxy connects from. With the port that Compose
   publishes, that is the gateway of the Compose network. Send a request from
   the Docker host and read the log:

   ```sh
   curl -s http://127.0.0.1:8080/healthz
   docker compose logs scanopsis | tail -1
   ```

   Read `client_ip` in the last line, for example `"client_ip":"172.18.0.1"`.
   Ignore lines with `"client_ip":"127.0.0.1"`: they come from the
   container's own health check.

2. Set it in `.env` and apply it:

   ```text
   SCANOPSIS_TRUSTED_PROXIES=172.18.0.1
   ```

   ```sh
   docker compose up -d
   ```

   The log line `scanopsis starting` shows `"trusted_proxies":1`.

3. Check with a forwarded address:

   ```sh
   curl -s -H 'X-Forwarded-For: 203.0.113.9' http://127.0.0.1:8080/healthz
   docker compose logs scanopsis | tail -1
   ```

   The last log line shows `"client_ip":"203.0.113.9"`. If it still shows the
   gateway, the address in `SCANOPSIS_TRUSTED_PROXIES` is wrong, or you did
   not run `docker compose up -d` after editing `.env`.

Scanopsis reads `X-Forwarded-For` from right to left and stops at the first
address that is not a trusted proxy. Entries further left are ignored, because
clients can forge them. Never trust a range that includes hosts you do not
control: anyone in it can choose the client address that Scanopsis logs and
rate-limits.

---

[Back to the README](../README.md) · [All guides](../README.md#documentation) · Found a mistake? [Open an issue](https://github.com/siyamsarker/scanopsis/issues)
