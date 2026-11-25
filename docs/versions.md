# Versions and upgrades

Which version to install, and how to upgrade or roll back.

**Audience:** Everyone · **Back to:** [README](../README.md) · [All guides](../README.md#documentation)

---

## The short version

Install **0.4.3**, the newest version. It is on the branch `release/0.4.3`
with the tag `v0.4.3`. Scanopsis publishes no container image, so you build
it yourself from that branch, as [install the newest version](#install-the-newest-version)
shows.

| Do | Do not |
| --- | --- |
| Deploy the newest version branch, `release/0.4.3`. | Deploy from `main`. It is development code and can be broken at any time. |
| Upgrade as soon as a newer version is published. Only the newest version receives fixes. | Install an older version, or stay on one. |
| Back up the database and the secret key before every upgrade. | Upgrade without a backup. Some versions change the database, and that cannot be undone. |
| Tag your image with the version: `scanopsis:v0.4.3`. | Reuse one image name for different versions; you lose the way back. |

A version branch never changes once it is published. Fixes go into the next
version.

> **Older versions:** the branches `release/0.1.0`, `release/0.2.0`,
> `release/0.3.0`, `release/0.4.0`, `release/0.4.1` and `release/0.4.2` stay
> on GitHub only as a record. They receive no fixes, including security
> fixes, and are not meant for new installations. If you run one,
> [upgrade to 0.4.3](#upgrade-to-the-newest-version); you can go straight
> there from any older version.

Versions before 1.0.0 are early versions: compare the numbers with your own
Trivy and SonarQube results before you rely on them. The
[accuracy check](verification.md) shows how.

## Find the newest version

These guides show the newest version at the time they were written. If you
read them on a version branch, a newer version may exist since. Open the
[Branches page](https://github.com/siyamsarker/scanopsis/branches) on GitHub
and look for the highest `release/` number, or run:

```sh
git ls-remote --heads https://github.com/siyamsarker/scanopsis.git 'release/*'
```

Compare the numbers one part at a time: 1.10.0 is newer than 1.9.0.

## Install the newest version

Do this for a new installation.

1. Get the version and check it:

   ```sh
   git clone --branch release/0.4.3 --depth 1 https://github.com/siyamsarker/scanopsis.git
   cd scanopsis
   git describe --tags --always --dirty
   ```

   The last command prints `v0.4.3`. If it prints a commit hash, run
   `git fetch --depth 1 origin tag v0.4.3` and try again.

2. Build the image:

   ```sh
   make image IMAGE=scanopsis:v0.4.3
   ```

3. Continue with [step 2 of the getting started guide](getting-started.md#step-2-create-the-secrets).
   `deploy/compose/.env.example` already names the image `scanopsis:v0.4.3`.

## Check the running version

```sh
curl -s http://127.0.0.1:8080/api/v1/version
```

It prints the version the image was built from. The line looks like this
(an example: the commit and the Go version on your build differ):

```text
{"version":"v0.4.3","commit":"<commit>","go":"<go version>"}
```

`version` must be `v0.4.3`.

If `version` is not the one you expect, the container runs another image.
Build the image again from the version branch.

## Upgrade to the newest version

**When:** a newer version is published. **Takes:** about 10 minutes, plus
the backup.

You can upgrade from any older version straight to 0.4.3; you do not need to
install the versions in between. The newest version applies every database
change it finds missing.

| You run | Database change | Read first |
| --- | --- | --- |
| 0.4.2 | No | [From 0.4.2](#from-042) |
| 0.4.1 | Yes: the two small migrations of 0.4.2, applied when 0.4.3 starts | [From 0.4.1](#from-041) |
| 0.4.0 | Yes: four migrations, the two of 0.4.1 and the two of 0.4.2 | [From 0.4.0](#from-040) |
| 0.3.0 | Yes: the same four | [From 0.3.0](#from-030) |
| 0.2.0 | Yes: the same four | [From 0.2.0](#from-020) |
| 0.1.0 | Yes: all the changes since 0.1.0, applied when 0.4.3 starts | [From 0.1.0](#from-010) |

1. Read [before you upgrade](#before-you-upgrade) for the version you run.

2. Back up the database and the secret key, as the
   [operations guide](operations.md) describes under "Back up with pg_dump
   instead" (the Backups page exists only from 0.4.0). If you run 0.4.0 or
   later, use **Back up now** on the Backups page; it backs up the database,
   so also copy `deploy/compose/secrets/secret_key` to a password manager or
   vault, not next to the backups. An upgrade from 0.4.1 or older changes the
   database, so do not skip this.

3. Get 0.4.3 in your existing clone, and compare its Compose files with the
   ones you run before you switch, because the switch replaces them:

   ```sh
   git fetch --depth 1 origin release/0.4.3:release/0.4.3 tag v0.4.3
   git diff HEAD v0.4.3 -- deploy/compose
   git status --short deploy/compose
   ```

   The diff shows what changes in `compose.yaml` and `.env.example`: lines
   with `+` are new. Note each new setting for your `.env`. Your `.env`,
   secrets and data are not tracked by git, so the switch leaves them alone.
   `git status` lists the tracked files you changed yourself, marked `M`, and
   files git does not track, marked `??` (such as `compose.override.yaml`, a
   `Caddyfile` or a dump); the switch leaves the `??` files alone. If it lists
   `compose.yaml` or `compose.external-db.yaml` with `M`, keep your copy and
   undo the change, or the switch refuses with
   `Your local changes ... would be overwritten`:

   ```sh
   cp deploy/compose/compose.yaml ../compose.yaml.mine
   git restore deploy/compose/compose.yaml
   ```

   After the switch, carry your changes over into the new file, or better
   into `compose.override.yaml`, which no version replaces.

4. Switch and build:

   ```sh
   git switch release/0.4.3
   make image IMAGE=scanopsis:v0.4.3
   ```

   A fresh clone, as in [install the newest version](#install-the-newest-version),
   works too; compare its `deploy/compose` folder with yours.

   The new `compose.yaml` is now in place: it keeps the database and the
   backups in folders. Add the new settings you noted to `.env`. Leave
   `SCANOPSIS_IMAGE` in `.env` on your old image for now.

5. If your database is still in a Docker volume (every version before 0.4.0
   does this), [move it into a folder](#move-the-database-into-a-folder)
   now, while the old image is still set. A new 0.4.3 `compose.yaml` started
   without the move would create an empty database in `data/postgres` and
   leave your data behind in the volume. If `deploy/compose/data/postgres`
   exists already, skip this step.

6. Set `SCANOPSIS_IMAGE=scanopsis:v0.4.3` in `deploy/compose/.env` and start
   it:

   ```sh
   cd deploy/compose
   docker compose up -d
   ```

   From 0.4.2 the database does not change. From 0.4.1 it changes in a
   moment. From an older version it takes a while on a large database: the change that gives every
   scan its artifact key took about 25 seconds on a laptop with 540,000 scans
   and 4.3 million findings. The log has an `applying migration` and an
   `applied migration` line for each change, the second with its duration,
   for example `"duration":"41ms"`:

   ```sh
   docker compose logs scanopsis | grep -E 'applying migration|applied migration'
   ```

   If it stops partway (for example when the host restarts), start 0.4.3 again
   and it continues. Do not start the old version on a database that is half
   upgraded.

7. [Check the running version](#check-the-running-version). It must show
   `v0.4.3`.

8. Once 0.4.3 runs well and the backup you made before the upgrade (step 2)
   still exists, remove the old volume, if you moved one. Until then it is
   your way back:

   ```sh
   docker volume rm scanopsis_db-data
   ```

The [deployment guide](deployment.md#upgrade-and-roll-back) shows the output
of each step in a recorded upgrade (made with 0.3.0; the schema version of 0.4.3 is 14, the same as 0.4.2).

### Move the database into a folder

Versions before 0.4.0 keep the database in the Docker volume
`scanopsis_db-data`. A volume is deleted by `docker compose down -v` and by
`docker volume prune`. From 0.4.0 the database lives in the folder
`deploy/compose/data/postgres` and the backups in `deploy/compose/backups`,
which no Docker cleanup command (`docker compose down -v`,
`docker volume prune`, `docker system prune`) deletes. Move the data once, with the old version
stopped. **Takes:** a few minutes, plus the copy. You need the free disk space
for a second copy of the database.

1. In `deploy/compose`, stop Scanopsis so that nothing writes any more. The
   database keeps running. Count the rows of every table and keep the
   output:

   ```sh
   docker compose stop scanopsis
   docker compose exec -T db psql -U scanopsis -d scanopsis -c "SELECT table_name, (xpath('/row/c/text()', query_to_xml(format('SELECT count(*) AS c FROM %I', table_name), false, true, '')))[1]::text AS rows FROM information_schema.tables WHERE table_schema = 'public' AND table_type = 'BASE TABLE' ORDER BY 1"
   ```

   Run against a database with sample data (recorded with 0.3.0), it printed:

   ```text
       table_name    | rows
   ------------------+------
    audit_log        | 1
    goose_db_version | 11
    recovery_codes   | 0
    scans            | 2
    secret_findings  | 2
    services         | 2
    sessions         | 0
    settings         | 0
    sonar_analyses   | 2
    sonar_deliveries | 1
    users            | 0
    vulnerabilities  | 2
   (12 rows)
   ```

2. Stop the rest. Do not add `-v`:

   ```sh
   docker compose down
   ```

3. Check that the old volume exists, create the two folders and copy the
   volume into `data/postgres`. The volume is named `<project>_db-data`, and
   the project is `scanopsis` unless you changed `name:` in `compose.yaml`;
   `docker volume ls` shows it.

   ```sh
   # 70 is the user the PostgreSQL image runs as; only it may open the database files.
   docker volume inspect scanopsis_db-data > /dev/null &&
   sudo install -d -m 700 -o 70 -g 70 data/postgres &&
   sudo install -d -m 700 -o 65532 -g 65532 backups &&
   docker run --rm -v scanopsis_db-data:/from -v "$PWD/data/postgres":/to \
     alpine:3.22@sha256:49340b346798875b5592e5d420241f2f9cfba5f47e62ebb1446a1b34734e898f \
     sh -c 'test -z "$(ls -A /to)" || { echo "data/postgres is not empty" >&2; exit 1; }; cp -a /from/. /to/' &&
   sudo chown 70:70 data/postgres &&
   sudo chmod 700 data/postgres
   ```

   The commands are chained, so the block stops at the first one that fails.
   If the first prints `no such volume`, do not go on: without the check,
   `docker run` would create an empty volume and copy nothing. The copy
   refuses to run if `data/postgres` already holds files, so it never mixes
   two databases. Otherwise the commands print nothing and exit with 0. `cp -a`
   keeps the owners of the files, which the database needs; the last two
   commands set the folder itself back to user 70 and mode 700, because the
   copy can carry over the owner and mode of the volume's top folder. The folder
   `data/postgres` then holds one folder named `18`, the PostgreSQL major
   version. `data/postgres` has mode 700 because it holds the database files, and belongs to user 70, the user the PostgreSQL image runs as; only it may open them.

4. Start again. `compose.yaml` is the new one, and `SCANOPSIS_IMAGE` in `.env`
   still names your old image, so that the move is the only change:

   ```sh
   docker compose up -d
   ```

5. Wait until `docker compose ps` shows both containers `(healthy)`, then run
   the count from step 1 again. Every number must be exactly the same as
   before the move. If a number differs, run `docker compose down`, keep both
   the volume and the folder, and
   [open an issue](https://github.com/siyamsarker/scanopsis/issues).

Go on with the upgrade: set the new image only now. The old volume stays until
the upgraded version runs well (step 8 of
[upgrade to the newest version](#upgrade-to-the-newest-version)).

From now on `docker compose down -v` removes only the containers and the
network, and the database is still there after `docker compose up -d`.

### Before you upgrade

#### From 0.4.2

- 0.4.3 does not change the database: the schema version stays 14, and the
  log has no `applying migration` line. **Back up first** anyway, with **Back
  up now** on the Backups page. To go back, set the 0.4.2 image again; no
  restore is needed.
- Every upload now locks its service before it stores the scan. An upload
  that arrives while an administrator archives the service is refused with
  `409` and the code `service_archived`, and nothing is stored. An upload for
  a service that an administrator deleted meanwhile creates the service again
  and stores the scan with `201`, as the next upload would; it failed with a
  server error before. An upload can wait a moment while an administrator
  archives, deletes or renames that same service.
- **Cancel waiting key** on the Services page never removes a SonarQube key
  any more. If SonarQube confirmed the waiting key just before you selected
  it, the key stays linked and the page says "SonarQube already confirmed
  <key>.". Scripts can do the same with
  `PATCH /api/v1/services/{id}` and the body `{"sonar_key_declared": null}`.
  If another key was set on the service just before, the page says
  "<service> is already linked to <key>." instead.
- If `SCANOPSIS_SONAR_SYNC_INTERVAL` is below `1m`, which 0.4.2 accepted,
  nothing is needed: 0.4.3 starts, ignores the variable and logs a warning
  that it is below the minimum, and uses the saved interval, else the default
  of `15m`, also after a reset of the interval or a restore. Remove the
  variable or set `1m` or more. A saved interval below `1m` still runs at most
  once a minute, and the Settings page asks for `1m` or more at the next save.
  Only a new installation, one without an administrator yet, does not start
  with such a value.

#### From 0.4.1

Everything in [from 0.4.2](#from-042), and also:

- The two migrations of 0.4.2 run when 0.4.3 starts. **Back up first**, with **Back up
  now** on the Backups page. Going back to the version you ran before needs
  that backup.
  - Migration 00013 adds the analysis time to the SonarQube deliveries, so an
    analysis that is linked when the first scan of its pipeline arrives keeps
    its pipeline number.
  - Migration 00014 adds to each service the SonarQube key its pipeline
    declared in an upload, and when, while SonarQube has not confirmed it.
  - Both only add empty columns and rewrite no rows, so they take
    milliseconds whatever the size of the database.
- Deliveries that arrived before the upgrade have no analysis time, so an
  analysis linked from one of them later still shows no pipeline number.
- Take over the new job scripts from the
  [GitLab CI guide](integrations/gitlab-ci.md). They keep retrying an upload
  while Scanopsis is down (with curl 7.71 or later), and they send
  `SONAR_PROJECT_KEY` as `sonar_key`, so a service links itself to its
  SonarQube project once an analysis confirms the key, see
  [link the SonarQube project](integrations/gitlab-ci.md#link-the-sonarqube-project).
  Both scripts end with `2`, not `1`, when Trivy cannot scan, and the
  central scan script takes a list of images. A job with
  `allow_failure: exit_codes: [1]` then stops the pipeline on a scan error,
  which it let pass before.
- A key that a pipeline declared and that waits for SonarQube shows on the
  Services page. Setting or clearing a service's SonarQube key there cancels
  it.
- If you run the [accuracy check](verification.md), take its new commands:
  step A counts the secrets in image layers apart, and `collect.sh` keeps the
  backslashes of the API answer.
- The `applied migration` log line gives its duration as text, such as
  `"duration":"1ms"`, no longer as a number of nanoseconds.

#### From 0.4.0

Everything in [from 0.4.1](#from-041), and also:

- The two migrations of 0.4.1 run before those of 0.4.2. **Back up first**,
  with **Back up now** on the Backups page, because 0.4.0 can start on the
  upgraded database but cannot work with it: its uploads fail.
  - Migration 00011 gives every scan its artifact key (the image name without
    its tag, or the folder relative to the repository) and rewrites the
    OS-package targets of image scans with it. It works in batches, and Scanopsis
    serves requests only after all migrations are done, so it is unavailable
    while they run; it took about 25 seconds on a laptop with
    540,000 scans and 4.3 million findings. Rows are rewritten, so the
    database needs free disk space of about the size of the vulnerabilities
    table and its indexes while it runs.
  - Migration 00012 adds two columns to the SonarQube deliveries, used to link
    analyses that arrive before the first scan.
- If the upgrade stops partway, start 0.4.3 again. Do not go back to 0.4.0:
  0.4.0 cannot handle a database where 00011 ran only in part. Going back to
  0.4.0 after a finished upgrade needs the backup you made first.
- Totals are correct at once. A service whose pipelines tagged each image per
  run (`web-frontend:4`, `web-frontend:5`) added the pipelines up before; now
  each image name counts once, with its newest scan, and the risk and the
  counts drop to the real values. The older per-tag scans stop counting.
- Images that differ only by their tag, such as `web-frontend:amd64` and
  `web-frontend:arm64` (platforms or environments) scanned on one branch,
  become **one** artifact at the upgrade, and only the newer scan counts. In the
  same pipeline change that takes over the new job script, pass the optional
  `artifact` parameter for each variant, see
  [keep two variants apart](integrations/gitlab-ci.md#keep-two-variants-apart).
  Rows that are already stored cannot be split afterwards.
- The first data clean-up runs one minute after Scanopsis starts. Version 0.4.0
  kept every tag's latest scan, because each tag counted as "the latest". After
  the upgrade those are ordinary older scans, so the first clean-up deletes the
  ones older than the retention period, with their findings, and empties the
  stored original report of every scan that is not current and is older than 30
  days. See the [retention rules](operations.md#data-retention). To keep more
  history, raise the retention period on the Settings page **before** you
  upgrade.
- Take over the new job script from the
  [GitLab CI guide](integrations/gitlab-ci.md): it uploads the full report
  (every severity, unfixed findings, secrets in image layers) whatever
  `TRIVY_SEVERITY` or `TRIVY_IGNORE_UNFIXED` the job sets.
- The first setup screen of a new installation is **Welcome to Scanopsis**; an
  existing installation is not affected.

#### From 0.3.0

Everything in [from 0.4.0](#from-040), and also:

- 0.4.0 added no migration, so the migrations that run are the four of 0.4.1
  and 0.4.2.
  Which storage a version uses is decided by the `compose.yaml` you run it
  with: with the `compose.yaml` of 0.3.0, the database is the old Docker
  volume. Going back to 0.3.0 needs the backup you made before the upgrade.
- The database lives in a folder now, not in the Docker volume. Follow
  [move the database into a folder](#move-the-database-into-a-folder) before
  you start 0.4.3.
- Take over the new `compose.yaml`: longer stop times (2 minutes for the
  database, 30 seconds for Scanopsis), 256 MB of shared memory for the
  database, and the `backups` folder mounted into Scanopsis.
- Backups and restores are new. After the first sign-in, open **Backups** in
  the Admin menu and turn on scheduled backups; see
  [back up from the Backups page](operations.md#back-up-from-the-backups-page).

#### From 0.2.0

Everything in [from 0.3.0](#from-030), and also:

- The database changes on the way to 0.4.3. If you need to go back, restore
  the backup you made before the upgrade.
- The web interface is new. Ask users to reload pages that are open during the
  upgrade.
- Scripts that read `GET /api/v1/services/{id}/trends` receive at most the
  newest 300 scans of the chosen period. The answer says `truncated: true`
  when it left older scans out.

#### From 0.1.0

Everything in [from 0.2.0](#from-020), and the database changes more on the
way (every migration since 0.1.0 runs), and also:

- If a reverse proxy runs in front of Scanopsis, set
  `SCANOPSIS_TRUSTED_PROXIES` to its address. Without it, all users count as
  one client and share a small number of request slots.
- Take over the memory limit, `GOMEMLIMIT`, the process limit and log rotation
  from the new `compose.yaml`.
- Users are signed out after 30 minutes without use and 12 hours after sign-in
  (it was 8 and 24 hours), unless you set other values on the Settings page.
- If you change the GitLab URL later, GitLab users sign in as new viewers. The
  [operations guide](operations.md) explains this under "GitLab users and the
  GitLab URL".

## Roll back to the previous version

**When:** the upgrade does not work for you. A rollback is a way out of an
emergency, not a place to stay: please
[open an issue](https://github.com/siyamsarker/scanopsis/issues) that says
what went wrong, and upgrade again once it is fixed.

If the upgrade changed the database ([before you upgrade](#before-you-upgrade)
says so), you need the backup from before the upgrade, because the old
version cannot read the updated database. Otherwise skip the restore in
step 3.

1. Find the image of the version you ran before, for example 0.2.0:

   ```sh
   docker image ls scanopsis
   ```

   If `scanopsis:v0.2.0` is no longer there, build it again in your clone:

   ```sh
   git fetch --depth 1 origin release/0.2.0:release/0.2.0 tag v0.2.0
   git switch release/0.2.0
   make image IMAGE=scanopsis:v0.2.0
   ```

2. Set `SCANOPSIS_IMAGE=scanopsis:v0.2.0` in `deploy/compose/.env`.

3. Restore the backup and start the old version, as the
   [deployment guide](deployment.md#roll-back) describes. Without a
   database change, only run `docker compose up -d` in `deploy/compose`.

Versions before 0.4.0 keep the database in a Docker volume, not in
`data/postgres`. To go back to one, use the `compose.yaml` of that version and
restore the backup into its database, as the
[deployment guide](deployment.md#roll-back) shows; the folder `data/postgres`
is not read by the old version.

If you came from 0.1.0, use 0.1.0 wherever these steps say 0.2.0. A restore
loses everything that changed after the backup, including scans uploaded in
the meantime.

## Maintainers: publish a version

Publish only from `main`, and only a commit you tested. Replace `X.Y.Z` with
the new version and `A.B.C` with the version it replaces.

1. On a clean, up-to-date `main`, run every check. There is no CI service, so
   this is the whole gate:

   ```sh
   git switch main && git pull --ff-only
   make check test-integration generate-check coverage perf e2e image-scan dco commits
   ```

2. Make the guides point to the new version. They always show the newest
   version in their commands, never a placeholder:

   - List every place that names the version it replaces, with
     `git grep -n 'A\.B\.C'`, and change each install or upgrade command,
     example output and image tag to `X.Y.Z`. Leave the sentences that
     describe how an older version behaved.
   - README: the badge, the status note and the quick start. `SECURITY.md`:
     the supported version.
   - This page: the short version, the older-versions note (add `A.B.C`), the
     check-the-version output, the upgrade table and the upgrade steps. Add a
     "From A.B.C" list to [before you upgrade](#before-you-upgrade) and extend
     the lists of older versions with it, so that every older version still
     has a path to the newest one.
   - If the API changed, set `info.version` in `api/openapi.yaml` and run
     `make generate`.

   Commit with `git commit -s -am "docs: prepare version X.Y.Z"`.

3. Tag the commit, create the branch and push both with `main`:

   ```sh
   git tag -a vX.Y.Z -m "vX.Y.Z"
   git branch release/X.Y.Z vX.Y.Z
   git push origin main vX.Y.Z release/X.Y.Z
   ```

4. Check it as an operator would: [install the version](#install-the-newest-version)
   in an empty directory and confirm that `git describe` prints `vX.Y.Z`.

Never move a tag and never push to a version branch again. In the GitHub
repository settings, under **Rules**, block updates and deletion for branches
matching `release/*` and tags matching `v*`, and protect `main`.

---

[Back to the README](../README.md) · [All guides](../README.md#documentation) · Found a mistake? [Open an issue](https://github.com/siyamsarker/scanopsis/issues)
