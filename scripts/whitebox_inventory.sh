#!/usr/bin/env bash
# whitebox_inventory.sh — audit which unexported package symbols each
# white-box test file depends on.
#
# Usage: scripts/whitebox_inventory.sh [package...]
# With no arguments it audits the five packages listed in the ownership doc
# (docs/design/whitebox-test-ownership.md).
#
# Method: top-level unexported declarations are collected from the package's
# non-test sources with grep, then every white-box test file is scanned for
# word-boundary references to them. The result over-approximates (a symbol
# named in a comment counts), which is the safe direction for an ownership
# audit: a file listed with zero unexported references is a true "can move
# out" candidate, a file with references needs a human look.

set -euo pipefail

packages=("$@")
if [ ${#packages[@]} -eq 0 ]; then
    packages=(rpc rpc/methods pool aggregator internal/runtime)
fi

for pkg in "${packages[@]}"; do
    echo "== $pkg"
    # Unexported top-level symbols from production sources.
    symbols=$(grep -hE '^(func|type|var|const) [a-z][A-Za-z0-9_]*' "$pkg"/*.go 2>/dev/null \
        | sed -E 's/^(func|type|var|const) ([a-z][A-Za-z0-9_]*).*/\2/' | sort -u)
    if [ -z "$symbols" ]; then
        echo "  (no unexported top-level symbols)"
        continue
    fi
    # Package name check: only in-package (white-box) files.
    pkgname=$(go list -f '{{.Name}}' "./$pkg")
    for test in "$pkg"/*_test.go; do
        [ -e "$test" ] || continue
        if ! head -1 "$test" | grep -q "^package $pkgname\$"; then
            echo "  $test: black-box"
            continue
        fi
        hits=""
        while IFS= read -r sym; do
            if grep -qE "\b$sym\b" "$test"; then
                hits="$hits $sym"
            fi
        done <<< "$symbols"
        if [ -n "$hits" ]; then
            count=$(echo "$hits" | wc -w)
            echo "  $test: $count unexported refs:$hits"
        else
            echo "  $test: 0 unexported refs (movable)"
        fi
    done
done
