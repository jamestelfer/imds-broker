# Plan: Protected Configuration File

> Source PRD: `plans/protected-config.md`

## Architectural decisions

Durable decisions that apply across all phases:

- **Config path**: `${XDG_CONFIG_HOME:-$HOME/.config}/imds-broker/config.yaml`. Fixed. Resolved by a helper that mirrors `resolveLogDir` (manual XDG resolution, not `os.UserConfigDir`, to keep `~/.config` semantics on macOS). No flag or environment override is wired.
- **Format**: YAML. Recognised keys: `profile-filter`, `region`, `log-level`. Unknown keys ignored.
- **Loader package**: a small dedicated package under `pkg/` (working name `pkg/brokerconfig`) exposing the resolved path and a `Load(path)` that returns a typed struct. Missing file → zero value, no error. Present-but-unreadable/unparseable → error (caller exits non-zero).
- **Filter enforcement**: a profile is permitted iff it matches **both** the protected filter and any supplied (flag/env) filter. Implemented as a logical AND of two `regexp` predicates — never a constructed combined regex. The composed predicate is the single enforcement point reused by `profiles`, `mcp`, and `serve`.
- **Defaults mechanism**: `region` and `log-level` defaults are supplied through `github.com/urfave/cli-altsrc/v3`'s YAML source pointed at the fixed path. The enforced `profile-filter` is deliberately **not** wired through `cli-altsrc` (an altsrc-backed flag would be overridable on the command line).
- **Load timing**: configuration is loaded once at command startup. No hot-reload.
- **Trust basis**: the fixed path only. No ownership/permission/signature checks on the file.

## Normalization notes

PRD requirements were already in EARS form; IDs assigned for traceability.

**Configuration source**
- `R1`: The broker shall resolve its config path as `${XDG_CONFIG_HOME:-$HOME/.config}/imds-broker/config.yaml`.
- `R2`: The config path shall not be overridable by any flag or environment variable.
- `R3`: The config file shall be YAML.
- `R4`: The config file shall support keys `profile-filter`, `region`, `log-level`; unknown keys ignored.
- `R5`: When a command starts, the broker shall load the config file before evaluating flags and environment variables.
- `R6`: Where the config file is absent, the broker shall apply built-in defaults.
- `R7`: If the config file is present but cannot be read or parsed, then the command shall exit non-zero without starting a server, listing profiles, or serving a tool (fail closed).

**Enforced profile filter**
- `R8`: When the config file specifies `profile-filter`, the broker shall treat it as the authoritative allow-list.
- `R9`: If a supplied filter accompanies the protected filter, then the effective allow-set shall be their intersection.
- `R10`: A supplied filter shall never widen the protected filter.
- `R11`: Where the config omits `profile-filter`, the supplied filter shall apply alone, falling back to `profiles.DefaultFilter`.
- `R12`: The `profiles` command output shall be restricted to the effective allow-set.
- `R13`: The MCP `list_profiles` result shall be restricted to the effective allow-set.
- `R14`: When `create_server` is called for a profile outside the allow-set, the broker shall reject it without starting a server.
- `R15`: When `serve` is invoked with a `--profile` the protected filter does not permit, the command shall refuse to start and exit non-zero.

**Default settings precedence**
- `R16`: The effective log level shall follow precedence: flag > environment > config file > built-in default.
- `R17`: The effective `serve` region shall follow precedence: flag > config file > profile-configured region.
- `R18`: Where the config provides `region`/`log-level` and the flag is unset, the broker shall use the file value.
- `R19`: If the config provides an invalid `log-level`, then the command shall fail closed.

## P0 baseline and standard quality gate

Project standard command: **`just verify`** (runs fmt, build, golangci-lint, and tests in sequence — the canonical check per `CLAUDE.md`). Do not substitute `go test ./...` or `go build ./...`.

