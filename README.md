# imds-broker

Runs a local [IMDSv2](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/configuring-instance-metadata-service.html)-compatible server that vends AWS credentials from your local AWS config. Lets any AWS SDK tool behave as if it were running on EC2 — no environment variables required.

Useful for:
- Docker containers that need AWS credentials without injecting them as env vars
- CI pipelines or tools that only support EC2 instance credential resolution
- AI assistants (Claude, etc.) that need to call AWS APIs as part of agentic workflows

## What it does

imds-broker starts a local HTTP server implementing the EC2 Instance Metadata Service v2 (IMDSv2) protocol. AWS SDKs pointed at it via `AWS_EC2_METADATA_SERVICE_ENDPOINT` resolve credentials the same way they would on a real EC2 instance — by fetching a short-lived token then exchanging it for credentials.

The broker reads credentials from your AWS config files or SSO session on the host, validates them via STS, and vends them on demand. Static IAM credentials are automatically upgraded to short-lived session tokens before serving.

## Installation

### Direct download

Pre-built binaries are available on the [releases page](https://github.com/jamestelfer/imds-broker/releases).

Available platforms:

| OS      | Arch  | Archive                              |
|---------|-------|--------------------------------------|
| Linux   | amd64 | `imds-broker_linux_amd64.tar.gz`    |
| Linux   | arm64 | `imds-broker_linux_arm64.tar.gz`    |
| macOS   | amd64 | `imds-broker_darwin_amd64.tar.gz`   |
| macOS   | arm64 | `imds-broker_darwin_arm64.tar.gz`   |
| Windows | amd64 | `imds-broker_windows_amd64.zip`     |
| Windows | arm64 | `imds-broker_windows_arm64.zip`     |

Example (macOS arm64):

```sh
curl -L https://github.com/jamestelfer/imds-broker/releases/download/v0.1.0/imds-broker_darwin_arm64.tar.gz | tar xz
sudo mv imds-broker /usr/local/bin/
```

### mise

[mise](https://mise.jdx.dev/) can install imds-broker directly from GitHub Releases using the [github backend](https://mise.jdx.dev/dev-tools/backends/github.html):

```sh
mise use -g github:jamestelfer/imds-broker
```

### Build from source

Requires Go 1.22+.

```sh
go install github.com/jamestelfer/imds-broker/cmd/imds-broker@latest
```

## Usage

### MCP server (AI assistant integration)

The `mcp` command runs an [MCP](https://modelcontextprotocol.io/) stdio server that exposes three tools: `list_profiles`, `create_server`, and `stop_server`. AI assistants can use these to start IMDS servers on demand as part of an agentic workflow.

Configure in `claude_desktop_config.json` (or equivalent MCP host config):

```json
{
  "mcpServers": {
    "imds-broker": {
      "command": "imds-broker",
      "args": ["mcp"]
    }
  }
}
```

By default, only profiles matching `ReadOnly|ViewOnly` are exposed. Override with `--profile-filter`:

```sh
imds-broker mcp --profile-filter "my-team-.*"
# or via environment variable:
IMDS_BROKER_PROFILE_FILTER="my-team-.*" imds-broker mcp
```

### serve (standalone)

Starts a single IMDS server for a named AWS profile. Runs until interrupted.

```sh
imds-broker serve --profile my-profile [--region us-east-1]
```

On startup it logs the endpoint URL to stderr:

```
... INFO IMDS server listening url=http://127.0.0.1:PORT profile=my-profile
```

Point your AWS SDK at it:

```sh
export AWS_EC2_METADATA_SERVICE_ENDPOINT=http://127.0.0.1:PORT
aws s3 ls
```

Use `--quiet` to suppress stderr output (the URL still appears in the log file at `~/.local/state/sandy/logs/imds-broker/`).

### profiles (list)

Lists AWS profiles matching the filter as a JSON array. Useful for scripting or inspecting what the MCP server will expose.

```sh
imds-broker profiles [--profile-filter REGEX]
```

## Docker

The `serve` command binds to `0.0.0.0`, so containers can reach it on the host. No credentials enter the container — only the endpoint URL does.

```sh
# 1. Start on the host and note the port from the log output
imds-broker serve --profile prod-readonly
# stderr: ... INFO IMDS server listening url=http://127.0.0.1:54321 ...

# 2. Run the container pointing at the broker

# Linux (--network host):
docker run --rm \
  --network host \
  -e AWS_EC2_METADATA_SERVICE_ENDPOINT=http://127.0.0.1:54321 \
  amazon/aws-cli s3 ls

# macOS / Windows (Docker Desktop, host.docker.internal):
docker run --rm \
  -e AWS_EC2_METADATA_SERVICE_ENDPOINT=http://host.docker.internal:54321 \
  amazon/aws-cli s3 ls
```

This pattern works with any AWS SDK in any language inside the container.

## Strengths

- **No credential env vars** — credentials stay in AWS config files or SSO session on the host; they never enter a container or subprocess environment.
- **Full IMDSv2 compliance** — works with any AWS SDK that supports EC2 instance credential resolution, including older SDKs that predate newer credential providers.
- **Connection-filtered** — the token endpoint rejects connections from non-local addresses, reducing the risk of accidental credential exposure over the network.
- **MCP integration** — AI assistants can manage IMDS server lifecycle via natural-language tool calls.
- **Profile filtering** — the MCP server limits which profiles are visible via a configurable regex.
- **STS credential upgrade** — long-lived IAM credentials are automatically wrapped with STS `GetSessionToken` before vending, so the container always receives short-lived tokens.

## Caveats

- **AWS credentials must exist on the host** — the broker reads from local AWS config files or an active SSO session; it does not create credentials from nothing.
- **Ephemeral port** — the server binds to a random available port. You must read the port from stderr (or the log file) and pass it to your container or tool. There is no option to pin a fixed port.
- **Default profile filter is restrictive** — `ReadOnly|ViewOnly` profiles only. If your profiles don't match this pattern, set `--profile-filter` explicitly (e.g. `--profile-filter ".*"`).
- **No persistent state** — if the broker process exits, all running servers stop. Clients that cached the endpoint will need to reconnect after restarting the broker.
- **Mac/Windows Docker networking** — `--network host` is not supported on Docker Desktop; use `host.docker.internal` instead.
