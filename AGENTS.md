# IMDS Broker — Agent Instructions

## Project Overview

IMDS Broker is a Go service that vends AWS credentials via the EC2 Instance Metadata Service (IMDSv2) protocol. It serves developers and CI pipelines that run tooling expecting to be on EC2. It operates in three modes: MCP stdio server, standalone `serve` command, and `profiles` listing command.

Source PRD: `plans/prd.md`  
Implementation plan: `plans/imds-broker.md`

---

## Tech Stack

| Concern | Decision |
|---|---|
| Language | Go 1.26 |
| Dependency / toolchain management | mise (`.tool-versions`) |
| Command runner | just (`justfile`) |
| CLI framework | urfave/cli v3 |
| HTTP middleware | justinas/alice |
| HTTP server/mux | stdlib `net/http` — stock `http.Server` and `http.ServeMux` |
| Logging | `slog` with JSON handler |
| MCP protocol | mark3labs/mcp-go (stdio transport only) |
| AWS credentials | aws/aws-sdk-go-v2 |
| Testing | stretchr/testify (`assert` + `require`) |
| Linting | golangci-lint |
| CI | GitHub Actions |

---

## Architecture

```
cmd/imds-broker/     — CLI entry point, wiring, signal handling
pkg/imdsserver/      — IMDSv2 HTTP handler, token, credential logic
pkg/broker/          — Multi-server lifecycle manager
pkg/profiles/        — AWS profile reader and regex filter
pkg/mcpserver/       — MCP tool adapter (thin, no business logic)
```

All packages live under `pkg/` for importability. `cmd/` contains only CLI wiring.

---

## Implementation Principles

### Context propagation
- Pass `context.Context` as the first argument throughout.
- Attach `*slog.Logger` to request contexts via middleware, carrying a per-request ID. Use this logger for all request-scoped logging.
- MCP handlers get an equivalent context-bound logger with a per-call ID.

### Logging
- Single `slog.Logger` with JSON handler, constructed in `cmd/`.
- A common `--log-level` flag (urfave/cli) sets the slog level at startup. Use urfave/cli's documented approach for setting slog level from a flag.
- Request middleware (Alice chain) injects a child logger with `request_id` into the context.

### Middleware
- Use **justinas/alice** for all middleware chains — no manual chaining.

### HTTP
- Use Go 1.22+ mux patterns: `mux.HandleFunc("PUT /latest/api/token", ...)`.
- No third-party routers.

### Shutdown
- On SIGINT/SIGTERM (or MCP stdio EOF): cancel the root context, then stop all IMDS servers.
- **Hard stop**: cancel context immediately, use a very short deadline (~1–2s). Do not wait for connections to drain gracefully.

### Code style
- Modern Go only — no backwards-compatibility shims.
- Use generics where they reduce duplication and improve clarity.

---

## Context7 Library IDs

Use these when fetching documentation via the context7 MCP tool:

| Library | Context7 ID |
|---|---|
| urfave/cli | `/urfave/cli` |
| AWS SDK Go v2 | `/aws/aws-sdk-go-v2` |
| mark3labs/mcp-go | `/mark3labs/mcp-go` |
| golangci-lint | `/golangci/golangci-lint` |
| just (command runner) | `/casey/just` |
| stretchr/testify | `/stretchr/testify` |

> Note: justinas/alice is not indexed in context7. Refer to https://github.com/justinas/alice for usage.
