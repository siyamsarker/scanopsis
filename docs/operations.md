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
 2026-11-19 04:09:00.627176+00 | local:root   | settings.changed | {"keys": ["gitlab.client_secret"], "changes": [{"key": "gitlab.client_secret", "changed": true}]}
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

Five commands run inside the container and work on the database directly.
Use them when the web interface is not available, for example when the only
administrator lost a password or a phone.

| Command | What it does |
|---|---|
| `reset-password <username>` | New temporary password for a local account. |
| `reset-2fa <account>` | Removes the two-factor setup of an account. |
| `disable-user <account>` | Refuses every sign-in of an account and ends its sessions. |
| `enable-user <account>` | Lets a disabled account sign in again. |
| `delete-user <account>` | Deletes an account. |

An account is written in one of these ways:

| Form | Account |
|---|---|
| `local:<username>` | The local account with that name. |
| `gitlab:<username>` | The GitLab account with that name. If the name matches more than one account (users of different GitLab instances, see [GitLab users and the GitLab URL](#gitlab-users-and-the-gitlab-url)), the command changes nothing and lists the accounts, each as `gitlab-id:<n>`. |
| `gitlab-id:<n>` | The GitLab account with the GitLab user id `n` (shown on the user's GitLab profile). If two GitLab instances have a user with that id, the command lists them, each as `id:<n>`. |
| `id:<n>` | The account with the Scanopsis user id `n`, as in `GET /api/v1/users`. |

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
account as described in [administration commands](#administration-commands),
for example `local:root` or `gitlab:alice`. The prefix is required.

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
| `the account must be written local:<username>, gitlab:<username>, gitlab-id:<n> or id:<n>.` | You wrote only the name, for example `root`. Add the prefix, exit status 2. |
| `gitlab:alice matches 2 accounts; name one of them:` followed by a list | Users of two GitLab instances are called `alice`. Run the command again with the `gitlab-id:<n>` of the right one from the list. |
| `there is no gitlab account named "alice"` (or `local`) | The account does not exist. A GitLab account exists only after its owner signed in once. |

## Disable or delete a user

Disable an account to take access away at once, for example when someone
leaves or an account may be compromised. Delete an account that should not
appear on the **Users** page any more.

| | Disable | Delete |
|---|---|---|
| Sign-in | Refused, with the password and with GitLab, until the account is enabled again. The sign-in page says the account is disabled. | A local account no longer exists. A GitLab user who signs in again (and passes the group check) comes back as a new viewer. |
| Sessions | All end at once. | All end, with the account. |
| Role, two-factor setup, recovery codes | Kept, and in effect again after enabling. | Removed. |
| Audit log | `user.disabled`, `user.enabled` | `user.deleted`. Earlier events keep the name they were recorded with. |

To keep a GitLab user out for good, disable the account rather than deleting
it, or remove the user from the allowed GitLab groups.

On the **Users** page, an administrator selects **Disable** (or **Enable**)
or **Delete** next to the user and confirms. Two rules always apply:

- You cannot disable or delete your own account. Ask another administrator.
- The last enabled administrator can be neither disabled, deleted nor
  demoted. Enable or promote another administrator first.

The same works from the command line, for example when the account to disable
is the only administrator who can still sign in:

```sh
docker compose exec scanopsis /scanopsis admin disable-user gitlab:alice
docker compose exec scanopsis /scanopsis admin enable-user gitlab:alice
docker compose exec scanopsis /scanopsis admin delete-user local:olduser
```

Expected result, for example:

```text
The account gitlab:alice was disabled and signed out everywhere.
```

If it fails:

| Message | Meaning and fix |
|---|---|
| `local:root is the last enabled administrator; enable or promote another administrator first` | The change would leave nobody who can administer Scanopsis. Enable or promote another administrator, then run the command again. |
| `there is no gitlab account named "alice"` (or `local`) | The account does not exist. Check the spelling and the prefix. |

## GitLab users and the GitLab URL

A GitLab user is identified by the GitLab instance (the GitLab URL at the time
of the sign-in) together with the user id there. A user id of another GitLab
instance is another person, so a sign-in through a different GitLab URL never
uses an existing account, its role or its two-factor setup: it creates a new
viewer.

When the GitLab URL setting changes, every GitLab user therefore starts over
as a new viewer at the next sign-in, and the old accounts stay on the
**Users** page. Delete them once they are no longer needed.

If the same GitLab instance only moved to a new address, you can keep the
accounts by moving them to the new URL in the database, before anyone signs
in through it. Write both URLs exactly as the Settings page shows them:

```sh
docker compose exec db psql -U scanopsis -d scanopsis -c \
  "UPDATE users SET gitlab_issuer = 'https://gitlab.new.example.com' WHERE gitlab_issuer = 'https://gitlab.example.com';"
```

GitLab users created by version 0.1.0 are assigned to the GitLab URL stored in
the settings during the upgrade. Without a stored URL they get an empty one,
and their next sign-in creates a new account.

## Recovering access when locked out

Find the case that matches, then follow its steps.

| Situation | What to do |
|---|---|
| A user lost the phone and has a recovery code | On the page **Enter your code**, select **Use a recovery code** and enter one code. The code works once. After sign-in, nothing needs resetting. |
| A user lost the phone and has no recovery code | An administrator selects **Reset two-factor** next to the user on the **Users** page. If no administrator can sign in, run `reset-2fa` as shown above. The user enrols again at the next sign-in. |
| A local user forgot the password | Run `reset-password`. If the user also lost the phone, run `reset-2fa` as well. |
| The only administrator lost both password and phone | Run `reset-password root` and `reset-2fa local:root` (use the real user name). Sign in with the temporary password, enrol again, and set a new password. |
| A GitLab user cannot sign in | Check the [troubleshooting table](integrations/gitlab-sso.md#troubleshooting). Local sign-in still works for local accounts while GitLab is down. |
| A user sees "Your account is disabled" | An administrator disabled the account. Select **Enable** next to the user on the **Users** page, or run `enable-user` as shown in [disable or delete a user](#disable-or-delete-a-user). |
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

## Choose the dashboard branch of a service

Scanopsis keeps the scans of every branch (ref) that uploads, but a dashboard
shows one branch. An administrator chooses which one for each service. Without
this choice, a dashboard shows the branch with the most recent scan, so one
developer's feature branch can replace what everyone sees.

1. Sign in as an administrator and open **Services** in the navigation bar.
2. Find the service. The column **Dashboard branch** shows the chosen branch,
   or "Not chosen - showing the latest scanned branch (name)".
3. Select **Choose** (or **Change**). Type a branch name, or pick one of the
   branches that have been scanned from the suggestions. Select **Save**.
4. To go back to the latest scanned branch, select **Change**, then
   **Clear**.

Expected result: the service dashboard, the findings explorer and the
portfolio card show the branch you chose. The service dashboard says "dashboard
branch" next to the branch name, and the card names the branch.

The rule for the branch a dashboard shows is:

1. A branch you select on the service page, or pass as `ref` to the API, wins.
2. Otherwise the dashboard branch of the service, even if it has no scans yet.
   The dashboards then say "No scans of that branch yet". They never show
   another branch instead.
3. Otherwise the branch with the most recent scan.

A branch name can be at most 255 characters, without control characters.
Spaces at the start and end are removed. Every change is recorded in the
audit log as `service.updated`, with the previous and the new branch. The
cross-service CVE search lists the findings of every branch and is not
affected.

The dashboard branch is also the branch whose latest scans [data
retention](#data-retention) never deletes. The latest scan of any other
unprotected branch expires with the retention period.

## Data retention

Scanopsis deletes old data once a day so that the database does not grow
without limit.

### What is kept

- Scan history for the number of days in the `retention.days` setting. The
  default is 90.
- The **current scans**, however old. A scan is current when it is the latest
  scan of its service, ref, artifact and kind, and one of these holds:
  - its ref is the **dashboard branch** an administrator chose for the service
    (see [choose the dashboard branch](#choose-the-dashboard-branch-of-a-service));
  - its ref was **protected** when the scan ran (the scan's `ref_protected`
    flag);
  - the service has no chosen dashboard branch, and its ref is the one the
    dashboards then show: the ref of the service's most recent scan (when two
    refs share that time, the one that sorts first).

  So what a dashboard shows is never deleted, and the last scan of every
  protected branch stays. A branch that nobody has scanned for two years still
  shows its last result if it is the dashboard branch or protected.
- Every other scan expires after the period, **including the latest scan of an
  unprotected branch that is not the dashboard branch**. This keeps the
  database from growing with short-lived feature branches. If you want the
  last result of a branch to stay, choose it as the dashboard branch, or
  protect it in GitLab.
- The latest SonarQube analysis of every service, however old.
- Audit events for 365 days. This period is fixed and has no setting. If you
  set `retention.days` above 365, audit events are kept for that many days
  instead, so they are never kept for a shorter time than scan history.
- The original Trivy report of a scan for 30 days, unless the scan is
  current. See [original reports](#original-reports).

The set of current scans is read at the start of each batch, so a dashboard
branch you choose while a run is in progress protects its scans from then on.
If you change the dashboard branch of a service, the scans of the old branch
expire at the next run once they are older than the period.

### What is deleted

Each run does these steps, in this order:

1. Scans that are older than the period and are not current. Their findings are
   deleted with them.
2. The stored original report of scans that are older than 30 days and not
   current (it is emptied; the scan and its findings stay). The 30 days are
   fixed and have no setting.
3. SonarQube analyses that are older than the period and are not the latest
   of their service.
4. SonarQube webhook deliveries that are older than the period. This includes
   deliveries that never finished.
5. Audit events that are older than 365 days, or older than the period if
   that is longer.

A scan is as old as its `scanned_at` time, which is when Trivy ran, not when
the report arrived. A scan that is exactly as old as the period is kept.

### Original reports

Scanopsis stores the compressed report as uploaded with each scan. It is not
shown anywhere; it is there so you can check a number against what Trivy wrote.
It is also most of the database and of every backup, so it is kept for 30 days
for scans that are not current, and for as long as the scan is current.

To get the original report of a scan, use the scan ID (the number in the scan
URL or in the API, for example `42`):

```sh
docker compose exec scanopsis /scanopsis admin export-report 42 > report.json
```

A successful export is recorded in the audit log as **Scan report exported**
(`report.exported`, actor `cli`) with the scan ID only, never the content.

The command writes the decompressed JSON to standard output, so you can
compare it with `trivy convert` (see [verification](verification.md)). For a
scan whose report was cleared, it prints `the original report of scan 42 was
cleared by data retention` to standard error and exits with status 1.

Emptying a report frees its space for new data in the same database; PostgreSQL
gives the space back to the operating system only after a `VACUUM FULL`, which
locks the table while it runs. Autovacuum, which runs by default, makes the
space reusable without any action.

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
{"time":"2026-11-19T04:01:12.318Z","level":"INFO","msg":"data retention run finished","retention_days":90,"scans_deleted":41200,"raw_reports_cleared":9800,"sonar_analyses_deleted":3100,"sonar_deliveries_deleted":3100,"audit_events_deleted":12,"failed_steps":0,"duration":"48.2s"}
```

`failed_steps` is the number of the steps above that stopped with an
error. `raw_reports_cleared` is the number of original reports emptied. For each one, a separate `WARN` line `data retention step failed`
names the step and the error. The rows deleted before the error stay deleted,
and the next run continues from there. When another server holds the lock, the
run is skipped and logs only at the `debug` level.

A run that deleted anything also writes an audit event, **Old data deleted**
(`retention.deleted`, actor `retention`), with the period, the two cutoffs
and the number of rows deleted of each kind. Open **Audit log** and filter by
this action to see the history of deletions, including how many audit events
were removed.

## Backup and restore

Take a backup before every upgrade, see
[upgrade to a newer version](versions.md#upgrade-to-a-newer-version).

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

### Encrypt backups at rest

A backup holds password hashes, encrypted secrets and your scan data. Encrypt
it before it leaves the host, and keep the key that decrypts it **apart from
`SCANOPSIS_SECRET_KEY`**: whoever has both the backup and the secret key can
read every secret in it, and a backup and its key stored together protect
nothing. Use a separate key for each purpose, and store the backup key in your
password manager or vault.

With [age](https://github.com/FiloSottile/age) (the commands were run with
age 1.3.2):

```sh
age-keygen -o backup-key.txt
```

```text
Public key: age14c5m20x85hv834we8nszyg4ahmerl4jyex0rz7qvjrpmfsvs2g8qparejp
```

Move `backup-key.txt` off the host. The backup job needs only the public key:

```sh
PUBLIC_KEY=age14c5m20x85hv834we8nszyg4ahmerl4jyex0rz7qvjrpmfsvs2g8qparejp
(umask 077; docker compose exec -T db pg_dump -U scanopsis -d scanopsis \
  --format=custom | age -r "$PUBLIC_KEY" > "scanopsis-$(date +%F).dump.age")
ls -l scanopsis-*.dump.age
```

```text
-rw-------  1 siyam  wheel  34450 Nov 22 11:44 scanopsis-2026-11-22.dump.age
```

To restore, decrypt on a machine that has the key and pipe the result into
`pg_restore`, as in the restore steps below:

```sh
age -d -i backup-key.txt scanopsis-2026-11-22.dump.age | docker compose exec -T db \
  pg_restore -U scanopsis -d scanopsis --clean --if-exists --no-owner --exit-on-error
```

The restore printed nothing and exited with status 0.

With GnuPG, use `gpg --encrypt --recipient backup@example.com` in place of
`age -r`, and `gpg --decrypt` in place of `age -d`. The GnuPG commands were not
run for this page.

### Watch the disk of the database

The database lives in the Docker volume `db-data`, on the disk that holds
Docker's data (usually `/var/lib/docker`). Alert on the use of that disk at
**70%** (warning) and **85%** (critical). Check the use of the file system
that holds the volume with:

```sh
docker compose exec -T db df -h /var/lib/postgresql
```

```text
Filesystem                Size      Used Available Use% Mounted on
/dev/vda1               126.3G     26.4G     93.4G  22% /etc/hostname
```

(The mount name differs on your host. Read the `Use%` column.) Check the size
of the volume itself with `docker system df -v`.

The two levels have reasons:

- A full disk stops PostgreSQL from writing. Scanopsis then answers uploads and
  sign-ins with errors, and a database that ran out of space needs free space
  to recover. Fix this before it happens, not after.
- PostgreSQL, vacuum and the write-ahead log need free space to work, and the
  cleanup of old data does not return space to the disk at once. A backup and a
  restore need space as well. At 70% you still have time to add disk or to
  shorten the [retention](operations.md#data-retention) period. At 85% you
  have little room left: act at once.

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

3. Delete all sessions, as described in
   [after a restore](#after-a-restore) below.

   ```sh
   docker compose exec -T db psql -U scanopsis -d scanopsis -c "DELETE FROM sessions;"
   ```

   Expected result: `DELETE` and the number of sessions that the backup
   held, for example `DELETE 1`.

4. Start Scanopsis again and check it:

   ```sh
   docker compose start scanopsis
   docker compose ps
   ```

   Expected result: both containers are `healthy` after about 30 seconds.

5. Sign in. Every change after the backup is gone. For example, a password
   that was reset after the backup is back to the old one. Then check the
   users and the audit log, as described in [after a restore](#after-a-restore).

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

4. Delete all sessions, as described in
   [after a restore](#after-a-restore):

   ```sh
   docker compose exec -T db psql -U scanopsis -d scanopsis -c "DELETE FROM sessions;"
   ```

5. Start Scanopsis:

   ```sh
   docker compose up -d
   ```

6. Check that it works: `curl -s http://127.0.0.1:8080/readyz` answers
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

### After a restore

A restore puts the database back to the state of the backup. Do these checks
every time, also after a restore on a new host.

1. **Delete all sessions.** The backup holds the sessions that were valid when
   it was made. After the restore, they are valid again, including sessions
   that you ended or that belonged to users whom you removed after the backup.
   Delete all of them, with Scanopsis stopped, as in the restore steps above.
   Everyone then signs in again, with the password and a two-factor code:

   ```sh
   docker compose exec -T db psql -U scanopsis -d scanopsis -c "DELETE FROM sessions;"
   ```

   ```text
   DELETE 1
   ```

2. **Check the users.** Users, roles and two-factor settings are as they were
   at the backup. Remove anyone who left since then, and set roles right:

   ```sh
   docker compose exec -T db psql -U scanopsis -d scanopsis -c \
     "SELECT id, source, username, role, totp_enabled_at IS NOT NULL AS mfa, last_login_at FROM users ORDER BY id"
   ```

   ```text
    id | source | username | role  | mfa | last_login_at
   ----+--------+----------+-------+-----+---------------
     1 | local  | root     | admin | f   |
   (1 row)
   ```

   A user who set a new password, or reset two-factor authentication, after
   the backup has the old state again. Tell them.

3. **Read the end of the audit log.** The newest entries show the moment that
   the backup was made. Everything after it, including failed sign-ins and
   role changes, is gone:

   ```sh
   docker compose exec -T db psql -U scanopsis -d scanopsis -c \
     "SELECT at, actor, action FROM audit_log ORDER BY id DESC LIMIT 5"
   ```

   ```text
                 at               |     actor     |     action
   -------------------------------+---------------+-----------------
    2026-11-22 05:38:24.957072+00 | local:root    | setup.completed
    2026-11-22 05:35:41.998319+00 | local:unknown | login.failed
   ...
   ```

   If you restored because of a security incident, note the time of the last
   entry: you have no record of what happened after it.
