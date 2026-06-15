# Progress: Protected Configuration File

> Plan: `plans/protected-config-plan.md` · PRD: `plans/protected-config.md`

Mark a phase complete only after its acceptance criteria are reviewed and verified and `just verify` passes. Record durable decisions and problem fixes in Lessons learned.

## Phases

- [x] **P0 — Baseline**: `just verify` green before Phase 1.
- [x] **Phase 1 — Config loader + fixed path, `profiles` honours the protected filter** (R1–R8, R12)
- [ ] **Phase 2 — Filter intersection and no-widening** (R9–R11)
- [ ] **Phase 3 — Apply the enforced filter to MCP and `serve`** (R13–R15)
- [ ] **Phase 4 — Overridable defaults via `cli-altsrc`** (R16–R19)

## Lessons learned

Concise, agent-facing notes only: decisions made and solutions to problems that a later phase needs to succeed. Omit anything not required for subsequent work.

- P0: the resumed-container `just build failed` was an empty Go module cache, not a code defect. `just build` triggers the dependency download; `just verify` is green afterwards. No source change needed.
- Phase 1: loader lives in `pkg/brokerconfig` (`ResolvePath()`, `Load(path)`); `cmd` wraps both in `loadBrokerConfig()`. Reuse these in `mcp`/`serve` (Phase 3). The `profiles` listing logic is extracted as `runProfiles(ctx, cfg, suppliedFilter, w)` — Phase 2 replaces its `if cfg.ProfileFilter != ""` selection with the intersection predicate.
- Phase 1: `os.ReadFile` of the fixed path needs `//nolint:gosec` (gosec G304 flags the variable path); the fixed-path read is the security model, not user-controlled inclusion.
