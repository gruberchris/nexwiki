# ====================================================================================
# NexWiki Makefile
# Coordinator for frontend builds, local runs, Docker deployment, and cross-compilation.
# ====================================================================================

# Variables
VERSION ?= 0.1.0
LDFLAGS = -ldflags="-w -s -X main.Version=$(VERSION)"
BINARY_NAME=nexwiki
FRONTEND_DIR=frontend
DIST_DIR=frontend/dist
BUILD_DIR=bin

.PHONY: all build-frontend build-backend clean docker-build docker-up docker-down \
        build-windows-amd64 build-linux-amd64 build-linux-arm64 build-macos-arm64 build-all-platforms

# Default target: builds both frontend and host backend binary
all: build-frontend build-backend

# Ensure the output build directory exists
$(BUILD_DIR):
	mkdir -p $(BUILD_DIR)

# ------------------------------------------------------------------------------------
# 1. Frontend Asset compilation
# ------------------------------------------------------------------------------------
build-frontend:
	@echo "📦 Installing and compiling React frontend..."
	cd $(FRONTEND_DIR) && npm install && npm run build
	@echo "✅ Frontend compilation finished."

# ------------------------------------------------------------------------------------
# 2. Host Platform Compilation (requires frontend assets to exist for go:embed)
# ------------------------------------------------------------------------------------
build-backend: build-frontend
	@echo "🐹 Building Go backend for host platform..."
	go build $(LDFLAGS) -o $(BINARY_NAME) main.go
	@echo "✅ Host compilation complete: ./$(BINARY_NAME)"

# ------------------------------------------------------------------------------------
# 3. Docker Helpers
# ------------------------------------------------------------------------------------
docker-build:
	@echo "🐳 Building Docker image..."
	docker build --build-arg VERSION=$(VERSION) -t $(BINARY_NAME):$(VERSION) -t $(BINARY_NAME):latest .
	@echo "✅ Docker image built."

docker-up:
	@echo "🐳 Starting local container cluster..."
	docker compose up -d --build
	@echo "🚀 NexWiki running on http://localhost:8080"

docker-down:
	@echo "🐳 Stopping local container cluster..."
	docker compose down
	@echo "🛑 Container cluster stopped."

# ------------------------------------------------------------------------------------
# 4. Multi-Platform Cross-Compilation
# Note: All cross-compilation builds depend on 'build-frontend' since Go's embed
# package will throw compilation errors if 'frontend/dist' is empty or missing.
# ------------------------------------------------------------------------------------

build-windows-amd64: build-frontend | $(BUILD_DIR)
	@echo "🪟 Compiling for Windows AMD64..."
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe main.go
	@echo "💾 Windows binary generated at ./$(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe"

build-linux-amd64: build-frontend | $(BUILD_DIR)
	@echo "🐧 Compiling for Linux AMD64..."
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 main.go
	@echo "💾 Linux AMD64 binary generated at ./$(BUILD_DIR)/$(BINARY_NAME)-linux-amd64"

build-linux-arm64: build-frontend | $(BUILD_DIR)
	@echo "🐧 Compiling for Linux ARM64..."
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 main.go
	@echo "💾 Linux ARM64 binary generated at ./$(BUILD_DIR)/$(BINARY_NAME)-linux-arm64"

build-macos-arm64: build-frontend | $(BUILD_DIR)
	@echo "🍎 Compiling for macOS ARM64 (Apple Silicon)..."
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 main.go
	@echo "💾 macOS ARM64 binary generated at ./$(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64"

# Compile all platforms in a single command
build-all-platforms: build-windows-amd64 build-linux-amd64 build-linux-arm64 build-macos-arm64
	@echo "🎉 All target binaries successfully generated inside the ./$(BUILD_DIR)/ directory!"

# ------------------------------------------------------------------------------------
# 5. Cleanup
# ------------------------------------------------------------------------------------
clean:
	@echo "🧹 Cleaning up build artifacts..."
	rm -rf $(BINARY_NAME) $(BUILD_DIR) $(DIST_DIR)
	@echo "✨ Workspace clean."
