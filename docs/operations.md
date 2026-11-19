# Operations

This page describes how to run and look after a Scanopsis deployment that you
created with the [getting started guide](getting-started.md). Run every
`docker compose` command in `deploy/compose`.

## Health checks

Scanopsis has two endpoints for monitoring. Neither needs a sign-in.

| Endpoint | Meaning | Healthy answer | Unhealthy answer |
|---|---|---|---|
| `/healthz` | Liveness. The process is running. It does not check the database. | `200` with `{"status":"ok"}` | No answer |
| `/readyz` | Readiness. The process can reach the database. | `200` with `{"status":"ready"}` | `503` with a status that names the failing part, without secrets |

Use `/healthz` to decide whether to restart the container. Use `/readyz` to
decide whether to send traffic to it.

```sh
curl -s http://127.0.0.1:8080/healthz
curl -s http://127.0.0.1:8080/readyz
```

Expected result:

```text
{"status":"ok"}
{"status":"ready"}
```

### The health check of the image

The container image has no shell and no `curl`, so it carries its own probe.
The command `scanopsis healthcheck` sends a request to
`http://127.0.0.1:<port>/healthz` inside the container, where the port comes
from `SCANOPSIS_LISTEN_ADDR` (default `8080`). It exits with status 0 when
the answer is `200`, and with status 1 otherwise, after printing the reason.

Docker runs it every 30 seconds with a timeout of 5 seconds, allows 20
seconds for the start, and marks the container unhealthy after 3 failures in
a row. The Compose file does not redefine this check.

Read the result:

```sh
docker inspect --format '{{.State.Health.Status}}' scanopsis-scanopsis-1
```

Expected result: `healthy`. The value is `starting` during the first 20
seconds and `unhealthy` after repeated failures.

Run the probe by hand:

```sh
docker compose exec scanopsis /scanopsis healthcheck; echo "exit=$?"
```

Expected result:

```text
exit=0
```

When the probe fails, it prints one line. This example forces a failure by
asking for a port where nothing listens:

```sh
docker compose exec -e SCANOPSIS_LISTEN_ADDR=:9999 scanopsis /scanopsis healthcheck; echo "exit=$?"
```

```text
healthcheck: Get "http://127.0.0.1:9999/healthz": dial tcp 127.0.0.1:9999: connect: connection refused
exit=1
```

If the container is `unhealthy`, read the log (see the next section). A
restart does not fix a database that is down. Check `docker compose ps db`
first.

Docker does not restart a container only because it is unhealthy. The
`restart: unless-stopped` policy restarts a container that has exited.

## Logs

Scanopsis writes one JSON object per line to standard error. Docker collects
it:

```sh
docker compose logs scanopsis
docker compose logs --follow --since 10m scanopsis
```

Every line has `time`, `level` and `msg`. Other fields depend on the event.
Example of a request line:

```text
{"time":"2026-11-19T04:08:24.795750469Z","level":"INFO","msg":"request","method":"GET","path":"/healthz","status":200,"duration_ms":0,"client_ip":"172.18.0.1"}
```

What the log contains:

- At start, one line `scanopsis starting` with the version and a summary of
  the configuration, and one line `scanopsis listening`.
- One `request` line for each request, with the method, the path, the status,
  the duration and the client address.
- Warnings and errors, such as `GitLab sign-in failed`, `GitLab discovery
  failed` and `readiness check failed`.
- While no administrator exists, one `WARN` line with the setup URL and the
  one-time setup token. This is the only secret that is logged.

What the log never contains: passwords, two-factor codes, recovery codes,
cookies, request headers, query strings, the database URL, the secret key and
the values of secret settings.

Security events, such as sign-ins, failed sign-ins, role changes, setting
changes and the commands in this page, are written to the `audit_log` table in
the database, not to the log. Administrators read them on the **Audit log**
page, newest first, filtered by action or actor. When the web interface is
not available, for example while you recover access, read them with SQL:

```sh
docker compose exec db psql -U scanopsis -d scanopsis \
  -c "SELECT at, actor, action, detail FROM audit_log ORDER BY id DESC LIMIT 5"
```

```text
              at               |    actor     |      action      |                                            detail
-------------------------------+--------------+------------------+-----------------------------------------------------------------------------------------------
 2026-11-19 04:09:06.246826+00 | cli          | mfa.reset        | {"source": "gitlab", "username": "alice"}
 2026-11-19 04:09:00.699604+00 | gitlab:alice | login.succeeded  | {"ip": "172.18.0.1", "method": "gitlab", "username": "alice", "gitlab_user_id": 101}
 2026-11-19 04:09:00.627176+00 | local:root   | settings.changed | {"keys": ["gitlab.client_secret"]}
```