- [ ] **P0 stabilization (required first)**: the session-resume hook reported `just build failed`. Investigate and restore a green `just verify` before any Phase 1 work. The only commit on this branch so far is the PRD doc, so the failure is presumed pre-existing or environmental (missing toolchain in the resumed container). Confirm which, fix or restore the toolchain, and record the cause.
- [ ] Run `just verify` as the P0 baseline; it must pass before Phase 1.
- [ ] If P0 fails, complete stabilization before starting planned phases.
- [ ] Re-run `just verify` before marking each phase complete; it must pass.
- [ ] Commit per phase using Conventional Commits (`feat`/`refactor`/`test`/`docs`).

---

## Phase 1: Config loader + fixed path, `profiles` honours the protected filter

**EARS requirements**: R1, R2, R3, R4, R5, R6, R7, R8, R12

### Why this phase exists

Establishes the tracer bullet: a real file at the fixed path changes observable behaviour. The protected `profile-filter` becomes the authoritative allow-list for the `profiles` command, and the loader's failure modes (absent → defaults, malformed → fail closed) are nailed down. Everything later reuses this loader and enforcement point.

### Locked decisions (non-negotiable)

- Path resolution and format exactly as in Architectural decisions. No path override.
- Missing file → built-in defaults (`profiles.DefaultFilter`), no error.
- Present-but-unparseable/unreadable file → non-zero exit, nothing printed to stdout.
- Config is loaded before flags/env are evaluated.
- When `profile-filter` is set in the file, `profiles` output is restricted to it.

### Flex zone (implementation choice allowed)

- Loader package name and internal struct shape.
- How the `profiles` command threads the config filter into `profiles.List` (e.g. pass config regex as the `List` filter, or a post-filter pass).
- Interaction between a CLI `--profile-filter` and the config filter is **finalized in Phase 2**; Phase 1 tests do not set a conflicting CLI filter.

### End-to-end behaviour to implement

Add the loader and XDG path helper. In the `profiles` command, load the config at startup; if present and valid, use its `profile-filter` as the authoritative allow-list for the listing; if absent, fall back to existing default behaviour; if malformed, exit non-zero.

### Acceptance criteria

- [ ] `[observable]` With a config file setting `profile-filter` to a restrictive regex, `imds-broker profiles` prints only matching profiles.
- [ ] `[observable]` With no config file present, `imds-broker profiles` behaves exactly as today (`DefaultFilter`).
- [ ] `[observable]` With a malformed YAML config file, `imds-broker profiles` exits non-zero and prints nothing to stdout.
- [ ] `[observable]` Setting `XDG_CONFIG_HOME` relocates the file the broker reads (verified by pointing it at a temp dir).
- [ ] `[structural]` Path resolution mirrors `resolveLogDir`; no flag/env wires the config path (R2).
- [ ] `[structural]` Unknown YAML keys are ignored without error (R4).

### Verification

Build the binary. Run `imds-broker profiles` against a temp `XDG_CONFIG_HOME` for three fixtures: restrictive filter, absent file, malformed file. Observe stdout and exit codes. Add loader unit tests using `t.Setenv` + temp dirs in the style of `cmd/imds-broker/main_test.go`. Run `just verify`.

### Regression watchpoints

- `profiles` command default output must be unchanged when no config exists.
- `profiles.List`'s existing env-var file-override behaviour (`AWS_CONFIG_FILE`) must remain intact.

### Replan triggers

- `cli-altsrc/v3` or the chosen YAML library is unavailable or pulls an incompatible dependency.
- A clean way to load config before flag evaluation in urfave/cli v3 proves infeasible (e.g. requires a `Before` hook that conflicts with existing wiring).

---

## Phase 2: Filter intersection and no-widening

**EARS requirements**: R9, R10, R11

**Carry-forward**: Re-verify Phase 1 (`profiles` restricted by config, fail-closed) before starting.

### Why this phase exists

Completes the security semantics for the `profiles` surface: a supplied filter may only narrow, never widen, the protected allow-set. This is the core guarantee that makes the protected filter trustworthy.

