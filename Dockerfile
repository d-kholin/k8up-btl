# syntax=docker/dockerfile:1.7

# ---- frontend ----
FROM node:22-bookworm-slim AS frontend
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# ---- backend ----
FROM golang:1.24-bookworm AS backend
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN mkdir -p /out \
  && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# ---- restic (pinned) ----
FROM debian:bookworm-slim AS restic
ARG RESTIC_VERSION=0.18.0
# BuildKit injects TARGETARCH per platform. Do not default it here — a default
# of amd64 made arm64 builds download the wrong restic binary.
ARG TARGETARCH
RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates curl bzip2 \
  && rm -rf /var/lib/apt/lists/* \
  && arch="${TARGETARCH:?TARGETARCH not set}" \
  && case "$arch" in amd64|arm64) ;; *) echo "unsupported arch: $arch" >&2; exit 1 ;; esac \
  && mkdir -p /out \
  && curl -fsSL "https://github.com/restic/restic/releases/download/v${RESTIC_VERSION}/restic_${RESTIC_VERSION}_linux_${arch}.bz2" \
    | bunzip2 > /out/restic \
  && chmod 0755 /out/restic \
  && /out/restic version

# ---- runtime ----
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app

COPY --from=backend /out/server /app/server
COPY --from=frontend /src/dist /app/static
COPY --from=restic /out/restic /app/restic
# Distroless image already ships a CA bundle; restic inherits the process env.
# Prefer explicit path so cleaned subprocess envs still verify LE certs.
COPY --from=restic /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

ENV HTTP_ADDR=:8080 \
    STATIC_DIR=/app/static \
    AUDIT_DB_PATH=/data/audit.db \
    RESTIC_BINARY=/app/restic \
    SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt \
    PATH=/app

USER nonroot:nonroot
EXPOSE 8080
VOLUME ["/data"]

ENTRYPOINT ["/app/server"]
