# syntax=docker/dockerfile:1

# Build stage
FROM golang:1.26.6-alpine3.24 AS build

WORKDIR /src

# Copy module files first and download dependencies as a cached layer.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# CGO_ENABLED=0 produces statically linked binaries with no libc dependency.
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux \
    go build \
        -trimpath \
        -ldflags="-s -w -X main.version=${VERSION}" \
        -o /out/ledgerd ./cmd/ledgerd && \
    CGO_ENABLED=0 GOOS=linux \
    go build \
        -trimpath \
        -ldflags="-s -w -X main.version=${VERSION}" \
        -o /out/ledgerctl ./cmd/ledgerctl

# Development stage: hot reload and a debugger
FROM golang:1.26.6-alpine3.24 AS dev

# git is needed for VCS stamping during go install; curl is for health checks.
RUN apk add --no-cache git curl

RUN go install github.com/air-verse/air@v1.67.4 && \
    go install github.com/go-delve/delve/cmd/dlv@v1.27.1

WORKDIR /src

EXPOSE 8080 9090 2345
CMD ["air", "-c", ".air.toml"]

# Runtime stage
FROM alpine:3.24.1 AS runtime

# ca-certificates for outbound TLS, tzdata for proper timezone parsing.
RUN apk add --no-cache ca-certificates tzdata

# Dedicated unprivileged user to avoid running as root.
RUN addgroup -g 10001 -S ledger && \
    adduser  -u 10001 -S ledger -G ledger

COPY --from=build /out/ledgerd   /usr/local/bin/ledgerd
COPY --from=build /out/ledgerctl /usr/local/bin/ledgerctl

USER ledger:ledger

EXPOSE 8080 9090

# Exec form ensures the binary is PID 1 and receives SIGTERM directly for graceful shutdown.
ENTRYPOINT ["/usr/local/bin/ledgerd"]

