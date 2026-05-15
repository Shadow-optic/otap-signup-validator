# Multi-stage build for FLR

# === Build stage ===
FROM golang:1.23-alpine AS builder

WORKDIR /build

# Install build dependencies
RUN apk add --no-cache git make protobuf protobuf-dev

# Copy module files
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build binary
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-s -w -extldflags '-static'" \
    -o /build/bin/flr \
    ./cmd/flr

# === Runtime stage ===
FROM alpine:3.21

# Install CA certificates for TLS
RUN apk add --no-cache ca-certificates

# Create non-root user
RUN adduser -D -u 1000 flr

# Copy binary
COPY --from=builder /build/bin/flr /usr/local/bin/flr

# Create data directory
RUN mkdir -p /data && chown flr:flr /data

# Switch to non-root user
USER flr

# Default data directory
ENV FLR_DB_PATH=/data/registry
ENV FLR_KEYS_PATH=/data/keys

# Expose ports
EXPOSE 8080 9090

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD flr status || exit 1

ENTRYPOINT ["flr"]
CMD ["serve", "--config", "/data/config.yaml"]
