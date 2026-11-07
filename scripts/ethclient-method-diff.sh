#!/usr/bin/env bash
set -euo pipefail

# Compare the exported method sets in the locked go-ethereum ethclient.Client
# and EthGuard's EthClient view. Run from the repository root.
tmp_file=$(mktemp "${TMPDIR:-/tmp}/ethclient-method-diff.XXXXXX.go")
trap 'rm -f "$tmp_file"' EXIT
cat >"$tmp_file" <<'EOF'
package main

import (
	"fmt"
	"reflect"
	"sort"

	eg "github.com/codermaybe/ethguard"
	ge "github.com/ethereum/go-ethereum/ethclient"
)

func methodSet(t reflect.Type) map[string]bool {
	set := make(map[string]bool, t.NumMethod())
	for i := 0; i < t.NumMethod(); i++ {
		set[t.Method(i).Name] = true
	}
	return set
}

func sorted(set map[string]bool) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func main() {
	upstream := methodSet(reflect.TypeOf((*ge.Client)(nil)))
	view := methodSet(reflect.TypeOf((*eg.EthClient)(nil)))
	missing := make(map[string]bool)
	for name := range upstream {
		if !view[name] {
			missing[name] = true
		}
	}
	businessMissing := make(map[string]bool, len(missing))
	for name := range missing {
		if name != "Client" {
			businessMissing[name] = true
		}
	}
	fmt.Printf("go-ethereum ethclient.Client: %d methods\n", len(upstream))
	fmt.Printf("ethguard EthClient: %d methods\n", len(view))
	fmt.Printf("missing (including excluded Client accessor): %d %v\n", len(missing), sorted(missing))
	fmt.Printf("business target: %d\n", len(upstream)-1)
	fmt.Printf("business missing: %d %v\n", len(businessMissing), sorted(businessMissing))
}
EOF

go run "$tmp_file"
