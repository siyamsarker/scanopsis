# Versions and upgrades

This page helps you pick a version, deploy it, upgrade to a newer one and roll back. It also lists what changed in each version.

**Audience:** Everyone · **Back to:** [README](../README.md) · [All guides](../README.md#documentation)

---

Scanopsis publishes no container image and no release files. You build the
image yourself from a version branch. The newest version is **0.2.0**: the
branch `release/0.2.0` with the tag `v0.2.0`.

Versions before 1.0.0 are early versions. The accuracy check with real
pipeline data is still open, so compare the numbers with your own Trivy and
SonarQube results before you rely on them. The
[accuracy check](verification.md) shows how.

## What is on GitHub

The repository has two kinds of branches, and nothing else.

| Name | What it is | Deploy it? |
| --- | --- | --- |
| `main` | The latest development code. It can be unfinished or broken between versions. Every change lands here first. | No |
| `release/X.Y.Z` | One finished version, for example `release/0.2.0`. It is created once, at the tag `vX.Y.Z`, and never changes. | Yes |

Each version branch has an annotated tag `vX.Y.Z` on the same commit. The tag
names the tested commit. The branch is what you clone.

Version numbers follow [semantic versioning](https://semver.org/spec/v2.0.0.html).
A fix never goes into an existing version branch. It goes to `main` and ships
in the next version.

## Find the newest version

1. List the version branches. Open the
   [Branches page](https://github.com/siyamsarker/scanopsis/branches) on
   GitHub, or run:

   ```sh
   git ls-remote --heads https://github.com/siyamsarker/scanopsis.git 'release/*'
   ```

   You see one line per version, for example `refs/heads/release/0.2.0`.

2. Pick the highest number. Compare one part at a time, so 1.10.0 is newer
   than 1.9.0. Only the newest version gets security fixes, see the
   [security policy](../SECURITY.md#supported-versions).

3. Read its entry in the [version history](#version-history) below, especially
   the upgrade notes.

## Deploy a version

This is step 1 of the [getting started guide](getting-started.md), in detail.
Replace `X.Y.Z` with the version you picked.

1. Clone the version branch:

   ```sh
   git clone --branch release/X.Y.Z --depth 1 https://github.com/siyamsarker/scanopsis.git
   cd scanopsis
   git describe --tags --always --dirty
   ```

   The last command prints `vX.Y.Z`. If it prints a commit hash instead, fetch
   the tag with `git fetch --depth 1 origin tag vX.Y.Z` and run it again. If
   it prints `vX.Y.Z-dirty`, a file was changed, and the image would carry
   that suffix.

2. Build the image and give it a tag that names the version:

   ```sh
   make image IMAGE=scanopsis:vX.Y.Z
   ```

   When it ends without an error, `docker image ls scanopsis:vX.Y.Z` lists the
   image.

3. Go on with [step 2 of the getting started guide](getting-started.md#step-2-create-the-secrets).
   There you set `SCANOPSIS_IMAGE=scanopsis:vX.Y.Z` in `deploy/compose/.env`.

To try the development code instead, clone with `--branch main`. The version
then shows as a commit hash, not as `vX.Y.Z`.

## Check the running version

Ask the server:

```sh
curl -s http://127.0.0.1:8080/api/v1/version
```

You get the version, the commit and the Go version, for example:

```text
{"version":"v0.2.0","commit":"a58b46d","go":"go1.27.4"}
```

If `version` is not the one you expect, the container runs an image built
from another commit. Build the image again from the version branch.

To see the version inside an image without starting it:

```sh
docker run --rm scanopsis:vX.Y.Z --version
```

It prints a line such as `scanopsis v0.2.0 (commit a58b46d, go1.27.4)`.

## Upgrade to a newer version

The new version updates the database schema when it starts. That cannot be
undone, so the backup in step 2 is your only way back. Replace `NEW` with
the new version, for example `0.2.0`.

1. Read the upgrade notes of every version between yours and the new one in
   the [version history](#version-history).

2. Back up the database and the secret key, as the
   [operations guide](operations.md) describes under "Backup and restore".
   Keep the backup until you are sure you will not go back.

3. Get the new branch in your existing clone:

   ```sh
   git fetch --depth 1 origin release/NEW:release/NEW tag vNEW
   git switch release/NEW
   git describe --tags --always --dirty
   ```

   The last command prints `vNEW`. A clone made with `--depth 1` follows one
   branch only, so a plain `git fetch origin` does not see the new branch:
   name it as above. A fresh clone, as in [deploy a version](#deploy-a-version),
   works too.

4. Build the image under a new tag, so the old image stays on the host:

   ```sh
   make image IMAGE=scanopsis:vNEW
   ```

5. Set `SCANOPSIS_IMAGE=scanopsis:vNEW` in `deploy/compose/.env`, then
   recreate the container:

   ```sh
   cd deploy/compose
   docker compose up -d
   ```

   Compose recreates `scanopsis` and leaves the database alone. Migrations
   that build indexes on a large database can take a few minutes; the log
   shows each step.

6. [Check the running version](#check-the-running-version). It must show
   `vNEW`. The [deployment guide](deployment.md) shows how to check the schema
   version as well.

## Roll back to the previous version

A version branch never changes, so the previous version is always there. You
must also restore the backup from before the upgrade, because the old version
does not know the new schema.

1. Go back to the previous branch and build its image. If the clone no longer
   has the branch, fetch it as in step 3 of the upgrade.

   ```sh
   git switch release/OLD
   make image IMAGE=scanopsis:vOLD
   ```

2. Set `SCANOPSIS_IMAGE=scanopsis:vOLD` in `deploy/compose/.env`.

3. Restore the backup and start the old version, as the
   [deployment guide](deployment.md) describes under "Roll back".

4. [Check the running version](#check-the-running-version). It must show
   `vOLD`.

Everything that changed after the backup is lost, including scans uploaded
in the meantime. Scans that pipelines upload after the rollback are stored as
usual.

## Version history

The newest version comes first. Each entry lists the main changes and what you
must do when you upgrade to it.

### 0.2.0 (2026-11-22)

Main changes:

- **Account page.** Open it from your name in the header. It lists your
  sessions, and you can end any other session or all of them.
- **Disable, enable and delete users,** on the **Users** page and with the
  `disable-user`, `enable-user` and `delete-user` administration commands.
  Nobody can disable or delete themselves or the last enabled administrator.
- **Export the original Trivy report** of a scan with
  `scanopsis admin export-report <scan-id>`, to check it with `trivy convert`.
- **Faster at scale.** Pages stay well under 150 ms with 90 days of history
  for 500 services. One maximum report now peaks at about 80 MiB of memory
  instead of about 280 MiB.
- **Steady under load.** The server answers `503 busy` with `Retry-After`
  instead of letting requests queue, limits uploads per GitLab project and
  per CI job, and keeps `/readyz` answering while it is busy.
- **SonarQube** webhooks are processed apart from the periodic sync, and
  rejected webhooks are recorded in the audit log.
- **Security.** Shorter sessions by default, stronger recovery codes, GitLab
  users bound to their GitLab instance, a fixed open redirect in GitLab
  sign-in, and sign-in limits that a flood of made-up names cannot reset.

Upgrade notes (from 0.1.0):

- Back up first and follow [upgrade to a newer version](#upgrade-to-a-newer-version).
  The first start applies migrations 7 to 10. They build new indexes without
  blocking writes; on a large database this takes from seconds to a few
  minutes.
- Sessions now end after 30 minutes without use and 12 hours after sign-in
  (they were 8 and 24 hours), unless you stored other values on the Settings
  page. The session cookie now ends when the browser closes.
- Take over the new settings from `compose.yaml` and `.env.example`: a memory
  limit of 512 MiB with `GOMEMLIMIT=400MiB`, a process limit, log rotation and
  more. If a reverse proxy runs in front, set `SCANOPSIS_TRUSTED_PROXIES`,
  because each client address now gets its own share of the request slots.
- The vulnerability search now looks at the dashboard branches and the
  protected branches and tags. Choose **All branches** on the search page for
  the old behaviour.
- Data retention now also expires the latest scans of unprotected branches
  that no dashboard shows. It clears the stored original report 30 days after
  a scan stops being current.
- GitLab users are now bound to the GitLab URL they signed in through. After
  you change the GitLab URL, they sign in as new viewers. The
  [operations guide](operations.md) explains this under "GitLab users and the
  GitLab URL".
- New recovery codes are longer. Existing codes keep working until they are
  used.

### 0.1.0 (2026-11-22)

The first version. It contains:

- Sign-in with GitLab (OpenID Connect) and local accounts, mandatory
  two-factor authentication with recovery codes, a first-run setup protected
  by a one-time token, and administration commands to recover access.
- Upload of Trivy reports (image vulnerabilities and secrets) from GitLab CI
  jobs, authenticated with GitLab CI/CD ID tokens instead of a shared secret.
- The SonarQube integration: signed webhooks, quality gate, ratings, coverage
  and duplication, and a periodic sync.
- The web interface: portfolio, service dashboard with trends and a "fix
  first" list, findings explorer, scan comparison, cross-service CVE search,
  and administration pages for services, users, settings, integration health
  and the audit log. Dark and light themes; works on phones.
- A dashboard branch per service, data retention, and a Docker Compose
  deployment.

## Maintainers: publish a version

Publish only from `main`, and only the commit you tested. Replace `X.Y.Z`
with the new version.

1. Make sure the work tree is clean and on the commit to publish:

   ```sh
   git switch main
   git pull --ff-only
   git status --short
   ```

   The last command prints nothing.

2. Run every local check. There is no CI service, so this is the whole gate:

   ```sh
   make check test-integration generate-check coverage perf e2e image-scan
   ```

   Every target must end without an error. Fix failures on `main` and start
   again.

3. Add the version to the [version history](#version-history): a heading
   `### X.Y.Z (YYYY-MM-DD)` with today's date, the main changes in a few plain
   bullets, and the upgrade notes. Update the newest version named at the top
   of this page and in the README.

4. Commit the change with a sign-off:

   ```sh
   git commit -s -am "docs: prepare version X.Y.Z"
   ```

5. Tag the commit. Use `-s` instead of `-a` if you have a signing key:

   ```sh
   git tag -a vX.Y.Z -m "vX.Y.Z"
   ```

6. Create the version branch at the tag:

   ```sh
   git branch release/X.Y.Z vX.Y.Z
   ```

7. Push `main`, the tag and the branch:

   ```sh
   git push origin main
   git push origin vX.Y.Z release/X.Y.Z
   ```

   GitHub now lists the new branch and the new tag.

8. Check the result as an operator would. In an empty directory, run the
   commands of [deploy a version](#deploy-a-version) and confirm that
   `git describe --tags --always --dirty` prints `vX.Y.Z`.

Never move a tag, and never push to a version branch again. If a version has
a mistake, fix it on `main` and publish the next version.

### Protect the branches and tags

Set these rules in the GitHub repository settings, under **Rules**:

- A ruleset for branches matching `release/*` that blocks updates and
  deletion. The push that creates a branch is still allowed.
- A ruleset for tags matching `v*` that blocks updates and deletion.
- Branch protection for `main`, so changes arrive through pull requests and
  `main` cannot be deleted or force-pushed.

Keep Dependabot security updates off: they create extra branches, and the
repository has only `main` and `release/*`. Dependabot alerts can stay on,
because they only report.

---

[Back to the README](../README.md) · [All guides](../README.md#documentation) · Found a mistake? [Open an issue](https://github.com/siyamsarker/scanopsis/issues)