### Log level

`SCANOPSIS_LOG_LEVEL` sets the verbosity: `debug`, `info` (default), `warn` or
`error`. Set it in `.env` and run `docker compose up -d`. At `warn`, the
request lines disappear. At `debug`, the server adds one line each time it
removes expired sessions, once an hour at most. Keep `info` unless you
investigate a problem.

Docker keeps logs without a size limit under its default `json-file` driver.
Configure log rotation in the Docker daemon or in `compose.yaml` for a
long-running deployment.

## Administration commands

Two commands run inside the container and work on the database directly. Use
them when the web interface is not available, for example when the only
administrator lost a password or a phone.

```sh
docker compose exec scanopsis /scanopsis admin <command>
```

The commands need only the database URL, which the container already has from
its secret. The server must have started once, so that the tables exist. Run
`docker compose exec scanopsis /scanopsis admin -h` for the built-in help.

Exit status: 0 on success, 1 on failure, 2 on a usage error. Every command
records an event with the actor `cli` in the audit table.

### Reset a password

Use this when a local user forgot the password. It works for local accounts
only. A GitLab user changes the password in GitLab.

```sh
docker compose exec scanopsis /scanopsis admin reset-password root
```

Expected result: the command prints a temporary password, once.

```text
The temporary password for the local account "root" is:

asASUqzdu7KKD6YK2vfn

It is shown only now and is not stored anywhere. The account was signed out everywhere, and the temporary password must be changed at the next sign-in.
```

What happens:

1. The old password stops working. All sessions of the user end.
2. Give the temporary password to the user over a safe channel.
3. The user signs in with it, and enters the two-factor code. The application
   then asks for a new password before anything else. The new password must
   follow the password rules (at least 12 characters, not the user name).
4. After the user changes the password, the temporary password is useless.

The reset does not touch two-factor authentication. A user who also lost the
phone needs the next command too.

If it fails:

| Message | Meaning and fix |
|---|---|
| `there is no local account named "alice"` | There is no local account with that name. Check the spelling. A user who signs in with GitLab has no password in Scanopsis. |
| `the database has no schema yet: run the server once first so it can create it.` | The server never started against this database. Start it with `docker compose up -d`. |
| `scanopsis admin: SCANOPSIS_DATABASE_URL (or SCANOPSIS_DATABASE_URL_FILE) is not set` | You ran the command outside the Compose container. Use `docker compose exec scanopsis`. |

### Reset two-factor authentication

Use this when a user lost the phone and has no recovery code left. Write the
account as `local:<username>` or `gitlab:<username>`. The prefix is required.

```sh
docker compose exec scanopsis /scanopsis admin reset-2fa local:root
```

Expected result:

```text
Two-factor authentication was reset for local:root. The account was signed out everywhere and enrols again at the next sign-in.
```

What happens:

1. The two-factor secret and all recovery codes of the account are deleted.
2. All sessions of the account end.
3. At the next sign-in, the user passes the first step (password, or GitLab)
   and then sees the page **Set up two-factor sign-in**. The user scans a new
   QR code and receives 10 new recovery codes.

The account must prove its first factor again. For a GitLab account, the
group check also runs again.

An administrator who can still sign in can do the same on the **Users** page
with the button **Reset two-factor** next to the user.

To reset several accounts, run the command once for each account. There is no
option for all accounts.

If it fails:

| Message | Meaning and fix |
|---|---|
| `the account must be written local:<username> or gitlab:<username>.` | You wrote only the name, for example `root`. Add the prefix, exit status 2. |
| `there is no gitlab account named "alice"` (or `local`) | The account does not exist. A GitLab account exists only after its owner signed in once. |

## Recovering access when locked out

Find the case that matches, then follow its steps.

