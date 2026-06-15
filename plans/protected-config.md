# Protected Configuration File

## Problem Statement

IMDS Broker vends AWS credentials to tooling, including AI agents that drive the `mcp` command. The profile filter is the primary blast-radius control: it limits which profiles an agent can request credentials for. Today that filter is set via the `--profile-filter` flag or the `IMDS_BROKER_PROFILE_FILTER` environment variable. Both live on the command line that the agent — or whatever launches the broker — controls. An agent that can set its own flags can set `--profile-filter '.*'` and reach every profile, defeating the control.

The filter needs a source the agent cannot rewrite. It must also stay simple: a control that is awkward to configure will not be deployed, and an undeployed control protects nothing.

## Solution

Add a protected configuration file at a fixed, well-known path outside the broker's command line. The file lives in the host's XDG config directory, where a sandboxed agent's filesystem view does not expose it. The operator writes it once; the broker reads it on every invocation.

The file carries two kinds of settings:

- **Enforced** — the `profile-filter`. The file's filter is authoritative. A CLI or environment filter may only *narrow* it (a profile is permitted when it matches both). Nothing on the command line can widen it.
- **Defaults** — `region` and `log-level`. These are convenience defaults. An explicit flag or environment variable overrides them.

Trust derives solely from the path. The broker performs no ownership or permission checks on the file — those add setup friction without adding protection when the path itself is outside the agent's reach. When the agent cannot write the fixed path, the enforced filter holds. When the agent fully controls the host (including that path), the feature degrades to convenience defaults and offers no guarantee; that boundary is the operator's to enforce through sandboxing.

## Security Model

| Aspect | Decision |
|---|---|
| Trust basis | Fixed path only. No ownership, mode, or signature checks. |
| Path | `${XDG_CONFIG_HOME:-$HOME/.config}/imds-broker/config.yaml`. Fixed; not overridable by flag or environment variable. |
| Threat addressed | An agent that controls the broker's command line widening the profile filter to reach disallowed profiles. |
| Guarantee holds when | The protected path is outside the agent's write access (broker sandboxed from the agent, or the config directory is read-only to it). |
| Guarantee does not hold when | The agent can write the protected path (fully agent-controlled host). The feature then provides defaults only. |
| Complementary control | `~/.aws` must be off-limits to the agent. Otherwise the agent reads credentials directly and bypasses the broker entirely. Out of scope for this feature; an operator deployment requirement. |

## Requirements

### Configuration source

1. The broker shall resolve its configuration path as `${XDG_CONFIG_HOME:-$HOME/.config}/imds-broker/config.yaml`, mirroring the XDG resolution already used for the log directory.
1. The configuration path shall not be overridable by any CLI flag or environment variable.
1. The configuration file shall be YAML.
1. The configuration file shall support the keys `profile-filter`, `region`, and `log-level`. Unknown keys shall be ignored.
1. When a command starts, the broker shall load the configuration file before evaluating CLI flags and environment variables.
1. Where the configuration file is absent, the broker shall apply built-in defaults: `profiles.DefaultFilter` for filtering, and the existing flag defaults for region and log level.
1. If the configuration file is present but cannot be read or parsed, then the command shall exit non-zero with an error and shall not start a server, list profiles, or serve any MCP tool (fail closed).

### Enforced profile filter

1. When the configuration file specifies `profile-filter`, the broker shall treat it as the authoritative allow-list of AWS profile names.
1. If a `--profile-filter` flag or `IMDS_BROKER_PROFILE_FILTER` environment variable is supplied alongside the protected filter, then the effective allow-set shall be the intersection: a profile is permitted only if it matches both the protected filter and the supplied filter.
1. A CLI or environment filter shall never widen the protected filter. Profiles excluded by the protected filter shall remain excluded regardless of any supplied filter.
1. Where the configuration file omits `profile-filter`, the supplied filter (flag or environment variable) shall apply on its own, falling back to `profiles.DefaultFilter` when none is supplied.
1. The `profiles` command output shall be restricted to the effective allow-set.
1. The MCP `list_profiles` tool result shall be restricted to the effective allow-set.
1. When the MCP `create_server` tool is called for a profile outside the effective allow-set, the broker shall reject the call with an error and shall not start a server.
1. When `serve` is invoked with `--profile`, if the protected profile filter does not permit that profile, then the command shall refuse to start and exit non-zero with an error.

### Default settings precedence

