# Build frontend
FROM node:22-alpine AS frontend
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json* ./
RUN npm install
COPY frontend/ ./
RUN npm run build

# Build backend
FROM golang:1.24-bookworm AS backend
WORKDIR /src
COPY backend/go.mod backend/go.sum* ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -o /out/server ./cmd/server

# Runtime
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl restic \
  && rm -rf /var/lib/apt/lists/* \
  && useradd -r -u 65532 -d /home/nonroot nonroot
WORKDIR /app
COPY --from=backend /out/server /app/server
COPY --from=frontend /src/dist /app/static
RUN mkdir -p /data && chown -R nonroot:nonroot /data /app
USER nonroot
ENV STATIC_DIR=/app/static \
    AUDIT_DB_PATH=/data/audit.db \
    HTTP_ADDR=:8080
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/app/server"]
