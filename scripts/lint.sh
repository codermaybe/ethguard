#!/usr/bin/env bash
# Shared local, CI and release gate. Tool dependencies stay outside go.mod.
set -euo pipefail

readonly LINT_VERSION="2.15.0"
lint_bin="${GOLANGCI_LINT:-}"
lint_tmp=""
trap 'if [[ -n "$lint_tmp" ]]; then rm -rf -- "$lint_tmp"; fi' EXIT

if [[ -z "$lint_bin" ]]; then
    lint_bin="$(command -v golangci-lint || true)"
    if [[ -z "$lint_bin" ]] || [[ "$("$lint_bin" version --short)" != "$LINT_VERSION" ]]; then
        lint_tmp="$(mktemp -d)"
        GOBIN="$lint_tmp" go install "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v${LINT_VERSION}"
        lint_bin="$lint_tmp/golangci-lint"
    fi
fi

if [[ "$("$lint_bin" version --short)" != "$LINT_VERSION" ]]; then
    echo "golangci-lint ${LINT_VERSION} required; check GOLANGCI_LINT" >&2
    exit 1
fi

"$lint_bin" version
echo "==> linting all packages, tests, tools and generated code"
"$lint_bin" run --config .golangci.yml ./...
echo "==> linting the anvil build path (no local chain is started)"
"$lint_bin" run --config .golangci.yml --build-tags anvil ./...
