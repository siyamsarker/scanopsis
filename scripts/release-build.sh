#!/usr/bin/env bash
# Copyright 2026 The Scanopsis Authors
# SPDX-License-Identifier: Apache-2.0
#
# Build the release artefacts of one version: the multi-architecture image,
# a CycloneDX SBOM per architecture, the release notes and checksums.txt, all
# in $OUT (default bin/release).
#
#   DRY_RUN=false  pushes <repo>:<version> (and :latest for a final release)
#                  and takes the SBOMs from the pushed image, by digest. The
#                  caller must have logged in to the registry.
#   DRY_RUN=true   (default) builds each architecture into the local Docker
#                  daemon, pushes nothing and needs no credentials.
#
# Both architectures must pass the vulnerability and secret gates of CI
# before anything is pushed.
#
# Needs docker with buildx, jq, trivy and a clean checkout of the tag.
# Usage: scripts/release-build.sh <x.y.z>
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

version=${1:-}
dry_run=${DRY_RUN:-true}
repo=${IMAGE_REPO:-ghcr.io/siyamsarker/scanopsis}
out=${OUT:-bin/release}
platforms=(linux/amd64 linux/arm64)
# BuildKit for the docker-container driver, pinned by digest like every other
# image this project uses. Update tag and digest together.
buildkit='moby/buildkit:v0.34.2@sha256:c5f8862f52499773af85bc07695b2379ef08e8002352b0df2c4864c91d78dabe'

if ! [[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "usage: release-build.sh <x.y.z>" >&2
  exit 2
fi
case $dry_run in true | false) ;; *) echo "release-build: DRY_RUN must be true or false" >&2; exit 2 ;; esac
case $repo in *[!a-z0-9./:_-]*) echo "release-build: unexpected characters in IMAGE_REPO" >&2; exit 2 ;; esac

sha256() { if command -v sha256sum >/dev/null; then sha256sum "$@"; else shasum -a 256 "$@"; fi; }

rm -rf "$out"
mkdir -p "$out"

# The notes come first: a version without a changelog section stops here.
bash scripts/release-notes.sh "$version" "${CHANGELOG:-CHANGELOG.md}" >"$out/notes.md"

commit=$(git rev-parse --short=12 HEAD | tr -cd 'A-Za-z0-9._+-')
builder="scanopsis-release-$$"
trap 'docker buildx rm -f "$builder" >/dev/null 2>&1 || true' EXIT
docker buildx create --name "$builder" --driver docker-container \
  --driver-opt "image=$buildkit" >/dev/null

# Provenance and SBOM attestations are off so the pushed index holds only the
# two images; the SBOM is produced and checksummed separately below.
common=(--builder "$builder" --provenance=false --sbom=false
  --build-arg "VERSION=$version" --build-arg "COMMIT=$commit")

# Every architecture is built into the local daemon and must pass the same
# Trivy gates as CI before anything is pushed: no HIGH or CRITICAL
# vulnerability and no secret. The dry run keeps the SBOM of this build.
echo "== scan ${platforms[*]} before publishing"
for platform in "${platforms[@]}"; do
  arch=${platform#linux/}
  local_tag="scanopsis-release-check:$version-$arch"
  docker buildx build "${common[@]}" --platform "$platform" --load --tag "$local_tag" .
  trivy image --quiet --image-src docker --scanners vuln --severity HIGH,CRITICAL --exit-code 1 "$local_tag"
  trivy image --quiet --image-src docker --scanners secret --exit-code 1 "$local_tag"
  if [ "$dry_run" = true ]; then
    trivy image --quiet --image-src docker \
      --format cyclonedx --output "$out/scanopsis-$version-linux-$arch.cdx.json" "$local_tag"
  fi
  docker image rm -f "$local_tag" >/dev/null
done

if [ "$dry_run" = false ]; then
  tags=(--tag "$repo:$version")
  case $version in *-*) ;; *) tags+=(--tag "$repo:latest") ;; esac
  echo "== build and push ${platforms[*]} -> $repo:$version"
  docker buildx build "${common[@]}" --platform "$(IFS=,; echo "${platforms[*]}")" \
    "${tags[@]}" --push --metadata-file "$out/build.json" .
  digest=$(jq -r '."containerimage.digest"' "$out/build.json")
  [[ $digest =~ ^sha256:[0-9a-f]{64}$ ]] || { echo "release-build: no image digest in build metadata" >&2; exit 1; }
else
  digest="none (dry run, nothing was pushed)"
fi

echo "== SBOMs"
for platform in "${platforms[@]}"; do
  arch=${platform#linux/}
  sbom="$out/scanopsis-$version-linux-$arch.cdx.json"
  if [ "$dry_run" = false ]; then
    trivy image --quiet --image-src remote --platform "$platform" \
      --format cyclonedx --output "$sbom" "$repo@$digest"
  fi
  jq -e '.bomFormat == "CycloneDX" and (.components | length > 0)' "$sbom" >/dev/null ||
    { echo "release-build: $sbom is not a usable CycloneDX document" >&2; exit 1; }
done

# The notes name the image by digest, which is what a user should pin.
{
  cat "$out/notes.md"
  printf '\n### Container image\n\n'
  printf -- '- `%s:%s`\n' "$repo" "$version"
  printf -- '- Digest: `%s`\n' "$digest"
  if [ "$dry_run" = false ]; then
    printf -- '- Pin it: `docker pull %s@%s`\n' "$repo" "$digest"
  fi
  printf -- '- Architectures: %s\n' "${platforms[*]}"
  printf -- '- Each `.cdx.json` file is the CycloneDX SBOM of one architecture; `checksums.txt` lists the SHA-256 of the SBOMs and of these notes.\n'
} >"$out/release-notes.md"
rm -f "$out/notes.md"

(
  cd "$out"
  sha256 release-notes.md scanopsis-*.cdx.json >checksums.txt
)

echo
echo "image digest: $digest"
echo "artefacts in $out:"
ls -l "$out"
echo
cat "$out/checksums.txt"
