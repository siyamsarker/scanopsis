# Versions and branches

This guide explains how Scanopsis versions are published on GitHub and how you
deploy, upgrade and roll back a version. The first part is for operators. The
last part is for maintainers who publish a version.

Scanopsis publishes no container image and no release files. You build the
image yourself from a version branch, then run it with
[Docker Compose](../deploy/compose/README.md).

> **Status:** the first version, 0.1.0, is published as the branch
> `release/0.1.0` with the tag `v0.1.0`. Versions before 1.0.0 are early
> versions: the accuracy check with real pipeline data is still open, so
> compare the numbers with your own Trivy and SonarQube results before you
> rely on them. See the [accuracy check](verification.md).

## What is on GitHub

The repository has two kinds of branches, and nothing else.

| Name | What it is | Deploy it? |
|---|---|---|
| `main` | The latest development code. It can be unfinished or broken between versions. All changes land here first. | No |
| `release/X.Y.Z` | One finished version, for example `release/0.1.0`. The branch is created once, at the tag `vX.Y.Z`, and never changes after that. | Yes |

Every version branch has a matching annotated tag `vX.Y.Z` on the same commit.
The tag is the name of the tested commit. The branch is what you clone.

Version numbers follow [semantic versioning](https://semver.org/spec/v2.0.0.html).
A fix is never added to a version branch. It goes to `main` and ships in a new
version, so a fix for 1.0.1 arrives as 1.0.2.

## Find the newest version

1. List the version branches. Use either of these:

   - Open the [Branches page](https://github.com/siyamsarker/scanopsis/branches)
     on GitHub and look at the `release/` branches.
   - Run this command:

     ```sh
     git ls-remote --heads https://github.com/siyamsarker/scanopsis.git 'release/*'
     ```

   Expected result: one line for each version, for example
   `refs/heads/release/0.1.0`. No output means that no version has been
   published yet.

2. Pick the highest number. Compare the numbers one part at a time, so 1.10.0
   is newer than 1.9.0. The newest version is the only one that receives
   security fixes, see the [security policy](../SECURITY.md#supported-versions).

3. Read the `CHANGELOG.md` of that branch. The section of the version lists
   what it contains.

## Deploy a version

These steps replace "step 1" of the [getting started guide](getting-started.md#step-1-build-the-image).
Replace `X.Y.Z` with the version you picked.

1. Clone the version branch:

   ```sh
   git clone --branch release/X.Y.Z --depth 1 https://github.com/siyamsarker/scanopsis.git
   cd scanopsis
   ```

   The clone also fetches the tag `vX.Y.Z`, because the tag points at the
   commit of the branch. Check it:

   ```sh
   git describe --tags --always --dirty
   ```

   Expected result: `vX.Y.Z`. If you see a commit hash, fetch the tag with
   `git fetch --depth 1 origin tag vX.Y.Z` and run the command again. If you
   see a version followed by `-dirty`, you changed a file, and the image would
   carry that suffix.

2. Build the image. It takes the version from the tag, and you give it a tag
   of its own that names the version:

   ```sh
   make image IMAGE=scanopsis:vX.Y.Z
   ```

   Expected result: the build ends without an error, and
   `docker image ls scanopsis:vX.Y.Z` lists the image.

3. Continue with the [getting started guide](getting-started.md#step-2-create-the-secrets)
   from step 2. In step 3 of that guide, set `SCANOPSIS_IMAGE=scanopsis:vX.Y.Z`
   in `deploy/compose/.env`. With existing Compose files, set it there.

To try `main` instead, use `--branch main` in step 1. The version then shows as
a commit hash and not as `vX.Y.Z`.

## Check the running version

1. Ask the server for its version:

   ```sh
   curl -s http://127.0.0.1:8080/api/v1/version
   ```

   Expected result, for version X.Y.Z (the commit and Go version differ):

   ```text
   {"version":"vX.Y.Z","commit":"1a2b3c4","go":"go1.27.4"}
   ```

2. If `version` is not `vX.Y.Z`, you are running an image that was built from
   another commit. Build the image again from the version branch.

You can also run `docker run --rm scanopsis:vX.Y.Z --version` to see the
version that an image contains.

## Upgrade to a newer version

Schema changes are applied when the new version starts. They cannot be undone,
so the backup in step 2 is the only way back.

1. Pick the new version and read its section of `CHANGELOG.md`, see
   [find the newest version](#find-the-newest-version). Look for changes that
   need action from you.

2. Back up the database and the secret key, as described in
   [backup and restore](operations.md#backup-and-restore). Keep the backup
   until you are sure that you do not need to go back.

3. Get the new branch. In your existing clone:

   ```sh
   git fetch --depth 1 origin release/NEW:release/NEW tag vNEW
   git switch release/NEW
   git describe --tags --always --dirty
   ```

   Expected result: `vNEW`. A clone made with `--depth 1` follows one branch
   only, so a plain `git fetch origin` does not find the new branch: name it
   as above. You can also make a fresh clone, as in
   [deploy a version](#deploy-a-version).

4. Build the image with a tag of its own, so that the old image stays on the
   host:

   ```sh
   make image IMAGE=scanopsis:vNEW
   ```

5. In `deploy/compose/.env`, set `SCANOPSIS_IMAGE=scanopsis:vNEW`, then
   recreate the container:

   ```sh
   cd deploy/compose
   docker compose up -d
   ```

   Expected result: Compose recreates `scanopsis` and leaves the database
   alone. Wait about 30 seconds.

6. Check the version, as in [check the running version](#check-the-running-version).
   It must show `vNEW`. The [upgrade steps](deployment.md#upgrade) show how to
   check the schema version as well.

## Roll back to the previous version

A version branch never changes, so the previous version is always available.
You must restore the backup that you took before the upgrade, because the
database may have been changed in a way that the old version does not know.

1. Go back to the previous branch and build its image. If the clone no longer
   has the branch, fetch it as in step 3 of the upgrade first.

   ```sh
   git switch release/OLD
   make image IMAGE=scanopsis:vOLD
   ```

2. Set `SCANOPSIS_IMAGE=scanopsis:vOLD` in `deploy/compose/.env`.

3. Restore the backup and start the old version, as in the
   [roll back steps](deployment.md#roll-back).

4. Check the version. It must show `vOLD`.

Everything that changed after the backup is lost. Scans that pipelines upload
later are stored again.

## Maintainers: publish a version

Publish a version only from `main`, and only when the commit is the one you
tested. Replace `X.Y.Z` with the new version and use the date of the day.

1. Make sure the work tree is clean and on the commit to publish:

   ```sh
   git switch main
   git pull --ff-only
   git status --short
   ```

   Expected result: no output from the last command.

2. Run every local check. There is no CI service, so this is the whole gate:

   ```sh
   make check test-integration generate-check coverage perf e2e image-scan
   ```

   Expected result: every target ends without an error. Fix failures on
   `main` and start again.

3. In `CHANGELOG.md`, move the entries of `## [Unreleased]` under a new heading
   `## [X.Y.Z] - YYYY-MM-DD`, leave an empty `## [Unreleased]` above it, and
   update the link references at the end of the file:

   ```text
   [Unreleased]: https://github.com/siyamsarker/scanopsis/compare/vX.Y.Z...main
   [X.Y.Z]: https://github.com/siyamsarker/scanopsis/tree/release/X.Y.Z
   ```

   Use a compare link between the previous and the new tag from the second
   version on.

4. Commit the change with a sign-off:

   ```sh
   git commit -s -am "docs: prepare version X.Y.Z"
   ```

5. Tag the commit. Sign the tag with `-s` instead of `-a` if you have a signing
   key:

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

   Expected result: GitHub lists the new branch and the new tag.

8. Check the result as an operator would. In an empty directory, run the
   commands of [deploy a version](#deploy-a-version) and confirm that
   `git describe --tags --always --dirty` shows `vX.Y.Z`.

Never move a tag, and never push to a version branch again. If a version has a
mistake, fix it on `main` and publish the next version.

### Protect the branches and tags

Set these rules in the GitHub repository settings, under **Rules**:

- A ruleset for the branches matching `release/*` that blocks updates and
  deletion. The push that creates a branch is still allowed.
- A ruleset for the tags matching `v*` that blocks updates and deletion.
- Branch protection for `main`, so that changes arrive through pull requests
  and `main` cannot be deleted or force-pushed.

Keep Dependabot security updates turned off, because they create extra
branches, and the repository must have only `main` and `release/*` branches.
Dependabot alerts can stay on: they only report.
