# IMDS Broker

## Problem Statement

Developers and CI pipelines need AWS credentials available via the EC2 Instance Metadata Service (IMDS) protocol for tools and SDKs that expect to run on EC2. Outside of EC2 — on developer machines, in Docker containers, or in non-EC2 CI — there is no IMDS endpoint. Teams work around this with environment variables, credential files mounted into containers, or wrapper scripts, all of which are fragile and diverge from the production credential path.

## Solution

IMDS Broker is a Go service that starts local IMDS-compatible HTTP servers on demand, each serving credentials for a named AWS profile and region. It operates in three modes:

- **MCP server** (`mcp` command): exposes tools over stdio for AI-assisted workflows — listing available profiles and creating/stopping IMDS server instances.
- **Standalone server** (`serve` command): runs a single IMDS server for a given profile and region.
- **Profile listing** (`profiles` command): outputs filtered AWS profiles as JSON.

Each IMDS server implements the full IMDSv2 protocol (session token required) and resolves credentials through the standard AWS SDK credential chain. Servers bind to a single IP (not all interfaces), preferring the resolved address of `host.docker.internal` for Docker compatibility.

## Requirements

### IMDS Server

1. The IMDS server shall implement the IMDSv2 session token protocol, requiring a valid token for all metadata GET requests.
1. When a client sends a PUT request to `/latest/api/token` with a valid `X-aws-ec2-metadata-token-ttl-seconds` header, the IMDS server shall return a unique session token with the requested TTL.
1. If a PUT request to `/latest/api/token` specifies a TTL outside the range 1–21600 seconds, then the IMDS server shall reject the request with an appropriate error status.
1. When a client sends a GET request to `/latest/meta-data/iam/security-credentials/` with a valid session token, the IMDS server shall return the principal name derived from STS GetCallerIdentity at server startup.
1. When a client sends a GET request to `/latest/meta-data/iam/security-credentials/<role-name>` with a valid session token, the IMDS server shall return the current credentials in the standard IMDS JSON format.
1. When a client sends a GET request to `/latest/meta-data/placement/region` with a valid session token, the IMDS server shall return the configured region.
1. If a GET request has a missing, invalid, or expired session token, then the IMDS server shall respond with HTTP 401.
1. The IMDS server shall resolve credentials using the AWS SDK’s standard credential chain for the configured profile, delegating refresh and caching to the SDK.
1. When the configured profile has static (long-lived) credentials without a session token, the IMDS server shall call STS GetSessionToken to convert them to temporary credentials before serving.
1. The IMDS server shall validate session tokens using HMAC-SHA256 with a per-server random secret, embedding the expiry in the token itself. No server-side token storage is required.
1. The IMDS server shall never bind to all interfaces (`0.0.0.0`).
1. The IMDS server shall always bind to `127.0.0.1` on an ephemeral port.
1. When starting, the broker shall attempt to discover the Docker bridge gateway IP by executing `docker network inspect bridge` and extracting the gateway address.
1. Where the Docker bridge gateway IP was discovered, the IMDS server shall also bind to the gateway IP on an ephemeral port, creating a second listener sharing the same handler.
1. If the Docker bridge gateway discovery fails (Docker not installed, daemon not running, or command error), then the IMDS server shall only listen on `127.0.0.1`.
1. The IMDS server shall log every incoming request using slog with structured fields.

### Broker

1. When `create_server` is called with a profile and region, the broker shall start a new IMDS server and return the localhost URL.
1. Where the Docker bridge gateway listener is active, the broker shall also return the `http://host.docker.internal:<port>` URL in the `create_server` response.
1. If Docker bridge gateway discovery failed, then the broker shall omit the Docker-accessible URL from the response.
1. When `create_server` is called with a profile and region that matches a running IMDS server, the broker shall return the existing server’s URLs without starting a new one.
1. If `create_server` is called with a profile and region matching a server that has crashed, then the broker shall start a new server and return the new URLs.
1. When `stop_server` is called with a URL matching a running IMDS server, the broker shall stop that server and release its resources.
1. If `stop_server` is called with a URL that does not match any running server, then the broker shall return an error.
1. If a profile name does not exist in the AWS configuration, then the broker shall return an error without starting a server.
1. If an IMDS server panics, then the broker shall recover from the panic, log the error, and clean up the server’s resources without crashing the broker process.

### Profiles

1. The profile lister shall read available profiles from the AWS SDK configuration.
1. The profile lister shall filter profiles using a configurable regex substring match.
1. Where no filter is configured, the profile lister shall default to the regex `ReadOnly|ViewOnly`.

### MCP Server

1. The MCP server shall communicate over stdio using the MCP protocol.
1. The MCP server shall expose a `list_profiles` tool that returns the filtered profile list.
1. The MCP server shall expose a `create_server` tool that accepts a profile (required) and region (required) and delegates to the broker.
1. The MCP server shall expose a `stop_server` tool that accepts a URL and delegates to the broker.

### CLI

1. The CLI shall use urfave/cli v3 for command and flag parsing.
1. The CLI shall provide three commands: `mcp`, `profiles`, and `serve`.
1. The `serve` command shall accept `--profile` (required), `--region` (optional, defaults to the profile’s configured region), and start a single IMDS server in the foreground.
1. The `profiles` command shall print filtered profiles as JSON to stdout.
1. The `mcp` command shall start the MCP stdio server.
1. The CLI shall accept a `--profile-filter` flag (with `IMDS_BROKER_PROFILE_FILTER` env var fallback) applicable to the `mcp` and `profiles` commands.

### Lifecycle & Logging

1. The broker shall use slog with a JSON handler for all log output.
1. When the process receives SIGINT or SIGTERM, the broker shall gracefully shut down all running IMDS servers and exit.
1. While running in MCP mode, when the stdio pipe reaches EOF, the broker shall gracefully shut down all running IMDS servers and exit.

