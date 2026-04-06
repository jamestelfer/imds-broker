# Plan: IMDS Broker

> Source PRD: `plans/prd.md`

## Architectural decisions

- **Module layout**: `cmd/imds-broker`, `pkg/imdsserver`, `pkg/broker`, `pkg/profiles`, `pkg/mcpserver`
- **Language**: Go 1.26; mise `.tool-versions` for toolchain management; `just` as command runner (`justfile`)
- **CLI**: urfave/cli v3; `--log-level` flag sets slog level at startup; commands: `serve`, `profiles`, `mcp`
- **HTTP**: stdlib `http.Server` + `http.ServeMux`; Go 1.22+ `METHOD /path/{param}` patterns exclusively
- **Middleware**: justinas/alice for all middleware chains
- **Logging**: slog JSON handler; per-request logger injected into context via middleware, carrying a `request_id`
- **MCP transport**: stdio only (mark3labs/mcp-go)
- **Routes**: `PUT /latest/api/token`, `GET /latest/meta-data/iam/security-credentials/`, `GET /latest/meta-data/iam/security-credentials/{role}`, `GET /latest/meta-data/placement/region`
- **Token format**: `base64url(expiry).base64url(hmac-sha256)`, stateless, per-server 32-byte random secret, TTL 1–21600s; expiry is a `time.Time` marshalled via `MarshalText()` (RFC 3339 UTC); HMAC computed over the raw text bytes; validated by verifying HMAC then checking expiry against `time.Now().UTC()` — no server-side token storage
- **Shutdown**: cancel root context immediately on SIGINT/SIGTERM/EOF, hard stop with ~1–2s deadline
- **Binding**: always `127.0.0.1`; never `0.0.0.0`; optional Docker gateway IP as second listener
- **CI**: GitHub Actions with current action versions; golangci-lint rules imported from `chinmina/chinmina-bridge`

---

## Phase 1: Project scaffold + IMDSv2 server core

**User stories**: IMDS Server requirements 1–12, 16 (token lifecycle, credential endpoints, region endpoint, auth middleware, logging middleware, token HMAC design)

### What to build

Bootstrap the Go module, mise `.tool-versions` (pinning Go 1.26 and golangci-lint), `justfile` with common recipes (build, test, lint), golangci-lint config copied from the current state of `chinmina/chinmina-bridge` and modified as needed, and GitHub Actions CI workflow. Then implement `pkg/imdsserver` end-to-end:

- IMDSv2 token creation (`PUT /latest/api/token`) with TTL validation and HMAC-SHA256 signing; expiry encoded via `time.Time.MarshalText()` (RFC 3339 UTC), HMAC computed over those bytes
- Token validation middleware (Alice chain) enforcing valid, non-expired token on all GET requests
- Credential listing endpoint returning principal name — principal name is a constructor parameter, injected at creation time (tests supply a fixed string; Phase 2 derives it from STS GetCallerIdentity)
- Credential detail endpoint returning IMDS-format JSON, including static-to-temporary credential upgrade via STS GetSessionToken
- Region endpoint
- Request logging middleware injecting a per-request slog logger with `request_id` into context
- Constructor accepting profile name, region, bind addresses (variadic or slice — localhost always first, Docker gateway optional), and logger; returns a server handle with bound URLs, a stop method, and a `Done() <-chan struct{}` channel that closes on crash or stop
- Credential provider is injectable for testing — no real AWS calls in Phase 1; Phase 2 wires the real SDK credential chain

Verified entirely with `httptest` and a mock credential provider — no real AWS calls, no CLI yet.

### Acceptance criteria

- [ ] `go.mod` and `go.sum` present; `mise .tool-versions` pins Go 1.26, golangci-lint, and just
- [ ] `justfile` present with at minimum `build`, `test`, and `lint` recipes
- [ ] GitHub Actions CI uses current action versions and runs via `just`
- [ ] `PUT /latest/api/token` returns a token for TTL 1–21600; rejects TTL 0 and 21601
- [ ] GET endpoints return HTTP 401 with missing, invalid, or expired token
- [ ] GET endpoints return correct response with a valid token
- [ ] Credential endpoint returns IMDS JSON shape with `Code`, `AccessKeyId`, `SecretAccessKey`, `Token`, `Expiration`
- [ ] Region endpoint returns the configured region string
- [ ] Principal listing endpoint returns the role/user name
- [ ] Request logger with `request_id` is present in context; all requests are logged
- [ ] All tests pass; golangci-lint clean

---

## Phase 2: Single-server `serve` command

**User stories**: CLI requirements 1–3, 6; Lifecycle requirements 1–2; IMDS Server requirements 8–9 (real AWS SDK credential chain, static-to-temporary upgrade via STS GetSessionToken, principal name from STS GetCallerIdentity)

### What to build

Wire `pkg/imdsserver` into the CLI as the `serve` command using urfave/cli v3. The command starts a single IMDS server bound to `127.0.0.1` and runs until interrupted. This is the first end-to-end demoable slice: run `imds-broker serve --profile myprofile --region us-east-1`, set `AWS_EC2_METADATA_SERVICE_ENDPOINT`, and any AWS SDK tool works against it.

