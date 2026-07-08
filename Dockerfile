# syntax=docker/dockerfile:1
# Pare is a single self-contained Go binary: templates and migrations are
# embedded via go:embed, so there is no frontend build stage.
FROM golang:1.26-alpine AS build
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
