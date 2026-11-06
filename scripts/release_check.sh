#!/usr/bin/env bash
# release_check.sh runs the reproducible offline gates required before a
# release commit is tagged. Live canary evidence remains a separate online
# workflow and is not simulated here.

set -euo pipefail


echo "==> checking Go toolchain"
go version

echo "==> checking formatting"
unformatted="$(gofmt -l .)"
if [ -n "${unformatted}" ]; then
    echo "files not formatted with gofmt:" >&2
    echo "${unformatted}" >&2
    exit 1
fi

echo "==> checking module consistency"
go mod tidy -diff

echo "==> running static analysis"
go vet ./...

echo "==> building all packages"
go build ./...

echo "==> running race-enabled test suite"
# The locked-version method-set contract is intentionally soft during
# development but must be hard at release time. This prevents an intermediate
# facade subset from passing the release gate.
ETHGUARD_REQUIRE_COMPLETE=1 go test -race ./...

echo "==> scanning reachable vulnerabilities"
./scripts/govulncheck.sh

echo "release checks passed"
