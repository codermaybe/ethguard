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
trap 'rm -f "$generated_types"' EXIT
go run ./rpc/internal/wire/gen -out "$generated_types"
if ! cmp -s "$generated_types" ethapi/types.gen.go; then
    echo "ethapi/types.gen.go is stale; run go generate ./ethapi" >&2
    diff -u ethapi/types.gen.go "$generated_types" || true
    exit 1
fi

echo "==> running static analysis"
go vet ./...

echo "==> building all packages"
go build ./...

echo "==> running race-enabled test suite"
# The locked-version method-set contract is intentionally soft during
# development but must be hard at release time. This prevents an intermediate
# facade subset from passing the release gate.
accept_external_simulate_gap="${ETHGUARD_ACCEPT_EXTERNAL_SIMULATE_GAP:-0}"
case "${accept_external_simulate_gap}" in
    0)
        ETHGUARD_REQUIRE_COMPLETE=1 go test -race ./...
        ;;
    1)
        echo "==> accepting the documented external SimulateV1 compatibility gap"
        method_diff="$(bash ./scripts/ethclient-method-diff.sh)"
        expected_gap="business missing: 1 [SimulateV1]"
        if ! grep -Fqx "${expected_gap}" <<<"${method_diff}"; then
            echo "external gap acceptance requires exactly: ${expected_gap}" >&2
            echo "actual method diff:" >&2
            echo "${method_diff}" >&2
            exit 1
        fi
        ETHGUARD_REQUIRE_COMPLETE=0 go test -race ./...
        ;;
    *)
        echo "ETHGUARD_ACCEPT_EXTERNAL_SIMULATE_GAP must be 0 or 1" >&2
        exit 1
        ;;
esac

echo "==> scanning reachable vulnerabilities"
bash ./scripts/govulncheck.sh

if [ "${accept_external_simulate_gap}" = "1" ]; then
    echo "release checks passed with accepted external SimulateV1 gap; EthClient is not complete"
else
    echo "release checks passed"
fi
