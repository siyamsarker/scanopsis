# Versions and upgrades

Which version to run, and how to install, upgrade or roll back a version.

**Audience:** Everyone · **Back to:** [README](../README.md) · [All guides](../README.md#documentation)

---

## The short version

Scanopsis publishes no container image. You build it yourself from a
**version branch**. The newest version is **0.3.0**, on the branch
`release/0.3.0`.

| Do | Do not |
| --- | --- |
| Deploy from a version branch, `release/X.Y.Z`. | Deploy from `main`. It is development code and can be broken at any time. |
| Run the newest version. Only it receives fixes. | Stay on an old version for long. |
| Back up the database and the secret key before every upgrade. | Upgrade without a backup. A newer version changes the database, and that cannot be undone. |
| Tag your image with the version, for example `scanopsis:v0.3.0`. | Reuse one image name for different versions; you lose the way back. |

A version branch never changes once it is published. Fixes go into the next
version.

Versions before 1.0.0 are early versions: compare the numbers with your own
Trivy and SonarQube results before you rely on them. The
[accuracy check](verification.md) shows how.

## Find the newest version

Open the [Branches page](https://github.com/siyamsarker/scanopsis/branches)
on GitHub and look for the highest `release/X.Y.Z`, or run:

```sh
git ls-remote --heads https://github.com/siyamsarker/scanopsis.git 'release/*'
```

Compare the numbers one part at a time: 1.10.0 is newer than 1.9.0.

## Deploy a version

Do this for a new installation. Replace `X.Y.Z` with the version.

1. Get the version and check it:

   ```sh
   git clone --branch release/X.Y.Z --depth 1 https://github.com/siyamsarker/scanopsis.git
   cd scanopsis
   git describe --tags --always --dirty
   ```

   The last command prints `vX.Y.Z`. If it prints a commit hash, run
   `git fetch --depth 1 origin tag vX.Y.Z` and try again.

2. Build the image:

   ```sh
   make image IMAGE=scanopsis:vX.Y.Z
   ```

3. Continue with [step 2 of the getting started guide](getting-started.md#step-2-create-the-secrets),
   and set `SCANOPSIS_IMAGE=scanopsis:vX.Y.Z` in `deploy/compose/.env`.

## Check the running version

It prints the version the image was built from, for example:

```sh
curl -s http://127.0.0.1:8080/api/v1/version
```

```text
{"version":"v0.2.0","commit":"a58b46d","go":"go1.27.4"}
```

If `version` is not the one you expect, the container runs another image.
Build the image again from the version branch.

## Upgrade to a newer version

**When:** a newer version is published. **Takes:** about 10 minutes, plus
the backup. Replace `NEW` with the new version, for example `0.3.0`.

1. Read [before you upgrade](#before-you-upgrade) below.

2. Back up the database and the secret key, as the
   [operations guide](operations.md) describes under "Backup and restore".

3. Get the new version in your existing clone and build it:

   ```sh
   git fetch --depth 1 origin release/NEW:release/NEW tag vNEW
   git switch release/NEW
   make image IMAGE=scanopsis:vNEW
   ```

   A fresh clone, as in [deploy a version](#deploy-a-version), works too.

4. Compare `deploy/compose/compose.yaml` and `deploy/compose/.env.example` of
   the new version with your copies and take over new settings.

5. Set `SCANOPSIS_IMAGE=scanopsis:vNEW` in `deploy/compose/.env` and start it:

   ```sh
   cd deploy/compose
   docker compose up -d
   ```

   The new version updates the database when it starts. On a large database
   this can take a few minutes; the log shows each step.

6. [Check the running version](#check-the-running-version). It must show
   `vNEW`.

### Before you upgrade

For **0.2.0 to 0.3.0**:

- The database does not change. If you need to go back, 0.2.0 runs on the
  same database, without restoring the backup.
- The web interface is new. Ask users to reload pages that are open during the
  upgrade.
- Scripts that read `GET /api/v1/services/{id}/trends` receive at most the
  newest 300 scans of the chosen period. The answer says `truncated: true`
  when it left older scans out.

For **0.1.0 to 0.2.0**, and so also from 0.1.0 straight to 0.3.0:

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

**When:** the new version does not work for you. If the new version changed
the database ([before you upgrade](#before-you-upgrade) says so), you need the
backup from before the upgrade, because the old version cannot read the
updated database. Otherwise skip the restore in step 3.

1. Build the old version again:

   ```sh
   git switch release/OLD
   make image IMAGE=scanopsis:vOLD
   ```

2. Set `SCANOPSIS_IMAGE=scanopsis:vOLD` in `deploy/compose/.env`.

3. Restore the backup and start the old version, as the
   [deployment guide](deployment.md) describes under "Roll back". Without a
   database change, only run `docker compose up -d` in `deploy/compose`.

A restore loses everything that changed after the backup, including scans
uploaded in the meantime.

## Maintainers: publish a version

Publish only from `main`, and only a commit you tested. Replace `X.Y.Z` with
the new version.

1. On a clean, up-to-date `main`, run every check. There is no CI service, so
   this is the whole gate:

   ```sh
   git switch main && git pull --ff-only
   make check test-integration generate-check coverage perf e2e image-scan
   ```

2. Update the version in the README (badge, status and quick start) and at the
   top of this page. Replace the [before you upgrade](#before-you-upgrade) list
   with what operators must do for this version. If the API changed, set
   `info.version` in `api/openapi.yaml` and run `make generate`. Commit with
   `git commit -s -am "docs: prepare version X.Y.Z"`.

3. Tag the commit, create the branch and push both with `main`:

   ```sh
   git tag -a vX.Y.Z -m "vX.Y.Z"
   git branch release/X.Y.Z vX.Y.Z
   git push origin main vX.Y.Z release/X.Y.Z
   ```

4. Check it as an operator would: [deploy the version](#deploy-a-version) in
   an empty directory and confirm that `git describe` prints `vX.Y.Z`.

Never move a tag and never push to a version branch again. In the GitHub
repository settings, under **Rules**, block updates and deletion for branches
matching `release/*` and tags matching `v*`, and protect `main`.

---

[Back to the README](../README.md) · [All guides](../README.md#documentation) · Found a mistake? [Open an issue](https://github.com/siyamsarker/scanopsis/issues)
