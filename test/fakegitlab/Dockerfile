# Copyright 2026 The Scanopsis Authors
# SPDX-License-Identifier: Apache-2.0

# Fake GitLab OpenID Connect provider for end-to-end tests only.
# Build from the repository root:
#   docker build -f test/fakegitlab/Dockerfile -t scanopsis-fakegitlab:dev .

FROM golang:1.27.4-alpine3.24@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal/testutil/fakegitlab ./internal/testutil/fakegitlab
COPY test/fakegitlab ./test/fakegitlab
RUN CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o /out/fakegitlab ./test/fakegitlab

FROM gcr.io/distroless/static-debian13:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931
COPY --from=build /out/fakegitlab /fakegitlab
EXPOSE 9000
ENTRYPOINT ["/fakegitlab"]
