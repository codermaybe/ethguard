#!/usr/bin/env bash
# Run from the release checkout root. This gate reads repository state only;
# success evidence is written after every candidate and approval check passes.
set -euo pipefail

fail() {
    printf '::error::release candidate: %s\n' "$*" >&2
    exit 1
}

for variable in VERSION EXPECTED_SHA GITHUB_REPOSITORY GITHUB_OUTPUT GITHUB_STEP_SUMMARY; do
    [[ -n ${!variable:-} ]] || fail "${variable} must be supplied by the release workflow"
done
for tool in git gh jq; do
    command -v "$tool" >/dev/null 2>&1 || fail "${tool} is required in PATH"
done

if ! [[ "$VERSION" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$ ]]; then
    fail 'version must be a v-prefixed SemVer without build metadata'
fi
if [[ "$VERSION" == *-* ]]; then
    IFS=. read -ra identifiers <<< "${VERSION#*-}"
    for identifier in "${identifiers[@]}"; do
        if [[ "$identifier" =~ ^[0-9]+$ && "$identifier" == 0?* ]]; then
            fail 'numeric prerelease identifiers must not have leading zeros'
        fi
    done
fi

if ! checkout_sha="$(git rev-parse HEAD)"; then
    fail 'cannot read checkout HEAD; inspect the checkout error above'
fi
[[ "$checkout_sha" == "$EXPECTED_SHA" ]] || fail 'checkout HEAD differs from the dispatch commit; rerun from the intended master commit'
if ! checkout_status="$(git status --porcelain --untracked-files=all)"; then
    fail 'cannot read checkout status; inspect the git error above'
fi
[[ -z "$checkout_status" ]] || fail 'checkout must be clean; inspect changes made before the candidate gate'
[[ ! -e dist && ! -L dist ]] || fail 'dist already exists; inspect leftover build artifacts before rerunning'
[[ ! -e release-evidence && ! -L release-evidence ]] || fail 'release-evidence already exists; inspect leftover candidate evidence before rerunning'

if ! remote_sha="$(gh api "repos/$GITHUB_REPOSITORY/git/ref/heads/master" --jq .object.sha)"; then
    fail 'cannot read remote master; check repository access and contents: read permission'
fi
[[ "$remote_sha" == "$EXPECTED_SHA" ]] || fail 'master has moved since dispatch; start a new release run from the current master commit'
tag_status=0
git ls-remote --exit-code --tags "https://github.com/$GITHUB_REPOSITORY.git" "refs/tags/$VERSION" || tag_status=$?
case "$tag_status" in
    2) ;;
    0) fail "tag $VERSION already exists; select an unused version (tags are never overwritten)" ;;
    *) fail "cannot look up remote tag $VERSION; inspect the git error above and check repository access" ;;
esac

if ! environment="$(gh api "repos/$GITHUB_REPOSITORY/environments/release")"; then
    fail 'cannot read the release Environment; confirm Settings > Environments contains release, then check repository access and prepare job actions: read permission (see the API error above)'
fi
if ! jq -e '
    any(.protection_rules[]; .type == "required_reviewers" and .prevent_self_review == true and (.reviewers | length) > 0)
    and .deployment_branch_policy.custom_branch_policies == true
' <<< "$environment" >/dev/null; then
    fail 'release Environment must have at least one required reviewer, prevent self-review, and use selected deployment branches; configure these in Settings > Environments > release'
fi
if ! branches="$(gh api "repos/$GITHUB_REPOSITORY/environments/release/deployment-branch-policies" --jq .branch_policies)"; then
    fail 'cannot read release deployment branch policies; check the release Environment and prepare job actions: read permission'
fi
if ! jq -e 'length == 1 and .[0].name == "master" and .[0].type == "branch"' <<< "$branches" >/dev/null; then
    fail 'release Environment must allow only the master branch; configure one master branch rule and no tag or additional branch rules'
fi
if ! releases="$(gh api "repos/$GITHUB_REPOSITORY/releases?per_page=100" --paginate --slurp)"; then
    fail 'cannot list existing releases; check repository access and contents: read permission'
fi
if ! jq -e --arg version "$VERSION" 'all(.[][]; .tag_name != $version)' <<< "$releases" >/dev/null; then
    fail "release $VERSION already exists or the releases response is invalid; inspect existing drafts and published releases, then select an unused version"
fi
if ! previous="$(jq -r '[.[][] | select(.draft == false)][0].tag_name // empty' <<< "$releases")"; then
    fail 'cannot identify the previous published release from the releases response'
fi

mkdir release-evidence
printf '%s\n' "$EXPECTED_SHA" > release-evidence/commit.txt
printf 'sha=%s\nversion=%s\nprevious=%s\n' "$EXPECTED_SHA" "$VERSION" "$previous" >> "$GITHUB_OUTPUT"
printf '### Candidate %s\n\nCommit: %s\nPrevious published release: %s\n' "$VERSION" "\`$EXPECTED_SHA\`" "\`$previous\`" >> "$GITHUB_STEP_SUMMARY"
