# Releasing

This guide is for maintainers. It explains how to cut a release from your machine, how anyone can verify a release, and how to roll back.

A release is a signed Git tag of the form `vX.Y.Z`, a container image on `ghcr.io/siyamsarker/scanopsis` and a GitHub release with the release notes, one SBOM per architecture and `checksums.txt`. The repository has no CI service: you run the checks and the release script yourself.

## Before the first release

These are repository settings on GitHub, so they cannot be set from the code:

- **Branch protection on `main`.** Require a pull request and review from code owners. Block force pushes and deletion of the branch.
- **Tag protection.** Restrict who can create `v*` tags, for example with a repository ruleset.
- **Private vulnerability reporting.** Enable it under Settings, Code security, so that reporters have the private channel that `SECURITY.md` describes.
- **Dependabot alerts.** Enable them to be told about known vulnerabilities in the dependencies. No automatic update pull requests are configured.
- **Package visibility.** The first push creates the package `scanopsis` on ghcr.io as private. Link it to the repository and make it public under the package settings if users should pull it without signing in.

You need Docker with buildx, `jq` and `trivy` on the machine that builds the release, and a GitHub personal access token (classic) with the `write:packages` scope to push the image.

## Cut a release

1. On an up-to-date `main` with a clean working tree, run every check. Each must pass:

   ```sh
   make check
   make test-integration
   make generate-check
   make coverage
   make perf
   make e2e
   make image-scan
   ```

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

   Use a pre-release version such as `1.0.0-rc.1` for a release candidate. It does not move the `latest` image tag.

5. Try the build without publishing. Nothing is pushed and no credentials are needed:

   ```sh
   DRY_RUN=true scripts/release-build.sh X.Y.Z
   ```

6. Log in to the registry and publish. The token is read from standard input, so it does not appear in the shell history or the process list:

   ```sh
   docker login ghcr.io --username <your GitHub user> --password-stdin
   DRY_RUN=false scripts/release-build.sh X.Y.Z
   docker logout ghcr.io
   ```

7. Create the release on GitHub: open Releases, Draft a new release, choose the tag `vX.Y.Z`, set the title `vX.Y.Z`, paste the content of `bin/release/release-notes.md` and attach `bin/release/checksums.txt` and both `bin/release/scanopsis-X.Y.Z-linux-*.cdx.json` files. Mark a pre-release version as a pre-release.

## What the release script does

`scripts/release-build.sh` refuses a version without a non-empty section in `CHANGELOG.md`. It then:

1. Builds `linux/amd64` and `linux/arm64` with `docker buildx`. The Dockerfile cross-compiles on the build machine, so no emulation is involved. BuildKit comes from an image pinned by digest.
2. Scans both images with the same Trivy gates as `make image-scan`: any HIGH or CRITICAL vulnerability, or any secret, stops the release before anything is pushed.
3. With `DRY_RUN=false`, pushes `ghcr.io/siyamsarker/scanopsis:X.Y.Z`, and `:latest` for a final release, and reads the image digest from the build metadata.
4. Writes a CycloneDX SBOM for each architecture, from the pushed image by digest, or from the local build in a dry run.
5. Appends the image digest to the release notes.
6. Writes `checksums.txt` with the SHA-256 of the notes and of both SBOMs.

The output is in `bin/release`.

## Verify a release

Anyone can check a release.

1. Download `checksums.txt`, the SBOMs and the release notes from the release page. Check them:

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

The checksums prove that the files you downloaded are the ones the maintainer published only if you trust the release page. The digest in the notes is the stronger anchor, because the registry derives it from the image content.

## Roll back

A published version is never changed or deleted, because users may have pinned its digest.

- **A bad release.** Fix the problem on `main`, add a changelog section and publish the next patch version. Edit the release page of the bad version to state that it is withdrawn and name its replacement. Mark it as a pre-release if you want it to stop being the latest.
- **`:latest` points at a bad image.** Point it at the previous good image. Use the digest from the previous release notes, with an account that can write to the package:

  ```sh
  docker buildx imagetools create --tag ghcr.io/siyamsarker/scanopsis:latest \
    ghcr.io/siyamsarker/scanopsis@sha256:<previous digest>
  ```

- **The script failed half way.** If the image was pushed but the release was not created, do not delete the tag. Fix the cause and publish the next patch version, or create the release by hand from the files in `bin/release`. If nothing was pushed, delete the tag locally and on the remote, fix the problem and tag again.
- **An operator who upgraded.** Operators can return to the previous version by setting the previous digest in their Compose file. A newer version applies its database migrations at start and does not undo them when you go back, so restore the database backup taken before the upgrade first. See [backup and restore](operations.md#backup-and-restore).
