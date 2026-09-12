# NexWiki Developer Setup & Build Automation Guide 🛠️

This guide provides everything needed to contribute to NexWiki, run local development environments with hot-reloading, execute automated tests, and cross-compile standalone production binaries using the project `Makefile`.

---

## 🏗️ Architecture & Technology Stack

NexWiki is engineered as a zero-dependency, self-contained single binary containing both the Go backend server and the embedded React frontend:

* **Backend Engine**:
  * Written in **Go 1.27+** using standard library routing (`net/http` method matching introduced in Go 1.22+).
  * Full-text search powered by [Bleve](https://github.com/blevesearch/bleve) with disk-backed indexing.
  * Flat-file Open Knowledge Format (OKF v0.2) markdown storage with gzip-compressed revision histories.
  * Real-time Server-Sent Events (SSE) bus and dual-era Model Context Protocol (MCP) server supporting Stdio and Streamable HTTP.
  * Statically compiled with `CGO_ENABLED=0` for pure portability across any Linux, macOS, or Windows environment.
* **Frontend Web Application**:
  * Built with **React 19**, **TypeScript**, and **Tailwind CSS v4**.
  * Bundled using **Vite 8**.
  * Specialized components: CodeMirror 6 markdown editor with syntax linter, Mermaid SVG diagram renderer, and Lucide React icons.
  * Unit and component tests powered by **Vitest** and **Testing Library**.
* **Embedded Binary Assets**:
  * Go's `//go:embed frontend/dist/*` directive bundles the compiled frontend assets directly into the binary at compile time.
  * In production, the executable serves all HTML, JavaScript, CSS, and SVG assets without external dependencies or directory requirements.

---

## 📋 Prerequisites

To build and develop NexWiki locally, ensure you have the following installed:

1. **Go**: Version `1.27` or higher (`go version`)
2. **Node.js**: Version `20.x` LTS or higher with `npm` (`node -v`, `npm -v`)
3. **Make**: Standard GNU Make utility
4. **Docker & Docker Compose** *(optional)*: For container-based workflows and multi-platform testing

---

## ⚡ Quickstart: Building from Source

### 1. Clone the Repository
```bash
git clone https://github.com/gruberchris/nexwiki.git
cd nexwiki
```

### 2. Compile Everything with `make`
The root `Makefile` orchestrates frontend asset generation and Go compilation:
```bash
make
```
This performs two operations:
1. Executes `npm install && npm run build` inside `frontend/` to generate `frontend/dist/`.
2. Compiles `main.go` into `./nexwiki` with version stamping and stripped symbols (`-w -s`).

### 3. Run the Compiled Server
```bash
./nexwiki -port=5808 -data=./data -name="NexWiki Local Dev"
```
Navigate your browser to `http://localhost:5808`.

---

## 🔄 Frictionless Frontend Development (Hot-Reloading)

When modifying the React frontend, rebuilding the entire Go binary for each change is slow and unnecessary. Instead, utilize NexWiki's live development mode with Vite hot module replacement (HMR):

```
┌──────────────────────────────────────┐       ┌──────────────────────────────────────┐
│       Terminal 1: Vite Dev           │       │       Terminal 2: Go Backend         │
│       cd frontend && npm run dev     │       │       go run main.go                 │
│       Listens on: http://localhost:5173  │       │       Listens on: http://localhost:5808  │
└──────────────────┬───────────────────┘       └──────────────────┬───────────────────┘
                   │                                              │
                   │ (Browser UI with Hot-Reloading)             │ (REST & MCP API Endpoints)
                   └───────────────────────┬──────────────────────┘
                                           ▼
                               User Browser on :5173
```

### Step-by-Step Instructions:

1. **Terminal 1: Start the Vite Dev Server**
   ```bash
   cd frontend
   npm install
   npm run dev
   ```
   Vite starts the frontend with HMR at `http://localhost:5173`.

2. **Terminal 2: Start the Go Backend Server**
   ```bash
   go run main.go -port=5808 -data=./data
   ```
   The Go backend launches on port `5808`.

3. **Open Browser to `http://localhost:5173`**
   * Edit any `.tsx`, `.ts`, or `.css` file in `frontend/src/` — changes reflect instantly in your browser.
   * **CORS Middleware Support**: NexWiki's backend includes built-in CORS middleware that automatically permits requests originating from `http://localhost:5173`.
   * **Live Disk Serving**: If `frontend/dist/` exists on disk, the Go backend dynamically serves assets directly from disk instead of embedded memory, allowing you to test production builds without recompiling Go.

---

## 🧰 Makefile Automation Reference

The repository provides a complete `Makefile` to coordinate builds, testing, and container tasks:

| Command | Action | Description |
|---|---|---|
| `make` / `make all` | Build All | Compiles the frontend assets and builds the host backend binary (`./nexwiki`). |
| `make build-frontend` | Compile Frontend | Installs npm dependencies and runs `npm run build` to generate `frontend/dist/`. |
| `make build-backend` | Compile Backend | Ensures frontend assets are built, then compiles `./nexwiki` for the host platform. |
| `make clean` | Clean Artifacts | Deletes the compiled binary `./nexwiki`, the `bin/` directory, and `frontend/dist/`. |
| `make docker-build` | Build Container | Builds a local Docker image tagged with `VERSION` and `latest`. |
| `make docker-up` | Run Docker Stack | Launches the local Docker Compose stack in the background with auto-rebuilding. |
| `make docker-down` | Stop Docker Stack | Shuts down the local Docker Compose container cluster. |

---

## 🌍 Multi-Platform Cross-Compilation Matrix

NexWiki is designed to cross-compile effortlessly because it does not require CGO (`CGO_ENABLED=0`). All cross-compiled binaries are placed in the `./bin/` directory.

> 💡 **Embedded Assets Requirement**: All cross-compilation targets automatically trigger `make build-frontend` first. If `frontend/dist/` were missing or empty, Go's `//go:embed` compiler directive would fail.

### Target Commands:

* **Windows (AMD64 / x86_64)**:
  ```bash
  make build-windows-amd64
  # Output: ./bin/nexwiki-windows-amd64.exe
  ```

* **Linux (AMD64 / x86_64)**:
  ```bash
  make build-linux-amd64
  # Output: ./bin/nexwiki-linux-amd64
  ```

* **Linux (ARM64 / AArch64)**:
  ```bash
  make build-linux-arm64
  # Output: ./bin/nexwiki-linux-arm64
  ```

* **macOS (ARM64 / Apple Silicon M1/M2/M3/M4)**:
  ```bash
  make build-macos-arm64
  # Output: ./bin/nexwiki-darwin-arm64
  ```

* **Compile All Platforms Simultaneously**:
  ```bash
  make build-all-platforms
  ```
  Generates all 4 production binaries inside `./bin/` with a single command.

---

## 🧪 Testing Suites & Validation

NexWiki enforces strict quality gates covering formatting, static analysis, race detection, and component rendering.

### 1. Backend Go Tests
```bash
# Run standard test suite
go test -v ./server/... .

# Run with race detector enabled (CRITICAL: mirrors CI test gate)
go test -race -v ./server/... .

# Run Go static analysis
go vet ./server/... .

# Verify formatting complies with gofmt (must return no output)
gofmt -l main.go server/
```

### 2. Frontend Vitest & React Tests
```bash
cd frontend

# Run complete Vitest suite once
npm test

# Run Vitest in interactive watch mode during development
npm run test:watch

# Run ESLint validation
npm run lint

# Verify TypeScript types
npx tsc -b
```

### 3. Local CI Pre-Flight Check
To run the exact validation commands executed by the GitHub Actions CI pipeline before pushing code or creating a PR:
```bash
# 1. Format check (must output nothing)
gofmt -l main.go server/

# 2. Go vet
go vet ./server/... .

# 3. Race detector test suite
go test -race ./server/... .

# 4. Frontend tests and production build
(cd frontend && npm ci && npm run build && npm test)

# 5. Verify static binary compiles with CGO disabled
CGO_ENABLED=0 go build -ldflags="-w -s" -o /tmp/nexwiki main.go
```