| Situation | What to do |
|---|---|
| A user lost the phone and has a recovery code | On the page **Enter your code**, select **Use a recovery code** and enter one code. The code works once. After sign-in, nothing needs resetting. |
| A user lost the phone and has no recovery code | An administrator selects **Reset two-factor** next to the user on the **Users** page. If no administrator can sign in, run `reset-2fa` as shown above. The user enrols again at the next sign-in. |
| A local user forgot the password | Run `reset-password`. If the user also lost the phone, run `reset-2fa` as well. |
| The only administrator lost both password and phone | Run `reset-password root` and `reset-2fa local:root` (use the real user name). Sign in with the temporary password, enrol again, and set a new password. |
| A GitLab user cannot sign in | Check the [troubleshooting table](integrations/gitlab-sso.md#troubleshooting). Local sign-in still works for local accounts while GitLab is down. |
| Local sign-in is off and GitLab sign-in does not work | Turn local sign-in on again in the database, as described next. |
| The server does not start and the log mentions `SCANOPSIS_SECRET_KEY` | See [the secret key](configuration.md#the-secret-key). |

### Turn local sign-in on again

Local sign-in can only be off while GitLab sign-in is fully configured; if it
is not, the server refuses to start and its log says what to change. You can
still end up without a way in when local sign-in is off and GitLab sign-in
stops working, for example because GitLab is down or the client secret is
wrong. GitLab then shows an error, or the sign-in page reports that GitLab
could not be reached.

1. If `.env` contains `SCANOPSIS_LOCAL_LOGIN_ENABLED=false`, delete that line.
   Otherwise the value is saved again at the next start.
2. Delete the saved value in the database. This makes the setting fall back to
   its default, which is on:

   ```sh
   docker compose exec db psql -U scanopsis -d scanopsis \
     -c "DELETE FROM settings WHERE key = 'auth.local_login_enabled'"
   ```

   Expected result: `DELETE 1`.

3. Restart Scanopsis, because it reads this setting at start when it is changed
   outside the Settings page:

   ```sh
   docker compose up -d --force-recreate scanopsis
   ```

4. Check the sign-in methods:

   ```sh
   curl -s http://127.0.0.1:8080/api/v1/auth/methods
   ```

   Expected result: `"local":true`.

```text
{"gitlab":false,"local":true}
```

Sign in as a local administrator. If you lost the password or the phone, use
the commands above first.

## Data retention

Scanopsis deletes old data once a day so that the database does not grow
without limit.

### What is kept

- Scan history for the number of days in the `retention.days` setting. The
  default is 90.
- The latest scan of every service, ref, artifact and kind, however old. A
  branch that nobody has scanned for two years still shows its last result.
  Older scans of the same service, ref, artifact and kind are deleted when
  they are older than the period.
- The latest SonarQube analysis of every service, however old.
- Audit events for 365 days. This period is fixed and has no setting. If you
  set `retention.days` above 365, audit events are kept for that many days
  instead, so they are never kept for a shorter time than scan history.

### What is deleted

Each run deletes, in this order:

1. Scans that are older than the period and are not the latest of their
   service, ref, artifact and kind. Their findings are deleted with them.
2. SonarQube analyses that are older than the period and are not the latest
   of their service.
3. SonarQube webhook deliveries that are older than the period. This includes
   deliveries that never finished.
4. Audit events that are older than 365 days, or older than the period if
   that is longer.

A scan is as old as its `scanned_at` time, which is when Trivy ran, not when
the report arrived. A scan that is exactly as old as the period is kept.

### When it runs

The first run starts one minute after the server starts. After that, it runs
every 24 hours. The server stays available while it runs. It deletes at most
1,000 rows at a time, each in its own short transaction, and uses one
database connection. If you run more than one Scanopsis server against the
same database, only one of them runs the cleanup at a time; the others skip
that run.

### Change the period

Open **Settings**, find **Retention**, enter a whole number from 7 to 3650
and select **Save changes**. You can also seed the value with
`SCANOPSIS_RETENTION_DAYS` before the first start. After the first start, the
database value wins, as for the other settings. See
[runtime settings](configuration.md#runtime-settings).

The new value applies from the next run. Saving it does not start a run. If
you shorten the period, the next run deletes everything that is now outside
it, in batches, so the first run after a large reduction can take longer than
usual.

Deleted data cannot be brought back, except by restoring a [backup](#backup-and-restore).

### Check the last run

Each run writes one line at the `info` level when it finishes:

```sh
docker compose logs scanopsis | grep "data retention"
```

```text
{"time":"2026-11-19T04:01:12.318Z","level":"INFO","msg":"data retention run finished","retention_days":90,"scans_deleted":41200,"sonar_analyses_deleted":3100,"sonar_deliveries_deleted":3100,"audit_events_deleted":12,"failed_steps":0,"duration":"48.2s"}
```

`failed_steps` is the number of the four steps above that stopped with an
error. For each one, a separate `WARN` line `data retention step failed`
names the step and the error. The rows deleted before the error stay deleted,
and the next run continues from there. When another server holds the lock, the
run is skipped and logs only at the `debug` level.

A run that deleted anything also writes an audit event, **Old data deleted**
(`retention.deleted`, actor `retention`), with the period, the two cutoffs
and the number of rows deleted of each kind. Open **Audit log** and filter by
this action to see the history of deletions, including how many audit events
were removed.

## Backup and restore

Back up two things, and keep them apart:

| What | How | Where to keep it |
|---|---|---|
| The database | `pg_dump`, as below | Your normal backup storage |
| `secrets/secret_key` | Copy the file | A password manager or secrets vault, not next to the database backups |

The database holds users, sessions, settings and the audit log. Secret values
in it, such as two-factor secrets and the GitLab client secret, are encrypted
with the secret key, so a restored database is only usable with the same key.
See [the secret key](configuration.md#the-secret-key). Also keep a copy of
`.env` and `compose.yaml` and the other two secret files, so that you can
rebuild the host.

A backup file contains password hashes and encrypted secrets. Protect it like
a secret.

### Back up the database

```sh
(umask 077; docker compose exec -T db pg_dump -U scanopsis -d scanopsis \
  --format=custom > "scanopsis-$(date +%F).dump")
```

The `umask` makes the file readable only by you. The `-T` option keeps Docker
from adding terminal characters to the output. The backup does not stop the
server.

Verify that the file is a valid archive:

```sh
docker compose exec -T db pg_restore --list < scanopsis-2026-11-19.dump | head -8
```

Expected result: a header that starts with `Archive created at` and ends with
`Format: CUSTOM`, then the list of objects.

```text
;
; Archive created at 2026-11-19 04:09:24 UTC
;     dbname: scanopsis
;     TOC Entries: 35
;     Compression: gzip
;     Dump Version: 1.16-0
;     Format: CUSTOM
;     Integer: 4 bytes
```

If Compose answers `no configuration file provided: not found`, you are not
in `deploy/compose`. If it answers `service "db" is not running`, the database
container is stopped: run `docker compose up -d db`. If the backup file is
empty, read the error that `pg_dump` printed.

Run the backup on a schedule, and copy the files to storage on another host.
Test a restore regularly.

### Restore into the existing database

Use this to go back to an earlier state of a running installation.

1. Stop Scanopsis, so that nothing writes while you restore. The database
   keeps running.

   ```sh
   docker compose stop scanopsis
   ```

2. Restore the backup over the existing data:

   ```sh
   docker compose exec -T db pg_restore -U scanopsis -d scanopsis \
     --clean --if-exists --no-owner --exit-on-error < scanopsis-2026-11-19.dump
   echo "exit=$?"
   ```

   Expected result: no output from `pg_restore` and `exit=0`. The option
   `--clean` removes the current tables first, so you lose everything that
   changed after the backup.

3. Start Scanopsis again and check it:

   ```sh
   docker compose start scanopsis
   docker compose ps
   ```

   Expected result: both containers are `healthy` after about 30 seconds.

4. Sign in. Every change after the backup is gone. For example, a password
   that was reset after the backup is back to the old one.

### Restore on a new host or an empty volume

Use this after a disaster, or to move Scanopsis to a new host.

1. Prepare the host as in the getting started guide, up to and including the
   configuration file (steps 1 to 3). Use the **same** `secrets/secret_key`
   that the backup belongs to. The other two secret files can be new. If you
   create a new `secrets/postgres_password`, update `secrets/database_url` to
   match.
2. Start only the database and wait until it is healthy:

   ```sh
   docker compose up -d db
   docker compose ps db
   ```

3. Restore the backup:

   ```sh
   docker compose exec -T db pg_restore -U scanopsis -d scanopsis \
     --clean --if-exists --no-owner --exit-on-error < scanopsis-2026-11-19.dump
   echo "exit=$?"
   ```

   Expected result: `exit=0`.

4. Start Scanopsis:

   ```sh
   docker compose up -d
   ```

5. Check that it works: `curl -s http://127.0.0.1:8080/readyz` answers
   `{"status":"ready"}`. Sign in with an existing account and enter a
   two-factor code. The log has no `setup_token` line, because the restored
   database already has an administrator.

If it fails:

| Symptom | Meaning and fix |
|---|---|
| `pg_restore` reports errors about roles or owners | You restored without `--no-owner`. Add it. |
| `pg_restore` cannot connect, or Scanopsis cannot connect after the restore | The database name, user or password differs. Compare `secrets/database_url` with `secrets/postgres_password`. |
| Scanopsis restarts in a loop and the log says `cannot be decrypted: SCANOPSIS_SECRET_KEY changed or is wrong` | The key is not the one that belongs to this backup. Restore the original key. See [the secret key](configuration.md#the-secret-key). |
| Sign-in works but two-factor codes give a server error | The same cause: wrong key, and no secret setting was saved. Restore the original key. |
