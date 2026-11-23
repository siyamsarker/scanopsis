# Run Scanopsis day to day

This guide covers the everyday jobs of looking after a running Scanopsis:
health checks, logs, user rescue, data retention, backups and restores.

**Audience:** Operators · **Back to:** [README](../README.md) · [All guides](../README.md#documentation)

---

Run every `docker compose` command in `deploy/compose`. The outputs were
captured on a test installation, so names, times and addresses in yours will
differ.

## Check that Scanopsis is healthy

Scanopsis has two endpoints for monitoring. Neither needs a sign-in.

| Endpoint | What it tells you | Healthy | Unhealthy |
|---|---|---|---|
| `/healthz` | The process runs. It does not touch the database. Use it to decide whether to restart. | `200` `{"status":"ok"}` | No answer |
| `/readyz` | The database answers. Use it to decide whether to send traffic. | `200` `{"status":"ready"}` | `503`, naming the failing part (never a secret) |

```sh
curl -s http://127.0.0.1:8080/healthz
curl -s http://127.0.0.1:8080/readyz
```

```text
{"status":"ok"}
{"status":"ready"}
```

`/readyz` checks the database over a connection of its own, at most once a
second. So it stays `200` while a busy server answers some requests with
`503 busy`, and fails only when the database is really gone. That connection
counts against the database's `max_connections`, on top of the pool.

### Read the container health

The image has no shell and no `curl`, so it carries its own probe:
`/scanopsis healthcheck` asks `/healthz` inside the container and exits 0 on
`200`, 1 otherwise. Docker runs it every 30 seconds (5-second timeout, 20
seconds of grace at start) and marks the container `unhealthy` after 3
failures in a row.

```sh
docker inspect "$(docker compose ps -q scanopsis)" --format '{{.State.Health.Status}}'
```

```text
healthy
```

The value is `starting` during the first 20 seconds. To run the probe by hand:

```sh
docker compose exec scanopsis /scanopsis healthcheck; echo "exit=$?"
```

```text
exit=0
```

A failing probe prints one line, for example
`healthcheck: Get "http://127.0.0.1:9999/healthz": dial tcp 127.0.0.1:9999: connect: connection refused`.

Docker does not restart a container just because it is `unhealthy`;
`restart: unless-stopped` only restarts one that exited. If Scanopsis is
unhealthy, read the log, and check the database with `docker compose ps db`
first: a restart does not fix a database that is down.

## Read the logs

Scanopsis writes one JSON object per line. Docker collects them:

```sh
docker compose logs scanopsis
docker compose logs --follow --since 10m scanopsis
```

Every line has `time`, `level` and `msg`:

```text
{"time":"2026-11-22T11:13:35.975316503Z","level":"INFO","msg":"request","method":"GET","path":"/healthz","status":200,"duration_ms":0,"client_ip":"172.30.0.10"}
```

What you find there:

- At start: `scanopsis starting` (version and a summary of the
  configuration), one `applying migration` and `applied migration` pair for
  each schema change, then `scanopsis listening`.
- One `request` line per request, with method, path, status, duration and
  client address.
- Warnings and errors, such as `GitLab sign-in failed`,
  `GitLab discovery failed` or `upload rejected`.
- While no administrator exists: one `WARN` line with the setup URL and the
  one-time setup token. This is the only secret ever logged.

The log never contains passwords, two-factor codes, recovery codes, cookies,
request headers, query strings, the database URL, the secret key or secret
settings. `compose.yaml` rotates the log (5 files of 10 MB), so it cannot fill
the disk.

### Change the log level

Set `SCANOPSIS_LOG_LEVEL` in `.env` to `debug`, `info` (the default), `warn`
or `error`, then run `docker compose up -d`. At `warn`, the request lines
disappear. At `debug`, you also see, for example, how many expired sessions
were removed each hour. Keep `info` unless you are chasing a problem.

### Read the audit log

Security events go to the `audit_log` table, not to the log: sign-ins and
failed sign-ins, role changes, setting changes, rejected uploads and every
administration command. Administrators read them on the **Audit log** page,
newest first, filtered by action or actor. When the web interface is not
available, use SQL:

```sh
docker compose exec db psql -U scanopsis -d scanopsis \
  -c "SELECT at, actor, action FROM audit_log ORDER BY id DESC LIMIT 5"
```

```text
              at               |   actor    |       action
-------------------------------+------------+---------------------
 2026-11-22 11:12:43.499417+00 | cli        | mfa.reset
 2026-11-22 11:12:43.226043+00 | cli        | user.password_reset
 2026-11-22 11:12:43.050236+00 | local:root | setup.completed
```

Add the `detail` column to see what changed. It never holds a secret.

## Administration commands

Six commands run inside the container and work on the database directly. Use
them when the web interface cannot help, for example when the only
administrator lost a password or a phone.

| Command | What it does |
|---|---|
| `reset-password <username>` | Gives a local account a new temporary password. |
| `reset-2fa <account>` | Removes the two-factor setup of an account. |
| `disable-user <account>` | Refuses every sign-in of an account and ends its sessions. |
| `enable-user <account>` | Lets a disabled account sign in again. |
| `delete-user <account>` | Deletes an account. |
| `export-report <scan-id>` | Writes the original Trivy report of a scan as JSON. |

Run them like this:

```sh
docker compose exec scanopsis /scanopsis admin <command>
```

Write an `<account>` in one of these ways. The prefix is required.

| Form | Which account |
|---|---|
| `local:<username>` | The local account with that name. |
| `gitlab:<username>` | The GitLab account with that name. If users of two GitLab instances share the name, the command changes nothing and lists them, each as `gitlab-id:<n>`. |
| `gitlab-id:<n>` | The GitLab account with GitLab user ID `n` (shown on the user's GitLab profile). |
| `id:<n>` | The account with Scanopsis user ID `n`, as in `GET /api/v1/users`. |

The commands need only the database URL, which the container already has. The
server must have started once, so the tables exist. Exit status: 0 on
success, 1 on failure, 2 on a usage error. Every command is recorded in the
audit log with the actor `cli`. Built-in help:
`docker compose exec scanopsis /scanopsis admin -h`.

### Reset a password

Use this when a local user forgot the password. GitLab users change theirs in
GitLab.

```sh
docker compose exec scanopsis /scanopsis admin reset-password root
```

```text
The temporary password for the local account "root" is:

xHwV9hFN8bgCGWvpsmbe

It is shown only now and is not stored anywhere. The account was signed out everywhere, and the temporary password must be changed at the next sign-in.
```

Give the temporary password to the user over a safe channel. The user signs
in with it, enters the two-factor code, and must then choose a new password
(at least 12 characters, not the user name) before anything else. The reset
also lifts a two-factor lock after too many wrong codes, but it keeps the
two-factor setup. A user who also lost the phone needs the next command too.

### Reset two-factor authentication

Use this when a user lost the phone and has no recovery code left:

```sh
docker compose exec scanopsis /scanopsis admin reset-2fa local:root
```

```text
Two-factor authentication was reset for local:root. The account was signed out everywhere and enrols again at the next sign-in.
```

The two-factor secret and all recovery codes of the account are deleted, and
its sessions end. At the next sign-in, the user passes the first step
(password or GitLab, including the group check), sees **Set up two-factor
sign-in**, scans a new QR code and gets 10 new recovery codes.

An administrator who can still sign in can do the same with **Reset
two-factor** in the user's actions menu (**⋯**) on the **Users** page.

### Disable, enable or delete a user

Disable an account to take access away at once, for example when someone
leaves. Delete an account you no longer want on the **Users** page.

| | Disable | Delete |
|---|---|---|
| Sign-in | Refused, with password and with GitLab, until enabled again. | A local account is gone. A GitLab user who signs in again (and passes the group check) comes back as a new viewer. |
| Sessions | End at once. | End with the account. |
| Role and two-factor setup | Kept, and back in effect after enabling. | Removed. |
| Audit log | `user.disabled`, `user.enabled` | `user.deleted`. Older events keep the name. |

To keep a GitLab user out for good, disable the account, or remove the user
from the allowed GitLab groups.

On the **Users** page, open the user's actions menu (**⋯**), select **Disable
user** (or **Enable user**) or **Delete user**, and confirm. You cannot disable or delete yourself, and the last
enabled administrator can be neither disabled, deleted nor demoted. The same
from the command line:

```sh
docker compose exec scanopsis /scanopsis admin disable-user gitlab:alice
docker compose exec scanopsis /scanopsis admin enable-user gitlab:alice
docker compose exec scanopsis /scanopsis admin delete-user local:olduser
```

```text
The account gitlab:alice was disabled and signed out everywhere.
```

### When a command fails

The message starts with `scanopsis admin:`.

| Message | What to do |
|---|---|
| `there is no local account named "alice"` | Check the spelling. A user who signs in with GitLab has no password in Scanopsis. |
| `there is no gitlab account named "alice"` | A GitLab account exists only after its owner signed in once. Check the prefix. |
| `the account must be written local:<username>, gitlab:<username>, gitlab-id:<n> or id:<n>.` | You wrote only the name, such as `root`. Add the prefix. |
| `gitlab:alice matches 2 accounts; name one of them:` and a list | Users of two GitLab instances are called `alice`. Run the command again with the right `gitlab-id:<n>` from the list. |
| `local:root is the last enabled administrator; enable or promote another administrator first` | The change would leave nobody to run Scanopsis. Enable or promote another administrator first. |
| `the database has no schema yet: run the server once first so it can create it.` | Start the server once with `docker compose up -d`. |
| `SCANOPSIS_DATABASE_URL (or SCANOPSIS_DATABASE_URL_FILE) is not set` | You ran the command outside the container. Use `docker compose exec scanopsis`. |

## Recovering access when locked out

Find your situation:

| Situation | What to do |
|---|---|
| A user lost the phone but has a recovery code | On **Enter your code**, select **Use a recovery code** and enter one. Each code works once. Nothing needs resetting. |
| A user lost the phone and has no recovery code | An administrator selects **Reset two-factor** in the user's actions menu on the **Users** page. If no administrator can sign in, run [`reset-2fa`](#reset-two-factor-authentication). |
| A local user forgot the password | Run [`reset-password`](#reset-a-password). If the phone is gone too, also run `reset-2fa`. |
| The only administrator lost both password and phone | Run `reset-password root` and `reset-2fa local:root` (with the real name). Sign in with the temporary password, enrol again, set a new password. |
| A user sees "Your account is disabled" | Select **Enable user** in the user's actions menu on the **Users** page, or run `enable-user`. |
| A GitLab user cannot sign in | See [troubleshooting](troubleshooting.md#gitlab-sign-in). Local accounts can still sign in while GitLab is down. |
| Local sign-in is off and GitLab sign-in does not work | [Turn local sign-in on again](#turn-local-sign-in-on-again). |
| The server does not start and the log mentions `SCANOPSIS_SECRET_KEY` | See [the secret key](configuration.md#the-secret-key). |

### Turn local sign-in on again

Scanopsis refuses to start with local sign-in off unless GitLab sign-in is
fully set up. You can still get locked out when local sign-in is off and
GitLab sign-in then stops working, for example because GitLab is down or the
client secret is wrong.

1. If `.env` contains `SCANOPSIS_LOCAL_LOGIN_ENABLED=false`, delete that line.
   Otherwise it is saved again at the next start.
2. Delete the saved value, so the setting falls back to its default (on):

   ```sh
   docker compose exec db psql -U scanopsis -d scanopsis \
     -c "DELETE FROM settings WHERE key = 'auth.local_login_enabled'"
   ```

   You should see `DELETE 1`.

3. Restart Scanopsis, because it reads a setting changed outside the Settings
   page only at start:

   ```sh
   docker compose up -d --force-recreate scanopsis
   ```

4. Check the sign-in methods:

   ```sh
   curl -s http://127.0.0.1:8080/api/v1/auth/methods
   ```

   ```text
   {"gitlab":false,"local":true}
   ```

Sign in as a local administrator. If you lost the password or the phone,
use the commands above first.

## GitLab users and the GitLab URL

Scanopsis knows a GitLab user by the GitLab instance (the GitLab URL at
sign-in) together with the user ID there. The same ID on another instance is
another person. So after you change the GitLab URL setting, every GitLab user
starts over as a new viewer, and the old accounts stay on the **Users** page.
Delete them when you no longer need them.

If the same GitLab only moved to a new address, you can keep the accounts:
move them to the new URL in the database before anyone signs in through it.
Write both URLs exactly as the Settings page shows them (example):

```sh
docker compose exec db psql -U scanopsis -d scanopsis -c \
  "UPDATE users SET gitlab_issuer = 'https://gitlab.new.example.com' WHERE gitlab_issuer = 'https://gitlab.example.com';"
```

GitLab users created by version 0.1.0 were assigned the GitLab URL saved in
the settings during the upgrade. If none was saved, their next sign-in creates
a new account.

## Choose the dashboard branch of a service

Scanopsis keeps the scans of every branch that uploads, but a dashboard shows
one branch. Until an administrator chooses it, a dashboard shows the branch
with the most recent scan, so one developer's feature branch can replace what
everyone sees. Choose it for every service after its first upload.

1. Sign in as an administrator and open **Services**.
2. Find the service. Its **Dashboard branch** tag shows the chosen branch, or
   "Latest" and the latest scanned branch when none is chosen.
3. Select the tag (or **Choose dashboard branch** in the actions menu), type a
   branch name or pick one of the scanned branches, and select **Save branch**.
4. To go back to the latest scanned branch, open the same editor and select
   **Use latest scanned**.

The service dashboard, the findings explorer and the portfolio card now show
that branch. Which branch a dashboard shows, in order:

1. A branch you pick on the service page, or pass as `ref` to the API.
2. Else the dashboard branch, even before it has scans. The dashboards then
   say "No scans of main yet" (with your branch name) and never show another
   branch instead.
3. Else the branch with the most recent scan.

A branch name can have up to 255 characters, without control characters.
Every change is in the audit log as `service.updated`, with the old and the
new branch.

## Find a vulnerability across services

**Search** in the header finds where a vulnerability ID (a CVE or GHSA number)
appears in the current scans, meaning the latest scan of each artifact on a
branch. A switch picks which branches are searched:

- **Dashboard branches and protected refs** (default): the dashboard branch of
  every service and every protected branch or tag. This is what your services
  are judged by, and it stays fast with many short-lived branches.
- **All branches**: every branch of every service. Slower: 25 to 30 ms at 500
  services with 4 branches each, about 0.3 s with 80,000 (service, branch,
  artifact) groups.

The choice is part of the address (`/search?id=CVE-2024-3094&scope=all`), and
the API takes `scope=dashboard` or `scope=all`. Suppressed findings and
archived services are left out. At most 1,000 findings are listed, the most
severe first; the totals count every match.

## Data retention

Scanopsis deletes old data once a day, so the database does not grow without
end.

### What is kept

- **Scan history** for `retention.days` days (default 90).
- **Current scans, however old.** A scan is current when it is the latest of
  its service, branch, artifact and kind, and its branch is the dashboard
  branch, or was protected when the scan ran, or (with no dashboard branch
  chosen) is the branch the dashboards show. So what a dashboard shows is
  never deleted, and the last scan of every protected branch stays.
- **The latest SonarQube analysis** of every service, however old.
- **Audit events** for 365 days, or for `retention.days` if that is longer.
- **The original Trivy report** of a scan for 30 days, or as long as the scan
  is current.

Everything else expires after the period, **including the latest scan of an
unprotected branch that is not the dashboard branch**. That keeps short-lived
feature branches from piling up. To keep a branch's last result, choose it as
the dashboard branch or protect it in GitLab.

A scan's age is its `scanned_at` time, when Trivy ran, not when the report
arrived.

### What a cleanup run does

Each run, in this order:

1. Deletes scans older than the period that are not current, with their
   findings.
2. Empties the stored original report of scans older than 30 days that are
   not current. The scan and its findings stay.
3. Deletes SonarQube analyses older than the period, except the latest of each
   service.
4. Deletes SonarQube webhook deliveries older than the period.
5. Deletes audit events older than 365 days (or the period, if longer).

The first run starts one minute after the server starts, then every 24 hours.
The server stays available: a run deletes 1,000 rows at a time, each batch in
its own short transaction, on one database connection. A run that deleted or
emptied 10,000 rows or more ends with `ANALYZE`, so later queries plan with
fresh statistics.

Deleted rows free space for new data. PostgreSQL gives the space back to the
operating system only after `VACUUM FULL`, which locks the table. You do not
need it: autovacuum makes the space reusable on its own, and Scanopsis tunes
it to start early on the two large tables.

### Change the retention period

Open **Settings**, find **Retention**, enter a whole number of days from 7 to
3650, and select **Save changes**. You can also seed the value with
`SCANOPSIS_RETENTION_DAYS` before the first start; after that, the Settings
page wins. The new value applies from the next run. After a big cut, the first
run takes longer than usual.

Deleted data can only come back from a [backup](#backup-and-restore).

### Check the last cleanup

Each run logs one line when it finishes:

```sh
docker compose logs scanopsis | grep "data retention"
```

```text
{"time":"2026-11-22T11:16:56.344773721Z","level":"INFO","msg":"data retention run finished","retention_days":90,"scans_deleted":0,"raw_reports_cleared":0,"sonar_analyses_deleted":0,"sonar_deliveries_deleted":0,"audit_events_deleted":0,"analyzed":false,"failed_steps":0,"duration":"16ms"}
```

`raw_reports_cleared` counts emptied original reports. `analyzed` says whether
the run ended with `ANALYZE`. `failed_steps` counts steps that stopped with an
error; each has its own `WARN` line, `data retention step failed`, naming the
step and the error. Rows deleted before the error stay deleted, and the next
run carries on.

A run that deleted anything also writes the audit event **Old data deleted**
(`retention.deleted`, actor `retention`) with the counts. Filter the **Audit
log** page by it to see the history.

### Export the original report of a scan

Scanopsis keeps the compressed report as uploaded, so you can check a number
against what Trivy wrote. Use the scan ID (the number in the scan's address or
in the API):

```sh
docker compose exec scanopsis /scanopsis admin export-report 42 > report.json
```

The command writes the JSON to standard output, ready for `trivy convert` (see
the [accuracy check](verification.md)). Each export is in the audit log as
**Scan report exported** (`report.exported`), with the scan ID only. A report
that retention emptied gives
`the original report of scan 42 was cleared by data retention, 30 days after the scan; only the reports of scans that are current are kept`
and exit status 1. An unknown ID gives `there is no scan with ID 42`.

## Backup and restore

Scanopsis makes, keeps, downloads and restores backups itself. Open
**Backups** in the Admin menu (administrators only). Keep two things, and keep
them apart:

| What | Where it lives | Where to keep a copy |
|---|---|---|
| The backup files | The folder `backups` next to `compose.yaml` (`/var/lib/scanopsis/backups` in the container, set by `SCANOPSIS_BACKUP_DIR`) | Another host or your normal backup storage |
| `secrets/secret_key` | `deploy/compose/secrets/secret_key` | A password manager or vault, not next to the backup files |

A restore works only with the secret key the backup was made with, so a
backup without its key is of no use. Also keep copies of `.env`,
`compose.yaml` and the other two secret files, so you can rebuild the host.

A backup holds password hashes and encrypted secrets. Protect it like a
secret. Take one before every upgrade.

### What a backup contains

A backup is one file with everything Scanopsis stores:

- users, roles, two-factor secrets and recovery codes;
- settings, including the saved secrets in their encrypted form;
- services, scans and findings, with the original Trivy report of each scan
  that still has one;
- SonarQube analyses and deliveries;
- the audit log.

Sessions are not in it, because a restore signs everyone out anyway. The
file is signed with a key derived from `secrets/secret_key`, so Scanopsis
accepts only backups of its own installation.

The file is called, for example,
`scanopsis-backup-20261123T020000Z-scheduled-ab12cd.tar.gz`: the time in UTC,
the kind and six random characters (the date and characters here are an
example). The kinds are:

| Kind | How it was made |
|---|---|
| Manual | **Back up now** |
| Scheduled | The schedule |
| Uploaded | **Upload a backup**, or a restore on the setup screen |
| Before restore | Scanopsis saved the current data before a restore |

The folder has mode 700 and belongs to user 65532, the user that runs
Scanopsis; the files in it have mode 600. The Backups page checks every few
seconds that Scanopsis can still write to the folder. If it cannot, the page
says why and **Back up now** and **Upload a backup** are disabled until it is
fixed; no restart is needed.

### Back up from the Backups page

Select **Back up now**. The backup runs in the background, so you can leave
the page; the page shows a bar that moves with the data written so far, the
table in progress, and the new file at the top of the list once it is done.
The share is an estimate made from the size of each table, so the bar can
pause just short of the end of a table that holds more than expected. The server keeps working during a backup, and
the backup is a consistent snapshot of the database.

Before it starts, Scanopsis checks the free space of the backup folder: it
needs the estimated size of the backup plus 512 MiB. If there is less, the
backup does not start and the message gives both numbers. Free space, or
[watch the disk](#watch-the-database-disk) before it fills.

### Schedule backups

Scheduled backups are off by default. In the panel **Scheduled backups**:

1. Turn the switch on.
2. Add one or more times of day (24-hour `HH:MM`, at most 24 a day) and
   choose the time zone: type a city in the filter and press Enter to take
   the first match, or pick one from the list. The default is `02:00` in UTC.
3. Set how many days to keep scheduled backups, from 1 to 365 (default 7).
4. Select **Save schedule**. Changes you have not saved stay in the form when
   you open another page and come back in the same browser tab; the browser
   asks before a reload or a closed tab loses them.

After a scheduled backup succeeds, Scanopsis deletes the scheduled backups
older than the keep period. It never deletes the newest scheduled backup, and
it never deletes manual, uploaded or before-restore backups: those stay until
you delete them.

If a scheduled backup fails, administrators see a banner with the reason on
the Backups page and on the portfolio page, and the audit log has the event
**Backup failed**. The banner shows only while the schedule is on. **Dismiss**
hides it until the next scheduled run, which shows it again if that run fails
too; dismissing is audited as **Backup alert dismissed**
(`backup.schedule_alert_dismissed`). The most common reasons are in
[troubleshooting](troubleshooting.md#backups-and-restores).

A schedule change is saved as a setting, so it appears in the audit log as
**Settings changed** (`settings.changed`).

### Download, upload and delete a backup

Use the menu of a row to **Download** or **Delete** a backup. A download
streams the file and resumes if your browser supports it. Copy backups off the
host: a backup on the same disk does not help when the disk fails. A backup
file is signed, not encrypted: encrypt copies before they leave the host, with
a key kept apart from the secret key, as
[encrypt your backups](#encrypt-your-backups) shows for `pg_dump` files.

**Upload a backup** adds a file you downloaded earlier, for example from
another installation that uses the same secret key. The browser sends it in parts of 8 MiB, so a large file
passes a proxy and an interrupted upload resumes where it stopped. Scanopsis
then reads the whole file and checks the signature, in the background, one
file at a time; the dialog says **Checking the backup** until it is done.
While the file is being sent, the browser asks before you leave the page,
since leaving stops the upload. Once every byte was sent, **Close and keep
checking** closes the dialog; a message says when the backup is in the list.
An interrupted upload stays when you close the dialog: open it again and
select **Resume upload**. An empty file is refused before anything is sent. A
file from another secret key, from a newer version of Scanopsis or a damaged
file is refused with the reason. **Cancel upload** also stops a check. At most
two uploads can be open at once; an upload that receives nothing for 10
minutes makes room for a new one. Uploads that were started but not finished,
for example because the page was closed, are listed under **Unfinished
uploads** on the Backups page, where **Cancel upload** frees them.

Backups that Scanopsis cannot read are listed as **Cannot be read**; you can
download and delete them, not restore them. A file that appears in the folder
without being made or uploaded through Scanopsis, for example one copied in by
hand, is read in full in the background, one file at a time and after the
other checks. If it is damaged, for example because the copy stopped early, it
is listed as **Cannot be read** with the reason, such as `The file is damaged:
the file ends too early.` Scanopsis remembers the outcome in the hidden file
`.checked.json` in the backup folder, so a file is read again only when it
changes. A backup from a newer version of Scanopsis is
listed as **Made by a newer version of Scanopsis**: install that version or a
later one to restore it. A backup made with another secret key shows
**Made with another secret key** and cannot be restored here. To use it, set
up a new installation with that secret key and restore it on the setup
screen, as in [restore on a new host](#restore-on-a-new-host).

### Restore a backup

A restore replaces everything in Scanopsis with the backup. Do it in a quiet
moment. You need an administrator account with two-factor authentication.

1. In the menu of the backup, choose **Restore**. Scanopsis reads the whole
   file first, in the background; the dialog says **Checking the backup…**
   until it is done, which takes several minutes for a large backup. The
   dialog then shows the version of Scanopsis that made the backup and who
   made it, and says that:
   - everything is replaced by the backup, and everything that changed after
     its date is lost;
   - everyone is signed out, including you; for two hours afterwards the
     sign-in page says that a restore signed everyone out;
   - pipeline uploads are turned away while the restore runs, and scans
     uploaded in that time are not kept.
2. If the backup holds scans older than the retention period, the dialog also
   warns that the next daily clean-up removes up to that many scans. To keep
   them, raise the retention period on the **Settings** page right after the
   restore (see [data retention](#data-retention)).
3. Type `RESTORE` (exactly, in capitals) and a current two-factor code, then
   select **Restore backup**.

Scanopsis first checks the whole file again. Then it saves the current data as
a before-restore backup, so you can go back, and replaces the data right
after it. If the disk has no room for the before-restore backup, tick
**Skip the backup of the current state** in the dialog. The restore then cannot be undone.

A restore that cannot start, because another job runs or the backup cannot be
restored here, is refused before your two-factor code is checked, so the code
stays usable for the next try.

While the restore runs, a full-screen page shows the progress. The API
answers `503` with the code `restoring` and `Retry-After: 30`, except for
`/api/v1/version` and `/api/v1/restore/status`; `/healthz`, `/readyz` and the
web interface keep answering. Pipeline uploads are therefore turned away and
not kept; the upload script from the
[GitLab CI guide](integrations/gitlab-ci.md) treats `503` as a reason to
retry for several minutes and then lets the job pass.

The restore is all or nothing: it runs in one database transaction, so a
failure leaves the data as it was and the page says so. The API turns
requests away only from the moment the data is replaced, after the file was
checked again in full and the before-restore backup was taken. When it ends,
sign in again with an account from the backup. The backup schedule keeps the
values this server had before the restore.

A restore can commit and still not finish, for example when a step after the
commit fails. The page then says that the backup was restored but Scanopsis
must restart. When the commit itself fails, Scanopsis cannot tell whether
PostgreSQL saved the restore, and handles it the same way, but the page says
that the restore may have finished. In both cases the server stays in restore
mode until it restarts:

```sh
docker compose restart scanopsis
```

Then do the [checks after a restore](#check-everything-after-a-restore).

Restore needs disk space in PostgreSQL for the old and the new data until it
commits.

### Restore on a new host

Use this after a disaster, or to move Scanopsis to another machine. You need
a backup file and the **same** `secrets/secret_key` it was made with.

1. Prepare the host as in the [getting started guide](getting-started.md):
   build the image, create the secrets (with the old `secrets/secret_key`),
   `.env` and the data folders. The other two secret files can be new, but
   `secrets/database_url` must match `secrets/postgres_password`.
2. Start Scanopsis and read the setup token from the log:

   ```sh
   docker compose up -d
   docker compose logs scanopsis | grep setup_token
   ```

3. Open the address of Scanopsis. On the first screen choose **Restore from a
   backup**, enter the setup token and choose the backup file. Scanopsis
   uploads it in parts, checks it in the background (the screen says
   **Checking the backup** meanwhile) and shows its date, version and whether
   the key matches. If the page is reloaded during the check, enter the setup
   token again in the same browser tab: the screen follows the same check and
   does not ask for the file again.
4. Type `RESTORE` and confirm. There is no two-factor code to enter, because
   no account exists yet; the setup token proves your right instead. Wrong
   tokens are limited per client address.
5. When the screen says the restore finished, sign in with an account from the
   backup. The backup schedule comes from the backup too.

If the backup was made with another secret key, or by a newer version of
Scanopsis, the screen says so and stops. Fix the cause on this new installation, which has no data yet: use the
`secrets/secret_key` the backup was made with, or install the newer version,
and restart Scanopsis. A restart prints a new setup token. Never change the
secret key of an installation that is in use.

The restored file stays in the `backups` folder as an uploaded backup.

### Watch the database disk

The database lives in the folder `data/postgres` next to `compose.yaml`, and
the backups in `backups` next to it. Watch the disk that holds them; with the
default layout, both are on the same disk. Alert at **70%** used (warning) and
**85%** (critical):

```sh
docker compose exec -T db df -h /var/lib/postgresql
```

```text
Filesystem                Size      Used Available Use% Mounted on
/dev/vda1               126.3G     45.7G     74.2G  38% /etc/hostname
```

Read the `Use%` column; the mount name differs on your host.
`du -sh data/postgres backups` in `deploy/compose` shows the size of the two
folders (use `sudo`, because the database files belong to another user).

Why these levels: a full disk stops PostgreSQL from writing, so uploads and
sign-ins fail, and a full database needs free space to recover. Vacuum, the
write-ahead log, backups and restores need room too, and retention does not
hand space back at once. At 70% you still have time to add disk or shorten the
[retention period](#change-the-retention-period). At 85%, act now.

### Back up with pg_dump instead

The Backups page is the way to back up Scanopsis. You can still back up the
database with PostgreSQL's own tools, for example to fit an existing backup
routine. A `pg_dump` file includes the sessions table, and it is not signed;
restore it with `pg_restore`, as below, not on the Backups page.

#### Back up the database

```sh
(umask 077; docker compose exec -T db pg_dump -U scanopsis -d scanopsis \
  --format=custom > "scanopsis-$(date +%F).dump")
```

`umask 077` makes the file readable only by you. `-T` stops Docker from adding
terminal characters to the output. The server keeps running.

Check that the file is a valid archive:

```sh
docker compose exec -T db pg_restore --list < scanopsis-2026-11-22.dump | head -8
```

```text
;
; Archive created at 2026-11-22 11:14:56 UTC
;     dbname: scanopsis
;     TOC Entries: 91
;     Compression: gzip
;     Dump Version: 1.16-0
;     Format: CUSTOM
;     Integer: 4 bytes
```

Problems:

- `no configuration file provided: not found`: you are not in `deploy/compose`.
- `service "db" is not running`: start it with `docker compose up -d db`.
- An empty backup file: read the error `pg_dump` printed.

Run the backup on a schedule, copy the files to another host, and test a
restore now and then.

#### Encrypt your backups

Encrypt a backup before it leaves the host. Keep the decryption key **apart
from `SCANOPSIS_SECRET_KEY`**: whoever holds both the backup and the secret key
can read every secret in it. With [age](https://github.com/FiloSottile/age)
(version 1.3.2 was used):

```sh
age-keygen -o backup-key.txt
PUBLIC_KEY=$(age-keygen -y backup-key.txt)
(umask 077; docker compose exec -T db pg_dump -U scanopsis -d scanopsis \
  --format=custom | age -r "$PUBLIC_KEY" > "scanopsis-$(date +%F).dump.age")
```

Then move `backup-key.txt` off the host, into your password manager or vault.
The backup job needs only the public key, which `age-keygen` also prints.

To restore, decrypt on a machine that has the key and pipe the result into
`pg_restore`, as in the restore steps below:

```sh
age -d -i backup-key.txt scanopsis-2026-11-22.dump.age | docker compose exec -T db \
  pg_restore -U scanopsis -d scanopsis --clean --if-exists --no-owner --exit-on-error
```

It prints nothing and exits with 0. With GnuPG, use `gpg --encrypt --recipient`
and `gpg --decrypt` instead (not tested).

#### Restore into the running installation

Use this to go back to an earlier state.

1. Stop Scanopsis, so nothing writes during the restore. The database keeps
   running:

   ```sh
   docker compose stop scanopsis
   ```

2. Restore the backup over the existing data:

   ```sh
   docker compose exec -T db pg_restore -U scanopsis -d scanopsis \
     --clean --if-exists --no-owner --exit-on-error < scanopsis-2026-11-22.dump
   echo "exit=$?"
   ```

   You should see only `exit=0`. `--clean` drops the current tables first, so
   everything that changed after the backup is lost.

3. Delete all sessions (see [why](#check-everything-after-a-restore)):

   ```sh
   docker compose exec -T db psql -U scanopsis -d scanopsis -c "DELETE FROM sessions;"
   ```

4. Start Scanopsis and wait until it is `healthy` (about 30 seconds):

   ```sh
   docker compose start scanopsis
   docker compose ps
   ```

5. Sign in and do the [checks after a restore](#check-everything-after-a-restore).

#### Restore a pg_dump backup on a new host

Use this after a disaster, or to move Scanopsis to another machine.

1. Prepare the host as in the [getting started guide](getting-started.md):
   build the image, create the secrets and `.env`. Use the **same**
   `secrets/secret_key` the backup belongs to. The other two secret files can
   be new, but `secrets/database_url` must match `secrets/postgres_password`.
2. Start only the database and wait until it is `healthy`:

   ```sh
   docker compose up -d db
   docker compose ps db
   ```

3. Restore the backup and delete the sessions:

   ```sh
   docker compose exec -T db pg_restore -U scanopsis -d scanopsis \
     --clean --if-exists --no-owner --exit-on-error < scanopsis-2026-11-22.dump
   docker compose exec -T db psql -U scanopsis -d scanopsis -c "DELETE FROM sessions;"
   ```

4. Start Scanopsis with `docker compose up -d`.
5. Check: `curl -s http://127.0.0.1:8080/readyz` answers `{"status":"ready"}`,
   you can sign in with an existing account and a two-factor code, and the log
   has no `setup_token` line, because the restored database already has an
   administrator.

If it goes wrong:

| Symptom | Cause and fix |
|---|---|
| `pg_restore` complains about roles or owners | You left out `--no-owner`. Add it. |
| `pg_restore` or Scanopsis cannot connect | The database name, user or password differs. Compare `secrets/database_url` with `secrets/postgres_password`. |
| Scanopsis restarts in a loop with `cannot be decrypted: SCANOPSIS_SECRET_KEY changed or is wrong` | Wrong secret key for this backup. Put the original key back. |
| Sign-in works but two-factor codes give a server error | The same cause: the wrong key. Put the original key back. |

### Check everything after a restore

A restore puts the database back to the moment of the backup. Do these checks
every time.

1. **Delete all sessions (only after a `pg_restore`).** A restore on the
   Backups page or the setup screen already signs everyone out, because backups
   hold no sessions. A `pg_dump` file holds the sessions that were valid when
   it was made, including ones you ended or that belonged to people you
   removed since. With Scanopsis stopped:

   ```sh
   docker compose exec -T db psql -U scanopsis -d scanopsis -c "DELETE FROM sessions;"
   ```

   Everyone then signs in again, with password and two-factor code.

2. **Check the users.** Users, roles and two-factor setups are as they were.
   Remove anyone who left since, and fix roles:

   ```sh
   docker compose exec -T db psql -U scanopsis -d scanopsis -c \
     "SELECT id, source, username, role, disabled_at IS NOT NULL AS disabled, totp_enabled_at IS NOT NULL AS mfa, last_login_at FROM users ORDER BY id"
   ```

   ```text
    id | source | username | role  | disabled | mfa | last_login_at
   ----+--------+----------+-------+----------+-----+---------------
     1 | local  | root     | admin | f        | f   |
   ```

   A user who changed the password or reset two-factor authentication after
   the backup has the old state again. Tell them.

3. **Read the end of the audit log** (see
   [read the audit log](#read-the-audit-log)). The newest entry marks the
   moment of the backup. If you restored because of a security incident, note
   that time: you have no record of what happened after it. A restore from the
   Backups page or the setup screen adds the event **Backup restored**
   (`backup.restored`) after the restored entries.

---

[Back to the README](../README.md) · [All guides](../README.md#documentation) · Found a mistake? [Open an issue](https://github.com/siyamsarker/scanopsis/issues)
