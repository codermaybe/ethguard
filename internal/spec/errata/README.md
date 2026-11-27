# Reviewed standard corrections

These files correct pinned execution-apis schemas. They never rewrite provider
responses. The upstream bundles, vectors, VERSION and SOURCE.json remain the
independent source evidence.

Each JSON file contains its ID, exact source version/commit, target method and
`result` or `request` parameter (zero-based), reason, references, retirement
condition, input/output SHA-256, and an RFC 6902 `patch` array. Input hashes cover
the original extracted schema bytes; output hashes cover the fixed patch
implementation's bytes. Formatting changes also require review.

Audit the effective view with:

```sh
go test ./internal/executionapi -run '^TestErrata' -v
go generate ./ethapi
```

The generator header identifies applied correction files and their digests.
`executionapi.ReadOriginal` exposes the unmodified source;
`executionapi.Read` is the shared effective view for generation, masks and field
projection. Source drift and obsolete/redundant corrections fail explicitly.
An upstream fix requires reviewing and deleting or narrowing the correction,
then regenerating and rerunning the independent behavior checks. Retain this
README when the last JSON correction is deleted so an empty inventory works.

The contract and remaining source conflicts are maintained in
[wire-codegen.md](../../../docs/design/wire-codegen.md), with work ordered by the
[project roadmap](../../../docs/design/project-roadmap.md).
