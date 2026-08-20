# syntax=docker/dockerfile:1
# Pare is a single self-contained Go binary: templates and migrations are
# embedded via go:embed, so there is no frontend build stage.
# Full X.Y.Z patch pin, in lockstep with ciGoToolchain in
# hephaestus/userworkflows/ci_go.go. This read `golang:1.26-alpine` until
# 2026-08-20: a floating minor resolves to whatever the newest 1.26.x is on the
# day the layer cache misses, so what stdlib pare shipped was decided by build
# timing rather than by a commit, and the estate toolchain sweep could not see
# it (the sweep matched `golang:1.26.5-`, which this never said).
FROM golang:1.26.6-alpine AS build
WORKDIR /src
ARG PARE_BUILD_TAGS=""
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -tags "$PARE_BUILD_TAGS" -o /pare ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 1000 pare
COPY --from=build /pare /usr/local/bin/pare
USER pare
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/pare"]