### Locked decisions (non-negotiable)

- Effective allow-set = protected filter AND supplied filter (intersection), evaluated as two regex predicates.
- A supplied `--profile-filter`/`IMDS_BROKER_PROFILE_FILTER` of `.*` (or any superset) cannot expand beyond the protected filter.
- Where the config omits `profile-filter`, the supplied filter applies alone, falling back to `profiles.DefaultFilter`.

### Flex zone (implementation choice allowed)

- Location of the composed predicate helper (e.g. extend `mcpserver.ProfileFilter`, or a shared helper). Naming.
- Whether intersection is computed in the command or inside a reusable filter type — provided the observable result is identical across surfaces.

### End-to-end behaviour to implement

Introduce the composed-predicate enforcement and apply it in the `profiles` command so the printed set is the intersection of config and supplied filters, with the omit-fallback path preserved.

### Acceptance criteria

- [ ] `[observable]` Config filter `Prod` + `--profile-filter ReadOnly` prints only profiles matching both.
- [ ] `[observable]` Config filter `ReadOnly` + `--profile-filter '.*'` still prints only `ReadOnly` profiles (no widening).
- [ ] `[observable]` No config `profile-filter` + `--profile-filter Foo` prints `Foo`-matching profiles; no filter at all falls back to `DefaultFilter`.
- [ ] `[structural]` Intersection is a logical AND of two compiled regexes, not a constructed combined regex.

### Verification

Run `imds-broker profiles` with combinations of config filter and `--profile-filter`, including the widening attempt. Add table-driven unit tests over profile-name sets for the composed predicate. Run `just verify`.

### Regression watchpoints

- Phase 1's config-only restriction must still hold when no CLI filter is supplied.

### Replan triggers

- Any surface needs a different composition rule than intersection (would contradict the locked security model).

---

## Phase 3: Apply the enforced filter to MCP and `serve`

**EARS requirements**: R13, R14, R15

**Carry-forward**: Re-verify Phases 1–2 (intersection + no-widening on `profiles`) before starting.

### Why this phase exists

Extends the proven enforcement point to the remaining credential-exposing surfaces so the protected filter governs every command, not just `profiles`. This is where the agent-facing MCP tools and the human-facing `serve` command are brought under the same control.

### Locked decisions (non-negotiable)

- `list_profiles` returns only the effective allow-set (R13).
- `create_server` for a profile outside the allow-set is rejected with an error and starts no server (R14).
- `serve --profile` checked against the protected filter before any AWS call; a non-match aborts startup with a non-zero exit (R15).
- The `serve` check reuses the same composed predicate as the other surfaces.

### Flex zone (implementation choice allowed)

- For `mcp`, reuse the existing `mcpserver.ProfileFilter` gate by feeding it the composed predicate; the lister may continue returning all profiles (`.*`).
- Where the `serve` gate is placed in the command action, provided it precedes credential loading.
- Error wording.

### End-to-end behaviour to implement

Load config in the `mcp` and `serve` commands. Compose the enforcement predicate (config ∩ supplied) and apply it: as the MCP gate for `list_profiles`/`create_server`, and as a pre-flight check in `serve`.

### Acceptance criteria

- [ ] `[observable]` Over MCP stdio, `list_profiles` returns only allow-set profiles given a config `profile-filter`.
- [ ] `[observable]` `create_server` for a disallowed profile returns an error and starts no server.
- [ ] `[observable]` `imds-broker serve --profile <disallowed>` exits non-zero before contacting AWS; an allowed profile starts normally.
- [ ] `[structural]` All three surfaces call the same composed-predicate helper introduced in Phase 2.

### Verification

Drive the MCP server over stdio (or its handler tests) for `list_profiles` and `create_server` with allowed/disallowed profiles. Run `serve` with allowed and disallowed `--profile` and observe exit codes and that no AWS call occurs on rejection. Run `just verify`.

### Regression watchpoints

