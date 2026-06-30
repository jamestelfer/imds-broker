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
| Dependency / toolchain management | mise (`mise.toml`) |
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

## Threat Model

### Default deployment model
- Treat the sandboxed AI agent as the adversary.
- Assume `imds-broker mcp` runs outside the sandbox as a host-side process launched by the operator's MCP client.
- Assume the primary target sandbox is Anthropic's Claude Code sandbox.
- Assume Claude Code uses a real sandbox boundary: Anthropic `sandbox-runtime`, Docker, or a VM.
- Assume the sandbox grants the agent access to the project workspace, not to host credential or broker configuration directories.
- Assume the sandboxed agent cannot read `~/.aws`, AWS SSO caches, exported AWS credential environment variables, or paths named by `AWS_CONFIG_FILE` and `AWS_SHARED_CREDENTIALS_FILE`.
- Assume the sandboxed agent cannot read or modify `${XDG_CONFIG_HOME:-$HOME/.config}/imds-broker/config.yaml` unless the design explicitly exposes it.
- Assume the sandboxed agent cannot influence broker command arguments, broker environment variables, or `PATH` resolution for the host-side broker.
- Treat `imds-broker` as the only approved path by which the sandboxed agent can obtain AWS credentials.

### Security review rules
- Analyse whether a change lets the sandboxed agent bypass the broker and read credentials directly.
- Analyse whether a change lets the sandboxed agent influence host-controlled broker startup inputs or configuration.
- Analyse whether a change depends on Claude Code permission-bypass modes such as `--dangerously-skip-permissions`, `--allow-dangerously-skip-permissions`, or `--permission-mode bypassPermissions`.
- Analyse whether a change depends on adding `$HOME`, `~/.aws`, `~/.config`, `~/.claude`, the broker config directory, or other host credential/config paths to Claude Code, Docker, VM, or sandbox-runtime filesystem allow-lists.
- Analyse whether a change lets the sandboxed agent retain credential access beyond the intended session lifetime.
- Treat host-local compromise as out of scope unless the design explicitly claims resistance to it.
- If a finding depends on the attacker controlling host dotfiles, host process launch context, or host environment variables, mark it out of scope by default.
- If a design relies on a host-controlled file or path, verify that the sandbox boundary keeps that file out of reach of the agent.
- If a design relies on the sandbox for protection, state that dependency explicitly in the PRD, plan, docs, and code comments where relevant.

### Review guidance for broker configuration
- Treat broker configuration as host-controlled defaults, not a secret and not a security boundary by itself.
- Focus on whether the agent can modify configuration, broker launch arguments, environment variables, MCP client configuration, or AWS credential paths.
- Do not treat mere knowledge of the configured profile filter as a security issue.
- Treat command-line and environment overrides as host-controlled MCP launch inputs. If the agent controls them, the profile filter is advisory only.
- Do not over-focus on MCP configuration mechanics. Claude starts MCP servers in the host context; the core defence is the OS/container/VM sandbox around the agent tools.

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

### Change discipline
- Match the size of a change to its value. Refactoring for clarity, consistency, or to remove duplication is worthwhile; churn without a benefit is not. Neither gold-plate nor under-fix.
- Avoid speculative optimisation. Do not optimise without evidence the path is hot, and state the expected data scale in the justification.
- If a change would duplicate logic or add cross-package coupling, stop and extract a shared abstraction instead.
- Treat review comments as input, not a checklist. For each, weigh benefit against cost and data scale, then act or skip with a brief reason. Reject low-value changes explicitly.

### Verification

Run `just verify` before every commit. It runs fmt, build, lint (golangci-lint), and tests in sequence. Do not use `go test ./...` or `go build ./...` as a substitute — `just verify` is the canonical check.

Other useful targets:
- `just test-v` — verbose test output
- `just lint` — lint only
- `just build` — build only

### Commit messages
- Use [Conventional Commits](https://www.conventionalcommits.org/): `type(scope): description`
- Common types: `feat`, `fix`, `ci`, `docs`, `refactor`, `test`, `chore`
- Scope is optional but use it when helpful (e.g. `feat(imdsserver): ...`)

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
| justinas/alice | `/justinas/alice` |
| gopkg.in/ini.v1 | `/go-ini/ini` |

---

## Voice

Apply these rules to all documentation (README, plans, PRD), code comments, commit messages, and PR descriptions.

- **Register**: Technical-professional. Use terms directly without over-explanation.
- **Perspective in guides**: Second person ("you") and imperative mood. Start instructions with the action verb.
- **Perspective in reference**: Third person and declarative mood. Describe what things are and do.
- **No first person**: Never use "I" or "we" in any content type.
- **Directness**: State facts plainly. Make confident recommendations. State limitations without apologising.
- **No filler**: Never use "Let's dive in", "It's important to note", "As mentioned earlier", or similar padding.
- **No hedging**: Never use "You might want to consider", "It's generally a good idea", or similar softening.
- **No rhetorical questions**: State information directly instead of asking "But what does this mean?"
- **No apologetic language**: Never use "This might seem complicated, but..." or "Don't worry..."
- **Sentences**: Aim for under 20 words. Single-sentence paragraphs are acceptable.
- **Spelling**: Use British/Australian English spellings (e.g., "favour", "organise", "colour", "licence" as noun).
