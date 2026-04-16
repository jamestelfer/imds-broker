# Release Publishing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Homebrew cask and npm multi-arch publishing to the existing GoReleaser-based release pipeline.

**Architecture:** GoReleaser handles the Homebrew cask via a `homebrew_casks` stanza that pushes to `jamestelfer/homebrew-tap`. npm publishing uses a bash script called post-goreleaser that extracts binaries from `dist/` archives, creates per-platform packages, and publishes them along with a main wrapper package — all authenticated via GitHub OIDC (no stored token).

**Tech Stack:** GoReleaser 2.15.2, GitHub Actions, npm, bash, Node.js

**Spec:** `docs/superpowers/specs/2026-04-17-release-publishing-design.md`

---

## Files

| Action | Path | Purpose |
|--------|------|---------|
| Modify | `.goreleaser.yaml` | Add `homebrew_casks` stanza |
| Modify | `.github/workflows/release.yml` | Add environment, permissions, homebrew token, setup-node, npm publish step |
| Create | `.github/workflows/npm/main/package.json` | Main `@jamestelfer/imds-broker` package template |
| Create | `.github/workflows/npm/main/bin/imds-broker.js` | JS launcher shim |
| Create | `.github/workflows/npm/publish.sh` | npm publish script |

---

## Prerequisites (out-of-band, do before first release)

These cannot be automated — complete them before cutting the first release tag:

1. **`GITHUB_HOMEBREW_TOKEN`**: Create a GitHub PAT with `contents: write` scoped to `jamestelfer/homebrew-tap`. Add it as a secret named `GITHUB_HOMEBREW_TOKEN` to the `release` environment in `jamestelfer/imds-broker` repository settings.

2. **npm package names and trusted publishers**: For each of the 7 packages below, configure a Trusted Publisher on npmjs.com (Package Settings → Publishing → Add a Trusted Publisher) with:
   - Scope: GitHub Actions
   - Repository owner: `jamestelfer`
   - Repository name: `imds-broker`
   - Workflow filename: `release.yml`
   - Environment: `release`

   Packages:
   - `@jamestelfer/imds-broker`
   - `@jamestelfer/imds-broker-linux-x64`
   - `@jamestelfer/imds-broker-linux-arm64`
   - `@jamestelfer/imds-broker-darwin-x64`
   - `@jamestelfer/imds-broker-darwin-arm64`
   - `@jamestelfer/imds-broker-windows-x64`
   - `@jamestelfer/imds-broker-windows-arm64`

   Note: packages must exist on npm before you can configure a trusted publisher. Publish each once manually with `npm publish --access public` from a minimal package directory, or use the script with a real token the first time.

---

## Task 1: Add goreleaser homebrew cask configuration

**Files:**
- Modify: `.goreleaser.yaml`