- `mcp` `create_server` dedup/crash-recovery behaviour must be unaffected by the added gate.
- `serve` startup path for allowed profiles must be unchanged (Docker discovery, logging, signal handling).

### Replan triggers

- The MCP gate cannot reject `create_server` without business logic leaking into `mcpserver` (would violate the thin-adapter principle).

---

## Phase 4: Overridable defaults via `cli-altsrc`

**EARS requirements**: R16, R17, R18, R19

**Carry-forward**: Re-verify Phases 1–3 (enforcement across all surfaces) before starting.

### Why this phase exists

Delivers the convenience half of the file: persistent `region` and `log-level` defaults that an explicit flag still overrides. Independent of the security path, so it lands last.

### Locked decisions (non-negotiable)

- `log-level` precedence: flag > environment > config file > built-in default (R16).
- `serve` `region` precedence: flag > config file > profile-configured region (R17).
- An invalid `log-level` value in the file fails closed (non-zero exit) (R19).
- Defaults are supplied via `cli-altsrc/v3` at the fixed path; the enforced `profile-filter` is not routed through `cli-altsrc`.

### Flex zone (implementation choice allowed)

- Exact `cli-altsrc` source wiring and per-flag `Sources` ordering, provided the locked precedence holds.
- Whether the dual read (custom loader for the filter + `cli-altsrc` for defaults) is left as-is or unified later.

### End-to-end behaviour to implement

Wire `cli-altsrc/v3`'s YAML source into the `--log-level` (root) and `--region` (`serve`) flags, pointed at the resolved config path, so file values act as defaults beneath explicit flags.

### Acceptance criteria

- [ ] `[observable]` Config `log-level: debug` produces debug logging when `--log-level` is unset; passing `--log-level error` overrides it.
- [ ] `[observable]` Config `region: ap-southeast-2` is used by `serve` when `--region` is unset; `--region` overrides it.
- [ ] `[observable]` Config `log-level: bogus` causes a non-zero exit (fail closed).
- [ ] `[observable]` With no config file, flag defaults are unchanged from today.
- [ ] `[structural]` `profile-filter` is not wired through `cli-altsrc` (remains custom-loaded).

### Verification

Run the binary with config-only, flag-override, and invalid-value fixtures, observing effective log level, region, and exit codes. Run `just verify`.

### Regression watchpoints

- Existing `--log-level` and `--region` default behaviour with no config present.
- `cli-altsrc` must tolerate an absent config file without erroring on flag access (verify, since R6 requires absent → defaults).

### Replan triggers

- `cli-altsrc/v3` errors on a missing file or conflicts with the custom loader's read of the same path; if so, fall back to manual struct-based default application (PRD-sanctioned alternative).

---

## Requirements coverage matrix

| Requirement ID | Phase(s) | Notes |
|---|---|---|
| R1 | Phase 1 | XDG path resolution |
| R2 | Phase 1 | No override; verified structurally |
| R3 | Phase 1 | YAML |
| R4 | Phase 1 | Keys + unknown-ignored |
| R5 | Phase 1 | Load before flags/env |
| R6 | Phase 1 | Absent → defaults (re-checked in Phase 4 for `cli-altsrc`) |
| R7 | Phase 1 | Fail closed on parse error |
| R8 | Phase 1 | Config filter authoritative |
| R9 | Phase 2 | Intersection |
| R10 | Phase 2 | No widening |
| R11 | Phase 2 | Omit → supplied/DefaultFilter |
| R12 | Phase 1 | `profiles` restricted (intersection completed in Phase 2) |
| R13 | Phase 3 | `list_profiles` restricted |
| R14 | Phase 3 | `create_server` rejects |
| R15 | Phase 3 | `serve` gating |
| R16 | Phase 4 | log-level precedence |
| R17 | Phase 4 | serve region precedence |
| R18 | Phase 4 | file value when flag unset |
| R19 | Phase 4 | invalid log-level fails closed |
