---
title: Release Publishing — Homebrew Cask + npm Multi-Arch
date: 2026-04-17
status: approved
---

# Release Publishing Design

## Overview

Add two new distribution channels to the existing GoReleaser-based release pipeline:

1. **Homebrew cask** — publish `imds-broker` to `jamestelfer/homebrew-tap` via goreleaser's `homebrew_casks` stanza
2. **npm multi-arch packages** — publish `@jamestelfer/imds-broker` and 6 platform-specific packages to the npm registry using the esbuild-style pattern

The existing release workflow (tag-triggered, goreleaser on ubuntu-latest) is extended in-place. No new workflows are added.

---

## Section 1 — Homebrew Cask

### Mechanism

GoReleaser v2.10+ deprecates `brews` (formula) in favour of `homebrew_casks`, which is the correct mechanism for distributing pre-built binaries. We are on goreleaser 2.15.2.

### `.goreleaser.yaml` changes

Add a `homebrew_casks` stanza:

```yaml
homebrew_casks:
  - name: imds-broker
    directory: Casks
    homepage: "https://github.com/jamestelfer/imds-broker"
    description: "AWS IMDSv2-compatible credential server for local development and CI"
    repository:
      owner: jamestelfer
      name: homebrew-tap
      token: "{{ .Env.GITHUB_HOMEBREW_TOKEN }}"
    binaries:
      - imds-broker
    hooks:
      post:
        install: |
          if OS.mac?
            system_command "/usr/bin/xattr", args: ["-dr", "com.apple.quarantine", "#{staged_path}/imds-broker"]
          end
```

The quarantine removal hook is required because the binary will not be Apple-notarized.

### CI changes

- The release job must target the `release` GitHub Actions environment (where `GITHUB_HOMEBREW_TOKEN` is scoped)
- Add `GITHUB_HOMEBREW_TOKEN: ${{ secrets.GITHUB_HOMEBREW_TOKEN }}` to the goreleaser step's `env` block

### Prerequisites

- `GITHUB_HOMEBREW_TOKEN`: a PAT with `contents: write` on `jamestelfer/homebrew-tap`, stored as a secret in the `release` environment

---

## Section 2 — npm Multi-Arch Publishing

### Pattern

Follows the esbuild-style pattern:
- 6 platform-specific packages (`@jamestelfer/imds-broker-{os}-{arch}`) each containing only the binary
- 1 main package (`@jamestelfer/imds-broker`) with `optionalDependencies` pointing to all platform packages and a JS launcher shim as the `bin` entry

npm's `optionalDependencies` mechanism causes the package manager to install only the package matching the current platform (matched via the `os` and `cpu` fields in each platform package's `package.json`).

### Platform mapping

| GoReleaser archive suffix | npm package suffix | `os`      | `cpu`    |
|---------------------------|--------------------|-----------|----------|
| `linux_amd64`             | `linux-x64`        | `linux`   | `x64`    |
| `linux_arm64`             | `linux-arm64`      | `linux`   | `arm64`  |
| `darwin_amd64`            | `darwin-x64`       | `darwin`  | `x64`    |
| `darwin_arm64`            | `darwin-arm64`     | `darwin`  | `arm64`  |
| `windows_amd64`           | `windows-x64`      | `win32`   | `x64`    |
| `windows_arm64`           | `windows-arm64`    | `win32`   | `arm64`  |

### Repository structure

```
.github/workflows/npm/
  publish.sh              # publish script, called from CI with $VERSION
  main/
    package.json          # main package template (version placeholder)
    bin/
      imds-broker.js      # JS launcher shim
```

Platform packages are generated entirely at publish time from goreleaser's `dist/` archives — no static per-platform directories in the repo.

### `publish.sh` responsibilities

1. Accept `$VERSION` as first argument (semver without leading `v`, e.g. `1.2.3`)
2. For each of the 6 platforms:
   a. Locate the archive in `dist/` by name pattern
   b. Extract the binary (`imds-broker` or `imds-broker.exe`)
   c. Create a temp directory, write `package.json` (name, version, os, cpu, files, main)
   d. Copy binary into the temp dir
   e. `npm publish --access public`
3. Create main package from `.github/workflows/npm/main/`, substituting version into `package.json` and `optionalDependencies`
4. `npm publish --access public`

### JS launcher shim (`bin/imds-broker.js`)

```js
#!/usr/bin/env node
'use strict';
const { execFileSync } = require('child_process');
const path = require('path');

const platforms = {
  'linux-x64':    '@jamestelfer/imds-broker-linux-x64',
  'linux-arm64':  '@jamestelfer/imds-broker-linux-arm64',
  'darwin-x64':   '@jamestelfer/imds-broker-darwin-x64',
  'darwin-arm64': '@jamestelfer/imds-broker-darwin-arm64',
  'win32-x64':    '@jamestelfer/imds-broker-windows-x64',
  'win32-arm64':  '@jamestelfer/imds-broker-windows-arm64',
};

const key = `${process.platform}-${process.arch}`;
const pkg = platforms[key];
if (!pkg) {
  console.error(`imds-broker: unsupported platform ${key}`);
  process.exit(1);
}

const bin = process.platform === 'win32' ? 'imds-broker.exe' : 'imds-broker';
const binaryPath = path.join(path.dirname(require.resolve(`${pkg}/package.json`)), bin);

execFileSync(binaryPath, process.argv.slice(2), { stdio: 'inherit' });
```

### npm auth — OIDC trusted publishing

npm is configured with GitHub as a trusted publisher (no stored token required). The workflow requests an OIDC token from GitHub; npm validates it and authorises the publish.

`npm publish` is called with `--provenance`, which attaches a signed attestation linking the package to the exact commit and workflow run.

### CI changes

- Add `id-token: write` to the job's `permissions` block (required to obtain the OIDC token)
- Add a `uses: actions/setup-node` step with `registry-url: https://registry.npmjs.org` before the publish step
- After the goreleaser step:
  ```yaml
  - name: Publish npm packages
    run: .github/workflows/npm/publish.sh "${VERSION}"
    env:
      VERSION: ${{ github.ref_name }}   # e.g. v1.2.3 — script strips leading 'v'
  ```

The publish script calls `npm publish --provenance --access public`. No `NODE_AUTH_TOKEN` is needed — the OIDC exchange is handled automatically by the npm CLI when the `id-token: write` permission is present.

### Prerequisites

- Each `@jamestelfer/imds-broker*` package must have a Trusted Publisher configured on npmjs.com scoped to repo `jamestelfer/imds-broker`, workflow `release.yml`, environment `release`
- The `@jamestelfer/imds-broker*` package names must be claimed on npm before the first publish (initial publish with `--access public`)

---

## CI summary

```yaml
jobs:
  release:
    environment: release          # scopes GITHUB_HOMEBREW_TOKEN; satisfies npm trusted publisher env constraint
    permissions:
      contents: write
      id-token: write             # required for npm OIDC trusted publishing
    steps:
      - checkout
      - setup-go
      - goreleaser (produces dist/, pushes cask to homebrew-tap)
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          GITHUB_HOMEBREW_TOKEN: ${{ secrets.GITHUB_HOMEBREW_TOKEN }}
      - setup-node (registry-url: https://registry.npmjs.org)
      - npm publish step (.github/workflows/npm/publish.sh)
        env:
          VERSION: ${{ github.ref_name }}   # v1.2.3 — script strips 'v'
          # no NPM_TOKEN needed — OIDC trusted publishing
```