- [ ] **Step 1: Add `homebrew_casks` stanza to `.goreleaser.yaml`**

  Append after the `release:` block:

  ```yaml
  homebrew_casks:
    - name: imds-broker
      directory: Casks
      homepage: "https://github.com/jamestelfer/imds-broker"
      description: "AWS IMDSv2-compatible credential server for local development and CI"
      url:
        verified: "github.com/jamestelfer/imds-broker"
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

  The quarantine removal hook is required for unsigned/unnotarized binaries on macOS. The `url.verified` field helps `brew audit` pass when the homepage domain matches the download domain.

- [ ] **Step 2: Validate the goreleaser config**

  ```bash
  goreleaser check
  ```

  Expected: `config is valid` (or equivalent success message). If it fails, check indentation and field names against the error.

- [ ] **Step 3: Commit**

  ```bash
  git add .goreleaser.yaml
  git commit -m "feat(release): add homebrew cask publishing via goreleaser"
  ```

---

## Task 2: Update release workflow — homebrew

**Files:**
- Modify: `.github/workflows/release.yml`

- [ ] **Step 1: Add `environment` and `GITHUB_HOMEBREW_TOKEN` to the release job**

  The full updated `release.yml`:

  ```yaml
  name: Release

  on:
    push:
      tags:
        - 'v*'

  jobs:
    release:
      name: Release
      runs-on: ubuntu-latest
      environment: release
      permissions:
        contents: write
      steps:
        - uses: actions/checkout@v6
          with:
            fetch-depth: 0

        - uses: actions/setup-go@v6
          with:
            go-version-file: go.mod
            cache: true

        - name: Read goreleaser version
          id: tool-versions
          run: |
            version=$(grep '^goreleaser' .tool-versions | awk '{print $2}')
            echo "goreleaser=${version}" >> "$GITHUB_OUTPUT"

        - uses: goreleaser/goreleaser-action@v7
          with:
            version: ${{ steps.tool-versions.outputs.goreleaser }}
            args: release --clean
          env:
            GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
            GITHUB_HOMEBREW_TOKEN: ${{ secrets.GITHUB_HOMEBREW_TOKEN }}
  ```

  Changes from current:
  - Added `environment: release`
  - Added `GITHUB_HOMEBREW_TOKEN` to the goreleaser step env

- [ ] **Step 2: Commit**

  ```bash
  git add .github/workflows/release.yml
  git commit -m "ci(release): add release environment and homebrew token"
  ```

---

## Task 3: Create npm package static assets

**Files:**
- Create: `.github/workflows/npm/main/package.json`
- Create: `.github/workflows/npm/main/bin/imds-broker.js`

- [ ] **Step 1: Create the directory structure**

  ```bash
  mkdir -p .github/workflows/npm/main/bin
  ```

- [ ] **Step 2: Write the main package template**

  Create `.github/workflows/npm/main/package.json`:

  ```json
  {
    "name": "@jamestelfer/imds-broker",
    "version": "0.0.0-dev",
    "description": "AWS IMDSv2-compatible credential server for local development and CI",
    "homepage": "https://github.com/jamestelfer/imds-broker",
    "license": "MIT",
    "repository": {
      "type": "git",
      "url": "https://github.com/jamestelfer/imds-broker.git"
    },
    "bin": {
      "imds-broker": "./bin/imds-broker.js"
    },
    "files": [
      "bin/imds-broker.js"
    ],
    "optionalDependencies": {
      "@jamestelfer/imds-broker-linux-x64": "0.0.0-dev",
      "@jamestelfer/imds-broker-linux-arm64": "0.0.0-dev",
      "@jamestelfer/imds-broker-darwin-x64": "0.0.0-dev",
      "@jamestelfer/imds-broker-darwin-arm64": "0.0.0-dev",
      "@jamestelfer/imds-broker-windows-x64": "0.0.0-dev",
      "@jamestelfer/imds-broker-windows-arm64": "0.0.0-dev"
    },
    "engines": {
      "node": ">=18"
    }
  }
  ```

  The `publish.sh` script replaces all `0.0.0-dev` occurrences with the real version at publish time.

- [ ] **Step 3: Write the JS launcher shim**

  Create `.github/workflows/npm/main/bin/imds-broker.js`:

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
  const binaryPath = path.join(
    path.dirname(require.resolve(`${pkg}/package.json`)),
    bin
  );

  execFileSync(binaryPath, process.argv.slice(2), { stdio: 'inherit' });
  ```

  `require.resolve` finds `package.json` in the installed platform package — the binary sits alongside it.

- [ ] **Step 4: Commit**

  ```bash
  git add .github/workflows/npm/
  git commit -m "feat(release): add npm main package template and JS launcher shim"
  ```

---

## Task 4: Create npm publish script

**Files:**
- Create: `.github/workflows/npm/publish.sh`

- [ ] **Step 1: Write the publish script**

  Create `.github/workflows/npm/publish.sh`:

  ```bash
  #!/usr/bin/env bash
  set -euo pipefail

  # Publishes platform-specific and main npm packages from goreleaser dist/ archives.
  # Usage: publish.sh <version>
  #   <version>: git tag, e.g. v1.2.3 — leading 'v' is stripped automatically.
  # Environment:
  #   DIST_DIR: path to goreleaser dist directory (default: dist)

  DIST_DIR="${DIST_DIR:-dist}"
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

  TMPDIRS=()
  cleanup() {
    for d in "${TMPDIRS[@]+"${TMPDIRS[@]}"}"; do
      rm -rf "${d}"
    done
  }
  trap cleanup EXIT

  make_tmpdir() {
    local d
    d="$(mktemp -d)"
    TMPDIRS+=("${d}")
    echo "${d}"
  }

  publish_platform_package() {
    local version="${1}"
    local goreleaser_suffix="${2}"  # e.g. linux_amd64
    local npm_suffix="${3}"         # e.g. linux-x64
    local npm_os="${4}"             # e.g. linux
    local npm_cpu="${5}"            # e.g. x64
    local binary="${6}"             # e.g. imds-broker or imds-broker.exe

    local pkg_name="@jamestelfer/imds-broker-${npm_suffix}"
    local tmpdir
    tmpdir="$(make_tmpdir)"

    # Extract binary from archive
    if [[ "${goreleaser_suffix}" == windows_* ]]; then
      local archive="${DIST_DIR}/imds-broker_${goreleaser_suffix}.zip"
      unzip -j "${archive}" "${binary}" -d "${tmpdir}"
    else
      local archive="${DIST_DIR}/imds-broker_${goreleaser_suffix}.tar.gz"
      tar -xzf "${archive}" -C "${tmpdir}" "${binary}"
    fi

    chmod +x "${tmpdir}/${binary}"

    cat > "${tmpdir}/package.json" <<PKGJSON
  {
    "name": "${pkg_name}",
    "version": "${version}",
    "description": "AWS IMDSv2-compatible credential server — ${npm_os} ${npm_cpu} binary",
    "os": ["${npm_os}"],
    "cpu": ["${npm_cpu}"],
    "files": ["${binary}"],
    "license": "MIT",
    "repository": {
      "type": "git",
      "url": "https://github.com/jamestelfer/imds-broker.git"
    }
  }
  PKGJSON

    (cd "${tmpdir}" && npm publish --provenance --access public)
    echo "published ${pkg_name}@${version}"
  }

  publish_main_package() {
    local version="${1}"
    local tmpdir
    tmpdir="$(make_tmpdir)"

    cp -r "${SCRIPT_DIR}/main/." "${tmpdir}/"
    # Replace placeholder version with the release version
    sed -i "s/0\.0\.0-dev/${version}/g" "${tmpdir}/package.json"

    (cd "${tmpdir}" && npm publish --provenance --access public)
    echo "published @jamestelfer/imds-broker@${version}"
  }

  main() {
    local version="${1:?'usage: publish.sh <version>'}"
    version="${version#v}"  # strip leading v

    publish_platform_package "${version}" linux_amd64   linux-x64    linux  x64   imds-broker
    publish_platform_package "${version}" linux_arm64   linux-arm64  linux  arm64 imds-broker
    publish_platform_package "${version}" darwin_amd64  darwin-x64   darwin x64   imds-broker
    publish_platform_package "${version}" darwin_arm64  darwin-arm64 darwin arm64 imds-broker
    publish_platform_package "${version}" windows_amd64 windows-x64  win32  x64   imds-broker.exe
    publish_platform_package "${version}" windows_arm64 windows-arm64 win32 arm64 imds-broker.exe

    publish_main_package "${version}"
  }

  main "$@"
  ```