1. The effective log level shall follow the precedence: explicit `--log-level` flag, then `IMDS_BROKER_*` environment variable where applicable, then the configuration file's `log-level`, then the built-in default.
1. The effective `serve` region shall follow the precedence: explicit `--region` flag, then the configuration file's `region`, then the profile-configured region.
1. Where the configuration file provides `region` or `log-level` and the corresponding flag is not set explicitly, the broker shall use the file value.
1. If the configuration file provides a `log-level` value that is not a valid slog level, then the command shall fail closed per the parse-error requirement above.

## Implementation Decisions

### Configuration path resolution

Add a resolver mirroring `resolveLogDir`: read `XDG_CONFIG_HOME`, falling back to `filepath.Join(home, ".config")`, then append `imds-broker/config.yaml`. Manual XDG resolution is used rather than `os.UserConfigDir` for consistency with the existing log-directory logic and to keep `~/.config` semantics on macOS (where `os.UserConfigDir` returns `~/Library/Application Support`). The path is computed in `cmd/`; no flag or environment override is wired.

### Loader

A small loader reads the file once at startup and unmarshals YAML into a typed struct with optional fields. A missing file yields zero values and no error. A present-but-unreadable or unparseable file returns an error, which `cmd/` surfaces as a non-zero exit. The loader performs no filesystem permission or ownership checks.

### Enforced filter composition

The protected filter and any supplied filter are evaluated as two independent predicates; a profile passes only when both match. This is a logical AND of two `regexp` matches, not the construction of a combined regular expression — no regex algebra is required.

- For `mcp`, the existing `mcpserver.ProfileFilter` gate becomes the composed predicate. The list function continues to return all profiles (`.*`); the composed filter is the single gate for `list_profiles` and `create_server`.
- For `profiles`, the command applies the protected filter as the `List` regex and further filters the result by the supplied filter, producing the same intersection.
- For `serve`, the composed predicate is checked against `--profile` before any AWS call; a non-match aborts startup.

### Overridable defaults

`region` and `log-level` defaults are supplied through the `github.com/urfave/cli-altsrc/v3` YAML source, pointed at the resolved configuration path. This is the idiomatic urfave/cli v3 mechanism for file-backed defaults and keeps flag precedence (explicit flag beats file) without hand-written fallback logic. The enforced `profile-filter` is deliberately not wired through `cli-altsrc`, because an altsrc-backed flag value would be overridable on the command line; it is loaded and composed by the custom loader instead.

The configuration file is therefore read twice — once by the custom loader for the enforced filter, once lazily by `cli-altsrc` for the defaults. Both reads target the same fixed path. The redundancy is accepted for the clean split between enforced and overridable settings.

### Load timing

Configuration is loaded once at command startup. There is no hot-reload; a running server keeps the configuration it started with. This matches the existing "no persistent state across restarts" stance.

## Testing Decisions

- **Loader**: present/absent/malformed files; XDG resolution with and without `XDG_CONFIG_HOME`; unknown keys ignored; invalid `log-level` rejected. Use `t.Setenv` and temp dirs as the existing `resolveLogDir` tests do.
- **Filter composition**: protected-only, supplied-only, both (intersection), and the widening attempt (supplied `.*` against a restrictive protected filter must not widen). Table-driven over profile names.
- **Command integration**: `serve` refuses a disallowed `--profile`; `profiles` output equals the intersection; fail-closed on malformed file exits non-zero. Thin `cmd`/`mcpserver` layers defer to integration coverage, consistent with the existing testing stance.

## Out of Scope

- Ownership, permission-mode, or signature verification of the configuration file.
- Overriding the configuration path via flag or environment variable.
- Hot-reload or re-read of configuration while a server runs.
- Sandboxing the broker from the agent, or making `~/.aws` read-only — operator deployment concerns, not broker behaviour.
- Configuration of settings beyond `profile-filter`, `region`, and `log-level` (for example `quiet`, bind addresses, or Docker discovery).
- Windows-specific configuration paths beyond the XDG fallback.

## Further Notes

- The enforced `profile-filter` extends the existing `ReadOnly|ViewOnly` default safety net into a control an agent cannot loosen from its command line.
- The fixed-path, no-checks design is a deliberate trade against setup friction: a control that is trivial to deploy is more likely to be deployed than one gated on correct file ownership and modes.
- When the agent both launches the MCP host and controls the host filesystem, the protected file sits inside the agent's trust boundary and provides defaults only. Document this so operators do not over-trust the control in agent-controlled environments.
