# IMDS Proxy: Required Endpoints by AWS SDK

## Summary

All four major AWS SDKs (Python/botocore, Go v2, JavaScript v3, Java v2) use the same three IMDS paths for credential resolution. They diverge on region resolution: Python uses the availability-zone metadata path, Go and Java use the instance identity document, and JavaScript doesn't auto-resolve region from IMDS at all (requires `AWS_REGION` or config).

The IMDS proxy needs to handle **5 paths total**. Everything else should 404.

## Required Paths

### IMDSv2 Token (all SDKs)

```
PUT /latest/api/token
```
- Request header: `X-aws-ec2-metadata-token-ttl-seconds: <seconds>`
- Response: opaque token string (proxy can return any stable string)
- All SDKs send this first; the returned token is included as `X-aws-ec2-metadata-token` header on subsequent GETs

### Credential Resolution (all SDKs, identical flow)

**Step 1: Get role name**
```
GET /latest/meta-data/iam/security-credentials/
```
- Response: plain text role name (e.g. `SandyReadOnlyRole`)
- Single line, no trailing newline required

**Step 2: Get credentials for role**
```
GET /latest/meta-data/iam/security-credentials/{role-name}
```
- Response: JSON
```json
{
  "Code": "Success",
  "AccessKeyId": "ASIA...",
  "SecretAccessKey": "...",
  "Token": "...",
  "Expiration": "2024-12-30T22:31:56Z"
}
```
- Sources: botocore `InstanceMetadataFetcher._URL_PATH`, Go v2 `ec2rolecreds` via `GetMetadata("iam/security-credentials/")`, Java v2 `InstanceProfileCredentialsProvider.SECURITY_CREDENTIALS_RESOURCE`, JS v3 `fromInstanceMetadata`

### Region Resolution (SDK-specific)

**Python (botocore):** strips last character from AZ string
```
GET /latest/meta-data/placement/availability-zone/
```
- Response: plain text, e.g. `us-east-1a`
- SDK does `region = availability_zone[:-1]` → `us-east-1`
- Source: `InstanceMetadataRegionFetcher._URL_PATH`

**Go v2 / Java v1+v2:** parses region from instance identity document
```
GET /latest/dynamic/instance-identity/document
```
- Response: JSON with at minimum a `region` field
```json
{
  "region": "us-east-1",
  "availabilityZone": "us-east-1a",
  "instanceId": "i-0000000000000000",
  "accountId": "123456789012"
}
```
- Go source: `GetRegion` delegates to `buildGetInstanceIdentityDocumentPath`, extracts `.Region` from parsed JSON
- Java source: `EC2MetadataUtils` fetches `/latest/dynamic/instance-identity/document`
- Most fields can be synthetic/dummy — SDKs only extract `region`

**JavaScript v3:** does **not** auto-resolve region from IMDS. Requires `AWS_REGION` env var or SDK config. No additional IMDS path needed.

## Path Summary Table

| # | Path | Method | Used by | Purpose |
|---|---|---|---|---|
| 1 | `/latest/api/token` | PUT | All | IMDSv2 session token |
| 2 | `/latest/meta-data/iam/security-credentials/` | GET | All | List role name |
| 3 | `/latest/meta-data/iam/security-credentials/{name}` | GET | All | Credential JSON |
| 4 | `/latest/meta-data/placement/availability-zone/` | GET | Python | AZ string for region derivation |
| 5 | `/latest/dynamic/instance-identity/document` | GET | Go, Java | Identity doc JSON for region |

All other paths → 404.

## Implementation Notes

- **AZ value is trivially constructed**: `{region}a` works for all standard regions. No API call needed.
- **Identity document** needs valid JSON with `region` field; other fields are ignored by SDK region resolution but should be plausible (some user code may inspect them).
- **Token validation**: the proxy should issue a token on PUT and validate it on subsequent GETs via the `X-aws-ec2-metadata-token` header. Real IMDS rejects requests with expired or missing tokens (returns 401).
- **Trailing slashes**: Python's `InstanceMetadataRegionFetcher` uses a trailing slash on the AZ path. The proxy should handle both with and without.
- **`AWS_EC2_METADATA_SERVICE_ENDPOINT`**: all SDKs respect this env var to override the IMDS base URL. This is how the proxy is plumbed in — set this to the proxy's listen address.
- **Java v2 is IMDSv2-only**: no fallback to IMDSv1. Go/Python/JS will attempt IMDSv2 first and fall back. The proxy should implement IMDSv2 (PUT token flow) as the baseline.
