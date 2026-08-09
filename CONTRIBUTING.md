# Contributing to NexWiki

First off, thank you for considering contributing to NexWiki! It's people like you who make NexWiki a great tool.

## Table of Contents

- [Code of Conduct](#code-of-conduct)
- [How Can I Contribute?](#how-can-i-contribute)
  - [Reporting Bugs](#reporting-bugs)
  - [Suggesting Enhancements](#suggesting-enhancements)
  - [Pull Requests](#pull-requests)
- [Development Setup](#development-setup)
- [Coding Standards](#coding-standards)
- [Documentation Rules](#documentation-rules)
- [Commit Messages](#commit-messages)
- [Running Tests](#running-tests)

## Code of Conduct

This project and everyone participating in it is governed by our community standards. Please be respectful, constructive, and inclusive in all interactions.

## How Can I Contribute?

### Reporting Bugs

Before submitting a bug report, please check existing issues to avoid duplicates.

1. Navigate to the [Issues](https://github.com/gruberchris/nexwiki/issues) page
2. Click **New Issue** and select the **Bug Report** template
3. Fill in the template with as much detail as possible, including:
   - Steps to reproduce
   - Expected vs actual behavior
   - Your environment (OS, Go version, Node version, Docker version)
   - Relevant logs or screenshots

### Suggesting Enhancements

Feature requests help shape the future of NexWiki.

1. Navigate to the [Issues](https://github.com/gruberchris/nexwiki/issues) page
2. Click **New Issue** and select the **Feature Request** template
3. Describe the problem you're solving, your proposed solution, and any alternatives you've considered

### Pull Requests

1. Fork the repository
2. Create a feature branch from `main`:
   ```bash
   git checkout -b feature/your-feature-name
   ```
3. Make your changes following the coding standards below
4. Run the tests to ensure nothing is broken
5. Push your branch and open a Pull Request against `main`
6. Fill in the PR description with a clear summary of your changes

## Development Setup

### Prerequisites

- **Go**: 1.26 or later
- **Node.js**: 20.x or later (includes `npm`)
- **Docker**: Optional, for containerized development

### Quick Start

```bash
# Clone your fork
git clone https://github.com/YOUR_USERNAME/nexwiki.git
cd nexwiki

# Build everything (frontend + backend)
make

# Run the server
./nexwiki -port=8080 -data=./data -name="NexWiki Development"
```

### Frontend Hot-Reloading Mode

For active frontend development with hot-reloading:

```bash
# Terminal 1: Vite dev server
cd frontend
npm run dev

# Terminal 2: Go backend
go run main.go -port=8080 -data=./data
```

### Docker Development

```bash
# Build and run with Docker Compose
make docker-up

# Stop containers
make docker-down
```

## Coding Standards

### Go Backend

- Follow [Effective Go](https://go.dev/doc/effective_go) guidelines
- Use `gofmt` for consistent formatting
- Exported functions and types must have documentation comments
- Keep functions focused and under 50 lines when possible
- All custom environment variables must be prefixed with `NEXWIKI_`

### React Frontend

- Write TypeScript with strict mode enabled
- Use functional components with hooks
- Follow the existing component structure in `frontend/src/`
- Use Tailwind CSS (v3) for styling
- Keep components small and single-purpose

### General

- Do not add unnecessary comments — let code be self-documenting
- Follow existing naming conventions in the codebase
- Preserve Markdown formatting consistency in documentation files

## Documentation Rules

These are the two conventions a new contributor is most likely to miss, and both are enforced in review.

### Where documentation lives

| File | Purpose |
|---|---|
| [`README.md`](./README.md) | The pitch, quick start, and a one-line entry per feature |
| [`docs/`](./docs) | One in-depth guide per feature area; every guide is linked from [`docs/README.md`](./docs/README.md) |
| [`docs/mcp_server.md`](./docs/mcp_server.md) | **The** canonical MCP tool reference — never restate the full tool list anywhere else |
| [`AGENTS.md`](./AGENTS.md) | MCP architecture, protocol eras, prompts, and client configuration |
| [`CHANGELOG.md`](./CHANGELOG.md) | User-visible changes, added under `[Unreleased]` |

### The Documentation Integrity Rule

When you add a feature ([full rule in `AGENTS.md`](./AGENTS.md#-project-documentation-guide)):

1. Add it to the feature list in `README.md`.
2. Write a user guide in `docs/` that teaches the feature with practical examples.
3. Link that guide from `docs/README.md`.
4. Add a `CHANGELOG.md` entry under `[Unreleased]`.

**If you add or remove an MCP tool**, document it in `docs/mcp_server.md` and update the tool count in *every* place it is stated — `README.md`, `AGENTS.md`, `docs/README.md`, `docs/second_brain_workflow_guide.md`, and the `expectedToolCount` constant in `server/mcp_tools_test.go`. That constant makes CI fail with the list of files to update, so a stale count cannot merge unnoticed.

## Commit Messages

Use clear, descriptive commit messages:

- **Format**: `<type>: <short description>`
- **Types**: `feat`, `fix`, `docs`, `style`, `refactor`, `test`, `chore`
- **Examples**:
  - `feat: add markdown linter diagnostics panel`
  - `fix: resolve SSE connection drop on refresh`
  - `docs: update MCP server connection guide`
  - `refactor: extract tag cloud into separate component`
  - `test: add unit tests for slugify utility`

## Running Tests

### Go Backend Tests

```bash
# Run all tests
go test ./...

# Run with verbose output
go test -v ./...

# Run with coverage
go test -cover ./...
```

### Frontend Tests

```bash
cd frontend
npm test
```

### CI Pipeline

The GitHub Actions CI pipeline runs on every push to `main` and on all pull requests. It builds the frontend, runs Go tests, and verifies the binary compiles. You can view the pipeline status in the **Actions** tab of the repository.

---

Thank you for contributing to NexWiki! 🚀
