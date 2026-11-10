#!/usr/bin/env bash
# Compare every public package in the previous release and the working tree.
# apidiff module exports retain removed packages; changes are report-only,
# while download, package-loading, export, and report errors are fatal.

set -euo pipefail

if [[ $# -ne 1 ]] || [[ ! ${1-} =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$ ]]; then
    echo "usage: bash scripts/release_api_diff.sh PREVIOUS_TAG" >&2
    echo "PREVIOUS_TAG must be an existing vMAJOR.MINOR.PATCH tag (optional prerelease); the caller handles releases with no previous tag" >&2
    exit 2
fi

for tool in go jq; do
    if ! command -v "${tool}" >/dev/null 2>&1; then
        echo "release API comparison requires ${tool} in PATH" >&2
        exit 1
    fi
done

readonly previous_tag="$1"
readonly module="github.com/codermaybe/ethguard"
readonly apidiff="golang.org/x/exp/cmd/apidiff@v0.0.0-20261014143641-f0251eb4aa3e"
repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
readonly repo_root
readonly evidence="${repo_root}/release-evidence"
readonly report="${evidence}/api-diff.txt"
readonly diagnostics="${evidence}/api-diff.log"
mkdir -p "${evidence}"
: >"${diagnostics}"
printf 'API comparison: %s@%s -> working tree\nTool: %s\n\n' \
    "${module}" "${previous_tag}" "${apidiff}" >"${report}"

stage="reading current module"
trap 'status=$?; if (( status != 0 )); then
    printf "API comparison failed while %s (exit %s); evidence retained in %s\n" "${stage}" "${status}" "${evidence}" >&2
    printf "\nERROR: API comparison failed while %s (exit %s). See api-diff.log.\n" "${stage}" "${status}" >>"${report}"
    cat "${diagnostics}" >&2
fi' EXIT

cd -- "${repo_root}"
# Never let a surrounding workspace or mutable module resolution change the
# inputs. In particular, package loading must not write into the module cache.
export GOWORK=off
export GOFLAGS=-mod=readonly
go list -m -json >"${evidence}/new-module.json" 2>>"${diagnostics}"
if ! toolchain="$(jq -er --arg module "${module}" '
    select(.Path == $module)
    | .GoVersion
    | select(type == "string" and test("^[0-9]+\\.[0-9]+\\.[0-9]+$"))
    | "go" + .
' "${evidence}/new-module.json" 2>>"${diagnostics}")"; then
    echo "current module must be ${module} and declare an exact Go patch version" >>"${diagnostics}"
    exit 1
fi
# Use the current module's exact toolchain for both versions and for apidiff;
# a newer requirement must fail rather than silently switching toolchains.
export GOTOOLCHAIN="${toolchain}"
printf 'Go toolchain: %s\n\n' "${toolchain}" >>"${report}"

stage="downloading previous release ${previous_tag}"
if ! go mod download -json "${module}@${previous_tag}" \
    >"${evidence}/old-module.json" 2>>"${diagnostics}"; then
    # go mod download also reports some failures in JSON rather than stderr.
    jq -r '.Error // empty' "${evidence}/old-module.json" >>"${diagnostics}" 2>&1 || true
    exit 1
fi
old_dir="$(jq -er --arg module "${module}" --arg tag "${previous_tag}" '
    select(.Path == $module and .Version == $tag and .Error == null)
    | .Dir | select(type == "string" and length > 0)
' "${evidence}/old-module.json" 2>>"${diagnostics}")"
if [[ ! -d ${old_dir} ]]; then
    echo "previous release module directory is unavailable: ${old_dir}" >>"${diagnostics}"
    exit 1
fi

stage="exporting previous release module API"
(
    cd -- "${old_dir}"
    go run "${apidiff}" -m -w "${evidence}/old.api" "${module}"
) 2>>"${diagnostics}"

stage="exporting working-tree module API"
go run "${apidiff}" -m -w "${evidence}/new.api" "${module}" 2>>"${diagnostics}"

stage="reporting module API changes"
# ModuleChanges compares both package sets, including packages absent from new.
# The pinned command returns success for API differences, not for tool errors.
go run "${apidiff}" -m "${evidence}/old.api" "${evidence}/new.api" \
    >>"${report}" 2>>"${diagnostics}"
printf 'API comparison complete (changes are report-only): %s\n' "${report}"
