# Versions and upgrades

Which version to install, and how to upgrade or roll back.

**Audience:** Everyone · **Back to:** [README](../README.md) · [All guides](../README.md#documentation)

---

## The short version

Install **0.3.0**, the newest version. It is on the branch `release/0.3.0`
with the tag `v0.3.0`. Scanopsis publishes no container image, so you build
it yourself from that branch, as [install the newest version](#install-the-newest-version)
shows.

| Do | Do not |
| --- | --- |
| Deploy the newest version branch, `release/0.3.0`. | Deploy from `main`. It is development code and can be broken at any time. |
| Upgrade as soon as a newer version is published. Only the newest version receives fixes. | Install an older version, or stay on one. |
| Back up the database and the secret key before every upgrade. | Upgrade without a backup. Some versions change the database, and that cannot be undone. |
| Tag your image with the version: `scanopsis:v0.3.0`. | Reuse one image name for different versions; you lose the way back. |

A version branch never changes once it is published. Fixes go into the next
version.

> **Older versions:** the branches `release/0.1.0` and `release/0.2.0` stay
> on GitHub only as a record. They receive no fixes, including security
> fixes, and are not meant for new installations. If you run one,
> [upgrade to 0.3.0](#upgrade-to-the-newest-version); you can go straight
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
   git clone --branch release/0.3.0 --depth 1 https://github.com/siyamsarker/scanopsis.git
   cd scanopsis
   git describe --tags --always --dirty
   ```

   The last command prints `v0.3.0`. If it prints a commit hash, run
   `git fetch --depth 1 origin tag v0.3.0` and try again.

2. Build the image:

   ```sh
   make image IMAGE=scanopsis:v0.3.0
   ```

3. Continue with [step 2 of the getting started guide](getting-started.md#step-2-create-the-secrets).
   `deploy/compose/.env.example` already names the image `scanopsis:v0.3.0`.

## Check the running version

```sh
curl -s http://127.0.0.1:8080/api/v1/version
```

It prints the version the image was built from. For 0.3.0:

```text
{"version":"v0.3.0","commit":"55748a2","go":"go1.27.4"}
```

If `version` is not the one you expect, the container runs another image.
Build the image again from the version branch.

## Upgrade to the newest version

**When:** a newer version is published. **Takes:** about 10 minutes, plus
the backup.

You can upgrade from any older version straight to 0.3.0; you do not need to
install the versions in between. The newest version applies every database
change it finds missing.

| You run | Database change | Read first |
| --- | --- | --- |
| 0.2.0 | None | [From 0.2.0](#from-020) |
| 0.1.0 | Yes, applied when 0.3.0 starts | [From 0.1.0](#from-010) |

1. Read [before you upgrade](#before-you-upgrade) for the version you run.

2. Back up the database and the secret key, as the
   [operations guide](operations.md) describes under "Backup and restore".

3. Get 0.3.0 in your existing clone and build it:

   ```sh
   git fetch --depth 1 origin release/0.3.0:release/0.3.0 tag v0.3.0
   git switch release/0.3.0
   make image IMAGE=scanopsis:v0.3.0
   ```

   A fresh clone, as in [install the newest version](#install-the-newest-version),
   works too.

4. Compare `deploy/compose/compose.yaml` and `deploy/compose/.env.example` of
   0.3.0 with your copies and take over new settings.

5. Set `SCANOPSIS_IMAGE=scanopsis:v0.3.0` in `deploy/compose/.env` and start
   it:

   ```sh
   cd deploy/compose
   docker compose up -d
   ```

   If the database changes, it happens now. On a large database this can take
   a few minutes; the log shows each step.

6. [Check the running version](#check-the-running-version). It must show
   `v0.3.0`.

The [deployment guide](deployment.md#upgrade-and-roll-back) shows a recorded
upgrade from 0.1.0 to 0.3.0 with the output of each step.

### Before you upgrade

#### From 0.2.0

- The database does not change. If you need to go back, 0.2.0 runs on the
  same database, without restoring the backup.
- The web interface is new. Ask users to reload pages that are open during the
  upgrade.
- Scripts that read `GET /api/v1/services/{id}/trends` receive at most the
  newest 300 scans of the chosen period. The answer says `truncated: true`
  when it left older scans out.

#### From 0.1.0

Everything in [from 0.2.0](#from-020), except that the database does change,
and also:

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
   make check test-integration generate-check coverage perf e2e image-scan
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