## Implementation Decisions

### Existing Codebase

The project builds on [benkehoe/imds-credential-server](https://github.com/benkehoe/imds-credential-server), forked to `jamestelfer/imds-credential-server` on the `towards-multi-server` branch. The existing single-file implementation provides a working IMDSv2 server with HMAC-based token validation, credential serving (including static-to-temporary credential upgrade via STS GetSessionToken), request logging middleware, and token validation middleware. The refactoring work extracts this into a multi-server architecture.

### Module Design

Packages are laid out under `pkg/` for importability. The `cmd/` directory contains only the CLI entrypoint.

**`pkg/imdsserver`** — The core deep module. Extracts the existing IMDS HTTP handler, token, and credential logic into a reusable package. Constructor takes profile name, region, bind addresses (localhost + optional Docker gateway), and `*slog.Logger`. Starts one or two `http.Server` instances sharing the same `http.Handler` (mux + middleware chain). Returns a server handle exposing the bound URLs and a stop method. Panic recovery wraps each listener’s goroutine internally with deferred `recover()`.

**`pkg/broker`** — Manages multiple `imdsserver` instances. Keyed by `profile:region` pair for deduplication. Runs Docker bridge gateway discovery once at startup via `docker network inspect bridge`. Passes bind addresses (localhost + optional gateway IP) to each new `imdsserver`. Detects crashed servers on access and cleans up stale entries.

**`pkg/profiles`** — Reads AWS config via the SDK, filters by compiled regex, returns a profile list. Stateless — used directly by both the CLI `profiles` command and the MCP `list_profiles` tool.

**`pkg/mcpserver`** — Thin adapter registering three MCP tools and translating calls to the broker and profile lister. No business logic.

**`cmd/imds-broker`** — urfave/cli v3 command tree, signal handling, and wiring. Constructs the slog logger, broker, profile lister, and either starts the MCP server, prints profiles, or runs a single IMDS server.

### IMDSv2 Token Design

Tokens use HMAC-SHA256 with a per-server random 32-byte secret (from the existing implementation). The token format is `base64(expiry).base64(hmac)` — the expiry is embedded in the token and validated on each request by verifying the HMAC signature and checking the expiry time. This is stateless: no server-side token map or cleanup needed. TTL bounds (1–21600 seconds) are validated at token creation time.

### IMDS Credential Format

The `/latest/meta-data/iam/security-credentials/<role-name>` endpoint returns the standard IMDS JSON shape:

```json
{
  "Code": "Success",
  "LastUpdated": "2024-01-01T00:00:00Z",
  "Type": "AWS-HMAC",
  "AccessKeyId": "...",
  "SecretAccessKey": "...",
  "Token": "...",
  "Expiration": "..."
}
```

The principal name at the `/latest/meta-data/iam/security-credentials/` listing endpoint is derived from STS GetCallerIdentity at server startup — for assumed roles this is the role name, for IAM users the username. The `Expiration` field reflects the SDK credential’s expiry if available, or a synthetic future time (1 hour) for long-lived credentials. Static IAM credentials are upgraded to temporary credentials via STS GetSessionToken before serving.

### Docker Connectivity

At broker startup, execute `docker network inspect bridge --format '{{(index .IPAM.Config 0).Gateway}}'` to discover the Docker bridge gateway IP. Cache the result for the process lifetime. Each IMDS server always binds to `127.0.0.1` on an ephemeral port. If Docker gateway discovery succeeded, the server also binds to the gateway IP on a separate ephemeral port, creating a second listener sharing the same `http.Handler`. The `create_server` response always includes the localhost URL (`http://127.0.0.1:<port>`) and, when Docker discovery succeeded, also the Docker-friendly URL (`http://host.docker.internal:<docker-port>`).

### Panic Recovery

Each IMDS server listener’s `ListenAndServe` runs in a goroutine wrapped with `defer recover()`. On panic, the recovery function logs the panic value and stack trace via slog, marks the server as crashed in the broker’s registry, and closes all listeners for that server. The broker detects the crashed state on subsequent `create_server` calls for the same key and starts a fresh server.

## Testing Decisions

**`imdsserver`**: High-value target. Test IMDSv2 token lifecycle (create, use, expire, reject), credential endpoint format, region endpoint, TTL boundary validation (0, 1, 21600, 21601), and 401 on missing/invalid/expired tokens. Use `httptest` for request testing and a mock credential provider to avoid real AWS calls.

**`broker`**: Test deduplication (same key returns same server), crash recovery (panic in server, next create starts fresh), stop semantics (stop by URL, error on unknown URL), and profile validation (nonexistent profile returns error).

**`profiles`**: Test regex filtering including default pattern, empty config, and regex edge cases.

**`mcpserver`** and **`cmd`**: Thin layers — defer to integration testing.

## Out of Scope

- IMDSv1 support (v2 only)
- IMDS metadata paths beyond IAM credentials and region (instance identity document, network interfaces, block device mappings, etc.)
- Persistent configuration or state across broker restarts
- SSE transport for MCP (stdio only for now)
- Authentication or access control on the IMDS servers themselves (beyond IMDSv2 tokens)
- Automatic profile discovery refresh while the broker is running

## Further Notes

- The IMDS Broker is related to Sandy — it provides the local AWS credential vending that Sandy’s workflows depend on.
- The `serve` command enables simple single-server usage without MCP, useful for scripting or direct Docker Compose integration via `AWS_EC2_METADATA_SERVICE_ENDPOINT`.
- The profile filter default of `ReadOnly|ViewOnly` is a safety net — it limits MCP-exposed profiles to read-only roles by default, reducing the blast radius of accidental credential use.
