#!/usr/bin/env bash
# Run the repository's quality gates before preparing a release candidate.
# govulncheck requires network access. Live canary is a separate observation.

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

echo "==> checking generated public types"
generated_types="$(mktemp)"
generated_audit="$(mktemp)"
trap 'rm -f "$generated_types" "$generated_audit"' EXIT
go run ./internal/wiregen -out "$generated_types" -audit-out "$generated_audit"
if ! cmp -s "$generated_types" ethapi/types.gen.go; then
    echo "ethapi/types.gen.go is stale; run go generate ./ethapi" >&2
    diff -u ethapi/types.gen.go "$generated_types" || true
    exit 1
fi
if ! cmp -s "$generated_audit" ethapi/types.audit.json; then
    echo "ethapi/types.audit.json is stale; run go generate ./ethapi" >&2
    diff -u ethapi/types.audit.json "$generated_audit" || true
    exit 1
fi

echo "==> running static analysis"
go vet ./...
bash ./scripts/lint.sh

echo "==> compiling the opt-in anvil tests without starting a chain"
go test -tags anvil -run '^$' ./...

echo "==> building all packages"
go build ./...

echo "==> running race-enabled test suite"
echo "==> checking the complete locked-version business method set"
ETHGUARD_REQUIRE_COMPLETE=1 go test -race ./...

echo "==> scanning reachable vulnerabilities"
bash ./scripts/govulncheck.sh

echo "release checks passed"
