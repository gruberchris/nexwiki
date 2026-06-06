# ==========================================
# Stage 1: Build the React + TS Frontend
# ==========================================
FROM node:20-alpine AS frontend-builder
WORKDIR /app/frontend

# Copy dependency manifests
COPY frontend/package*.json ./

# Install packages
RUN npm install

# Copy all source assets
COPY frontend/ ./

# Force stable Tailwind CSS v3 for smooth compilations
RUN npm install -D tailwindcss@3

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
