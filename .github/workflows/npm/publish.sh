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