- [ ] **Step 2: Make the script executable**

  ```bash
  chmod +x .github/workflows/npm/publish.sh
  ```

- [ ] **Step 3: Run shellcheck**

  ```bash
  shellcheck .github/workflows/npm/publish.sh
  ```

  Expected: no output (exit 0). Fix any warnings before proceeding.

- [ ] **Step 4: Commit**

  ```bash
  git add .github/workflows/npm/publish.sh
  git commit -m "feat(release): add npm multi-arch publish script"
  ```

---

## Task 5: Update release workflow — npm

**Files:**
- Modify: `.github/workflows/release.yml`

- [ ] **Step 1: Add `id-token: write`, `setup-node`, and npm publish step**

  The full final `release.yml`:

  ```yaml
  name: Release

  on:
    push:
      tags:
        - 'v*'

  jobs:
    release:
      name: Release
      runs-on: ubuntu-latest
      environment: release
      permissions:
        contents: write
        id-token: write
      steps:
        - uses: actions/checkout@v6
          with:
            fetch-depth: 0

        - uses: actions/setup-go@v6
          with:
            go-version-file: go.mod
            cache: true

        - name: Read goreleaser version
          id: tool-versions
          run: |
            version=$(grep '^goreleaser' .tool-versions | awk '{print $2}')
            echo "goreleaser=${version}" >> "$GITHUB_OUTPUT"

        - uses: goreleaser/goreleaser-action@v7
          with:
            version: ${{ steps.tool-versions.outputs.goreleaser }}
            args: release --clean
          env:
            GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
            GITHUB_HOMEBREW_TOKEN: ${{ secrets.GITHUB_HOMEBREW_TOKEN }}

        - uses: actions/setup-node@v4
          with:
            node-version: '22'
            registry-url: 'https://registry.npmjs.org'

        - name: Publish npm packages
          run: .github/workflows/npm/publish.sh "${VERSION}"
          env:
            VERSION: ${{ github.ref_name }}
  ```

  Changes from Task 2's version:
  - Added `id-token: write` permission (required for npm OIDC trusted publishing)
  - Added `actions/setup-node@v4` step (configures npm registry)
  - Added npm publish step — no `NODE_AUTH_TOKEN` needed, OIDC handles auth

- [ ] **Step 2: Commit**

  ```bash
  git add .github/workflows/release.yml
  git commit -m "ci(release): add npm OIDC publishing to release workflow"
  ```

---

## Verification

After all tasks are complete and prerequisites are in place, verify end-to-end by cutting a release tag:

```bash
git tag v0.1.0
git push origin v0.1.0
```

Watch the Actions run. Confirm:
- GoReleaser builds archives and creates a GitHub release
- GoReleaser opens a PR (or direct push) to `jamestelfer/homebrew-tap` with the cask
- All 7 npm packages appear on `https://npmjs.com/~jamestelfer` at the correct version

To test the npm packages locally after publish:

```bash
npm install -g @jamestelfer/imds-broker
imds-broker --version
```
