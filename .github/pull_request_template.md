## What & why

<!-- What does this change, and what problem does it solve? Link any issue. -->

## How to verify

<!-- Steps a reviewer can follow, or the commands you ran. -->

```bash
go test -race ./server/... .
cd frontend && npm test
```

## Checklist

- [ ] `gofmt -l main.go server/` is clean and `go vet ./server/... .` passes
- [ ] `go test -race ./server/... .` passes
- [ ] Frontend tests pass (`cd frontend && npm test`) if the UI changed
- [ ] New behavior has a test that fails without the change

### If this adds or changes a feature

Per the [Documentation Integrity Rule](../AGENTS.md#-project-documentation-guide):

- [ ] The feature is listed in [`README.md`](../README.md)
- [ ] A user guide exists in [`docs/`](../docs) and is linked from [`docs/README.md`](../docs/README.md)
- [ ] [`CHANGELOG.md`](../CHANGELOG.md) has an entry under `[Unreleased]`

### If this adds or removes an MCP tool

- [ ] Documented in [`docs/mcp_server.md`](../docs/mcp_server.md) — the canonical tool reference
- [ ] The tool **count** is updated everywhere it appears: `README.md`, `AGENTS.md`, `docs/README.md`, `docs/second_brain_workflow_guide.md`, and the `expectedToolCount` constant in `server/mcp_tools_test.go`
