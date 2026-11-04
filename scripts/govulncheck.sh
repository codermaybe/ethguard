#!/usr/bin/env bash
# Run the repository's pinned reachable-vulnerability scanner with the same
# project toolchain used by local checks and CI.

set -euo pipefail

readonly GOVULNCHECK_VERSION="v1.9.0"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT

mkdir -p "${tmp_dir}/bin"
GOBIN="${tmp_dir}/bin" \
    go install "golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}"

"${tmp_dir}/bin/govulncheck" ./...
