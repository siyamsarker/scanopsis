# Versions and upgrades

Which version to install, and how to upgrade or roll back.

**Audience:** Everyone · **Back to:** [README](../README.md) · [All guides](../README.md#documentation)

---

## The short version

Install **0.4.0**, the newest version. It is on the branch `release/0.4.0`
with the tag `v0.4.0`. Scanopsis publishes no container image, so you build
it yourself from that branch, as [install the newest version](#install-the-newest-version)
shows.

| Do | Do not |
| --- | --- |
| Deploy the newest version branch, `release/0.4.0`. | Deploy from `main`. It is development code and can be broken at any time. |
| Upgrade as soon as a newer version is published. Only the newest version receives fixes. | Install an older version, or stay on one. |
| Back up the database and the secret key before every upgrade. | Upgrade without a backup. Some versions change the database, and that cannot be undone. |
| Tag your image with the version: `scanopsis:v0.4.0`. | Reuse one image name for different versions; you lose the way back. |

A version branch never changes once it is published. Fixes go into the next
version.

> **Older versions:** the branches `release/0.1.0`, `release/0.2.0` and
> `release/0.3.0` stay on GitHub only as a record. They receive no fixes,
> including security fixes, and are not meant for new installations. If you
> run one,
> [upgrade to 0.4.0](#upgrade-to-the-newest-version); you can go straight
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
   git clone --branch release/0.4.0 --depth 1 https://github.com/siyamsarker/scanopsis.git
   cd scanopsis
   git describe --tags --always --dirty
   ```

   The last command prints `v0.4.0`. If it prints a commit hash, run
   `git fetch --depth 1 origin tag v0.4.0` and try again.

2. Build the image:

   ```sh
   make image IMAGE=scanopsis:v0.4.0
   ```

3. Continue with [step 2 of the getting started guide](getting-started.md#step-2-create-the-secrets).
   `deploy/compose/.env.example` already names the image `scanopsis:v0.4.0`.

## Check the running version

```sh
curl -s http://127.0.0.1:8080/api/v1/version
```

It prints the version the image was built from. The line looks like this
(an example: the commit and the Go version on your build differ):

```text
{"version":"v0.4.0","commit":"<commit>","go":"<go version>"}
```

`version` must be `v0.4.0`.

If `version` is not the one you expect, the container runs another image.
Build the image again from the version branch.

## Upgrade to the newest version

**When:** a newer version is published. **Takes:** about 10 minutes, plus
the backup.

You can upgrade from any older version straight to 0.4.0; you do not need to
install the versions in between. The newest version applies every database
change it finds missing.

| You run | Database change | Read first |
| --- | --- | --- |
| 0.3.0 | None. 0.4.0 adds no migration. | [From 0.3.0](#from-030) |
| 0.2.0 | None up to 0.4.0 | [From 0.2.0](#from-020) |
| 0.1.0 | Yes, applied when 0.4.0 starts | [From 0.1.0](#from-010) |

1. Read [before you upgrade](#before-you-upgrade) for the version you run.

2. Back up the database and the secret key, as the
   [operations guide](operations.md) describes under "Back up with pg_dump
   instead" (the Backups page exists only from 0.4.0). If you run 0.4.0 and
   upgrade later, use the Backups page.

3. Get 0.4.0 in your existing clone and build it:

   ```sh
   git fetch --depth 1 origin release/0.4.0:release/0.4.0 tag v0.4.0
   git switch release/0.4.0
   make image IMAGE=scanopsis:v0.4.0
   ```

   A fresh clone, as in [install the newest version](#install-the-newest-version),
   works too.

4. Compare `deploy/compose/compose.yaml` and `deploy/compose/.env.example` of
   0.4.0 with your copies and take over new settings. Copy the new
   `compose.yaml` if you did not change yours: it keeps the database and the
   backups in folders. Leave `SCANOPSIS_IMAGE` in `.env` on your old image for
   now.

5. If your database is still in a Docker volume (every version before 0.4.0
   does this), [move it into a folder](#move-the-database-into-a-folder)
   now, while the old image is still set. A new 0.4.0 `compose.yaml` started
   without the move would create an empty database in `data/postgres` and
   leave your data behind in the volume. If `deploy/compose/data/postgres`
   exists already, skip this step.

6. Set `SCANOPSIS_IMAGE=scanopsis:v0.4.0` in `deploy/compose/.env` and start
   it:

   ```sh
   cd deploy/compose
   docker compose up -d
   ```

   If the database changes, it happens now. On a large database this can take
   a few minutes; the log shows each step.

7. [Check the running version](#check-the-running-version). It must show
   `v0.4.0`.

8. Once 0.4.0 runs well and the backup you made before the upgrade (step 2)
   still exists, remove the old volume, if you moved one. Until then it is
   your way back:

   ```sh
   docker volume rm scanopsis_db-data
   ```

The [deployment guide](deployment.md#upgrade-and-roll-back) shows the output
of each step in a recorded upgrade (made with 0.3.0).

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
     sh -c 'test -z "$(ls -A /to)" || { echo "data/postgres is not empty" >&2; exit 1; }; cp -a /from/. /to/'
   ```

   The commands are chained, so the block stops at the first one that fails.
   If the first prints `no such volume`, do not go on: without the check,
   `docker run` would create an empty volume and copy nothing. The copy
   refuses to run if `data/postgres` already holds files, so it never mixes
   two databases. Otherwise the commands print nothing and exit with 0. `cp -a`
   keeps the owners of the files, which the database needs. The folder
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

#### From 0.3.0

- The database schema does not change: 0.4.0 adds no migration, and the last
  one is still the one 0.3.0 has. Going back to 0.3.0 later needs no restore
  for the schema, but remember that 0.3.0 reads the database only from the
  Docker volume of its own `compose.yaml`.
- The database lives in a folder now, not in the Docker volume. Follow
  [move the database into a folder](#move-the-database-into-a-folder) before
  you start 0.4.0.
- Take over the new `compose.yaml`: longer stop times (2 minutes for the
  database, 30 seconds for Scanopsis), 256 MB of shared memory for the
  database, and the `backups` folder mounted into Scanopsis.
- Backups and restores are new. After the first sign-in, open **Backups** in
  the Admin menu and turn on scheduled backups; see
  [back up from the Backups page](operations.md#back-up-from-the-backups-page).

#### From 0.2.0

Everything in [from 0.3.0](#from-030), and also:

- The database does not change from 0.2.0 to 0.4.0. If you need to go back,
  0.2.0 runs on the same database, without restoring the backup.
- The web interface is new. Ask users to reload pages that are open during the
  upgrade.
- Scripts that read `GET /api/v1/services/{id}/trends` receive at most the
  newest 300 scans of the chosen period. The answer says `truncated: true`
  when it left older scans out.

#### From 0.1.0

Everything in [from 0.2.0](#from-020), except that the database does change
along the way, and also:

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
