# Copyright 2026 The Scanopsis Authors
# SPDX-License-Identifier: Apache-2.0

# Web build. The output is architecture-independent, so it always runs on the
# build host's platform instead of being emulated for each target.
FROM --platform=$BUILDPLATFORM node:26.13.1-alpine3.24@sha256:6bd9ab43c0ec0190ca7b2d193906cb8b7edf6e7f4afa43ddeccd6452710bffb5 AS web
WORKDIR /src/web
# Manifests first, so the dependency layer is reused until they change.
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Go build. Runs on the build host and cross-compiles, which avoids emulation.
FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine3.24@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
WORKDIR /src
# Copying the module files first keeps the download layer cached across
# source changes.
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .
# The embedded web interface must be in place before the binary is compiled.
COPY --from=web /src/web/dist/app web/dist/app
# Static, stripped binary with the version stamped in. VERSION and COMMIT are
# restricted to a safe character set so they cannot add extra linker flags.
RUN case "${VERSION}${COMMIT}" in *[!A-Za-z0-9._+-]*) \
      echo "VERSION and COMMIT may only contain A-Z a-z 0-9 . _ + -" >&2; exit 1;; esac; \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o /out/scanopsis ./cmd/scanopsis

# Runtime: no shell or package manager, only the static binary.
FROM gcr.io/distroless/static-debian13:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931
ARG VERSION=dev
ARG COMMIT=unknown
LABEL org.opencontainers.image.title="scanopsis" \
      org.opencontainers.image.description="Self-hosted dashboard for SonarQube and Trivy results from GitLab CI/CD pipelines." \
      org.opencontainers.image.source="https://github.com/siyamsarker/scanopsis" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}"
COPY --from=build /out/scanopsis /scanopsis
# Numeric nonroot user, so container runtimes can verify it is not root.
USER 65532:65532
# Unprivileged port; the binary writes nothing, so a read-only root filesystem works.
EXPOSE 8080
ENTRYPOINT ["/scanopsis"]