No new `pkg/imdsserver` logic — Phase 2 only adds the real AWS SDK credential chain and derives the principal name via STS `GetCallerIdentity` at startup. Includes: `--profile` (required), `--region` (optional, defaults to profile-configured region), `--log-level` flag wired to slog, SIGINT/SIGTERM handling with hard stop (cancel context + ~1–2s deadline).

### Acceptance criteria

- [ ] `imds-broker serve --profile <name> --region <region>` starts and logs its bind address
- [ ] AWS CLI / SDK pointed at the server successfully retrieves credentials
- [ ] Static IAM credentials are upgraded to temporary credentials via STS GetSessionToken before serving
- [ ] `--log-level` flag controls slog output level
- [ ] SIGINT/SIGTERM triggers hard stop: context cancelled, server exits within ~2s
- [ ] Server refuses to start if profile name does not exist in AWS config
- [ ] All log output is structured JSON via slog

---

## Phase 3: Broker — multi-server management and Docker gateway

**User stories**: Broker requirements 1–9; IMDS Server requirements 13–15

### What to build

`pkg/broker` manages multiple `imdsserver` instances. Docker bridge gateway IP is discovered once at broker startup via a `CommandExecutor` interface (wrapping `exec.Command`) — the interface allows injection of a fake executor in tests, decoupling broker tests from a live Docker daemon.

Docker discovery runs `docker network inspect bridge --format '{{(index .IPAM.Config 0).Gateway}}'` and caches the result for the process lifetime. Each new IMDS server receives both `127.0.0.1` and (if discovered) the gateway IP as bind addresses.

Broker API: `CreateServer(profile, region)` with deduplication by `profile:region` key; crash detection via the server handle's `Done()` channel (closed on crash); `StopServer(url)` matches on either the localhost or Docker URL — the broker's registry maps both URLs to the same server entry. Crashed servers are cleaned up and replaced on the next `CreateServer` call for the same key.

`create_server` response includes the localhost URL always and the `http://host.docker.internal:<port>` URL when Docker discovery succeeded.

### Acceptance criteria

- [ ] `CreateServer` with the same profile+region returns the same URLs (deduplication)
- [ ] `CreateServer` with a crashed server starts a fresh one and returns new URLs
- [ ] `StopServer` with a valid URL stops the server and frees resources
- [ ] `StopServer` with an unknown URL returns an error
- [ ] `CreateServer` with a nonexistent profile returns an error without starting a server
- [ ] When Docker gateway discovery succeeds, server binds a second listener and both URLs are returned
- [ ] When Docker gateway discovery fails, only the localhost URL is returned
- [ ] A panic in an IMDS server goroutine is recovered, logged, and does not crash the broker
- [ ] Broker tests cover all of the above without real AWS calls or Docker daemon

---

## Phase 4: Profile lister and `profiles` command

**User stories**: Profiles requirements 1–3; CLI requirements 4, 6 (`profiles` command and `--profile-filter` flag)

### What to build

`pkg/profiles` reads available profiles from the AWS SDK config, compiles the filter as a regex, and returns matching names. Default filter pattern: `ReadOnly|ViewOnly`. Stateless — safe to call multiple times.

Wire into the CLI as the `profiles` command (JSON output to stdout) and declare `--profile-filter` as a per-command flag (with `IMDS_BROKER_PROFILE_FILTER` env var fallback) on both the `profiles` and `mcp` commands independently.

### Acceptance criteria

- [ ] `imds-broker profiles` prints a JSON array of profile names matching the default filter
- [ ] `--profile-filter <regex>` overrides the default filter
- [ ] `IMDS_BROKER_PROFILE_FILTER` env var is honoured when flag is not set
- [ ] Invalid regex returns a clear error
- [ ] Empty AWS config returns an empty list without error
- [ ] `pkg/profiles` tests cover default pattern, custom pattern, empty config, and regex edge cases

---

## Phase 5: MCP server and `mcp` command

**User stories**: MCP Server requirements 1–4; CLI requirement 5; Lifecycle requirement 3 (stdio EOF shutdown)

### What to build

`pkg/mcpserver` is a thin adapter over mark3labs/mcp-go registering three MCP tools — `list_profiles`, `create_server`, `stop_server` — and delegating to `pkg/profiles` and `pkg/broker`. No business logic lives here.

Wire into the CLI as the `mcp` command. `server.ServeStdio(s)` blocks until the client disconnects or stdin closes, then returns — the `mcp` command treats this return as the shutdown signal, cancels the root context, and hard-stops all IMDS servers. SIGINT/SIGTERM are handled in parallel via the same root context cancellation path.

MCP tool handlers receive a context-bound logger carrying a per-call `request_id`, injected the same way as HTTP request logging.

### Acceptance criteria

- [ ] `imds-broker mcp` starts and communicates over stdio using the MCP protocol
- [ ] `list_profiles` tool returns the filtered profile list
- [ ] `create_server` tool starts (or returns existing) IMDS server and returns URLs
- [ ] `stop_server` tool stops a running server or returns an error for an unknown URL
- [ ] MCP call context carries a per-call `request_id` in the logger
- [ ] EOF on stdin triggers graceful hard stop of all IMDS servers
- [ ] SIGINT/SIGTERM also triggers hard stop in `mcp` mode
