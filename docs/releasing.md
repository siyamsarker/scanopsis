# Releasing

This guide is for maintainers. It explains how to cut a release, what the release workflow does, how anyone can verify a release, and how to roll back.

A release is a Git tag of the form `vX.Y.Z`. Pushing the tag starts the workflow in `.github/workflows/release.yml`, which publishes the container image to `ghcr.io/siyamsarker/scanopsis` and creates the GitHub release.

## Before the first release

When the repository is published, the maintainer must enable these GitHub settings. They are repository settings, so they cannot be set from the code:

- **Branch protection on `main`.** Require a pull request, require the checks of the CI workflow to pass, and require review from code owners. Block force pushes and deletion of the branch.
- **Tag protection.** Restrict who can create `v*` tags, for example with a repository ruleset, because a tag starts a publication.
- **Private vulnerability reporting.** Enable it under Settings, Code security, so that reporters have the private channel that `SECURITY.md` describes.
- **Dependabot alerts and security updates.** Enable both. Version updates are configured in `.github/dependabot.yml`.
- **Release environment.** Create an environment named `release` under Settings, Environments. Add required reviewers and limit its deployment to tags matching `v*`. The publishing job runs in this environment, so it waits for an approval, and a workflow changed on another branch cannot publish.
- **Workflow permissions.** Set the default `GITHUB_TOKEN` permission to read-only. The release workflow asks for write access in the one job that needs it.
- **Package visibility.** The first push creates the package `scanopsis` on ghcr.io as private. Link it to the repository and make it public under the package settings if users should pull it without signing in.

## Cut a release

1. Make sure `main` is green, including the Coverage and Performance jobs.
2. In `CHANGELOG.md`, rename the `## [Unreleased]` heading to `## [X.Y.Z] - YYYY-MM-DD`. Add a new empty `## [Unreleased]` section above it. At the end of the file, point `[Unreleased]` at `compare/vX.Y.Z...HEAD` and add a `[X.Y.Z]` reference. Check the notes that will be published:

   ```sh
   scripts/release-notes.sh X.Y.Z
   ```

3. Commit the change with a sign-off and merge it to `main` through a pull request.
4. Create and push a signed tag on the merge commit:

   ```sh
   git switch main && git pull
   git tag -s vX.Y.Z -m "vX.Y.Z"
   git push origin vX.Y.Z
   ```

   Use a pre-release version such as `1.0.0-rc.1` for a release candidate. It is published as a GitHub pre-release and does not move the `latest` image tag.
5. Watch the Release workflow. When it succeeds, the release page shows the notes, the image digest, the SBOMs and `checksums.txt`.

## What the workflow does

The workflow has two jobs.

**Verify** has read-only permissions. It fails if any of these is false:

- The tag is `vX.Y.Z`, with an optional pre-release suffix.
- The tag commit is reachable from `main`.
- `CHANGELOG.md` has a non-empty section for the version.
- `make check` and `make test-integration` pass.

**Build and publish** is the only job with `contents: write` and `packages: write`, and it runs in the `release` environment. It runs `scripts/release-build.sh`, which:

1. Builds `linux/amd64` and `linux/arm64` with `docker buildx`. The Dockerfile cross-compiles on the runner, so no emulation is involved. BuildKit comes from an image pinned by digest.
2. Scans both images with the same Trivy gates as CI: any HIGH or CRITICAL vulnerability, or any secret, stops the release before anything is pushed.
3. Pushes `ghcr.io/siyamsarker/scanopsis:X.Y.Z`, and `:latest` for a final release, and reads the image digest from the build metadata.
4. Writes a CycloneDX SBOM for each architecture from the pushed image, with the Trivy version pinned in the workflow and checked against a SHA-256 value.
5. Appends the image digest to the release notes.
6. Writes `checksums.txt` with the SHA-256 of the notes and of both SBOMs.

The workflow then creates the GitHub release with the `gh` CLI and attaches the SBOMs and `checksums.txt`. The registry login uses `GITHUB_TOKEN` through an environment variable and is removed at the end of the job. The workflow uses no third-party action: only `actions/checkout`, `actions/setup-go` and `actions/setup-node`, pinned by commit.

### Dry run

To test the pipeline without publishing, start the workflow by hand: Actions, Release, Run workflow, and enter a version that has a changelog section. A manual run never logs in to the registry, never pushes and never creates a release. It builds each architecture into the runner's Docker daemon, writes the SBOMs, the notes and the checksums, and shows them in the job summary.

You can also run the build part on your machine. It needs Docker with buildx, `jq` and `trivy`:

```sh
DRY_RUN=true scripts/release-build.sh X.Y.Z
```

The output is in `bin/release`. Nothing is pushed and no credentials are needed.

## Verify a release

Anyone can check a release.

1. Download `checksums.txt`, the SBOMs and `release-notes.md` from the release page. Check them:

   ```sh
   sha256sum -c checksums.txt
   ```

   On macOS, use `shasum -a 256 -c checksums.txt`.
2. Find the image digest in the release notes, under Container image. Compare it with the registry:

   ```sh
   docker buildx imagetools inspect ghcr.io/siyamsarker/scanopsis:X.Y.Z
   ```

   The `Digest` line must equal the one in the notes.
3. Pin the image by digest in your deployment, because a tag can be moved and a digest cannot:

   ```yaml
   image: ghcr.io/siyamsarker/scanopsis@sha256:<digest from the notes>
   ```

4. Use the SBOM to check the contents. Scan it for known vulnerabilities, for example with `trivy sbom scanopsis-X.Y.Z-linux-amd64.cdx.json`.

The checksums prove that the files you downloaded are the ones the workflow produced only if you trust the release page. The digest in the notes is the stronger anchor, because the registry derives it from the image content.

## Roll back

A published version is never changed or deleted, because users may have pinned its digest.

- **A bad release.** Fix the problem on `main`, add a changelog section and publish the next patch version. Edit the release page of the bad version to state that it is withdrawn and name its replacement. Mark it as a pre-release if you want it to stop being the latest.
- **`:latest` points at a bad image.** Point it at the previous good image. Use the digest from the previous release notes, with an account that can write to the package:

  ```sh
  docker buildx imagetools create --tag ghcr.io/siyamsarker/scanopsis:latest \
    ghcr.io/siyamsarker/scanopsis@sha256:<previous digest>
  ```

- **The workflow failed half way.** If the image was pushed but the release was not created, do not delete the tag. Fix the cause and publish the next patch version, or create the release by hand from the files of a dry run. If nothing was pushed, delete the tag locally and on the remote, fix the problem and tag again.
- **An operator who upgraded.** Operators can return to the previous version by setting the previous digest in their Compose file. A newer version applies its database migrations at start and does not undo them when you go back, so restore the database backup taken before the upgrade first. See [backup and restore](operations.md#backup-and-restore).
