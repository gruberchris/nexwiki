# ==========================================
# Stage 1: Build the React + TS Frontend
# ==========================================
FROM node:20-alpine AS frontend-builder
WORKDIR /app/frontend

# Copy dependency manifests
COPY frontend/package*.json ./

# npm ci, not npm install: it installs exactly what package-lock.json pins and fails if the two
# have drifted, so the image is built from the same dependency tree that was tested.
RUN npm ci

# Copy all source assets. .dockerignore keeps the host's node_modules out — without it this COPY
# lands platform-specific binaries (darwin-arm64 rollup/esbuild) on top of the linux ones just
# installed above.
COPY frontend/ ./

# NOTE: there was a `RUN npm install -D tailwindcss@3` here, pinning Tailwind v3 regardless of what
# package.json asked for. It silently overrode the manifest, so after the v4 migration the image
# installed v4 and then downgraded to v3, and the build died on `@import "tailwindcss"` —
# postcss-import tried to read tailwindcss/lib/index.js as a stylesheet and choked on "use strict".
# The version belongs in package.json alone; do not reintroduce a pin here.

# Compile production package (outputs to /app/frontend/dist)
RUN npm run build

# ==========================================
# Stage 2: Build the Go Web Server
# ==========================================
FROM golang:1.26-alpine AS backend-builder
WORKDIR /app

# Initialize Go environment
COPY go.mod go.sum ./

# Copy built frontend assets from Stage 1 into position for embedding
COPY --from=frontend-builder /app/frontend/dist ./frontend/dist

# Copy remaining Go server files
COPY main.go ./
COPY server/ ./server/

ARG VERSION=0.1.0

# Compile static self-contained binary, stripping debugging symbols (-w -s) for tiny footprint
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s -X main.Version=${VERSION}" -o nexwiki main.go

# ==========================================
# Stage 3: Minimal and Secure Production Runner
# ==========================================
FROM alpine:3.19
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# Copy single compiled static binary from Stage 2
COPY --from=backend-builder /app/nexwiki .

# Pre-create data directory for volume mapping
RUN mkdir -p /app/data

# Default port configuration
EXPOSE 8080

# Persistent storage mount point for articles and uploaded assets
VOLUME ["/app/data"]

# Run the single binary, directing persistence to the mounted volume
ENTRYPOINT ["/app/nexwiki", "-port=8080", "-data=/app/data"]
