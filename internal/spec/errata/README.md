# Reviewed standard overlays

These definitions correct the pinned execution-apis reference schemas. They do
not rewrite provider responses. The original bundles, inventory, vectors and
SOURCE.json remain independent source evidence.

Three `kind: "patch"` documents use RFC 6902 operations with explicit local
`before` and `after` guards. `data-whole-bytes.json` is one `data-pattern` rule:
its finite reviewed target set replaces the old DATA pattern with the complete
byte pattern. Every target identifies its schema node and unchanged local
identity. Exceptions, if needed, require an exact source node, identity, reason
and retirement condition in this same document.

Each definition carries an ID, reason, HTTPS references and retirement
condition. `dependsOn` declares ordering; filenames do not define semantics.
Cross-overlay overlapping writes require dependencies. Operations inside a
single reviewed RFC 6902 patch retain their declared order. Missing dependencies,
cycles, duplicate IDs, duplicate targets, stale guards and repeated application
fail atomically. Root-replacing patches are rejected; declare the affected
schema subpaths instead.

`executionapi.EvaluateOverlays` reports each target as `applicable`,
`already-satisfied` or `review-required`. The ordinary `Read` path rejects both
unresolved retirement candidates and conflicts. A diagnostic candidate never
becomes a runtime partial view. Formatting-only source changes do not require
hand-edited root hashes; measured original/input/output identities and changed
paths are written to the generated audit.

`ValidateSelectedData` independently scans the selected effective schemas.
An old DATA pattern outside the reviewed target set fails generation unless an
exact active exception covers it. Unselected schema inventory is not silently
expanded into compiler input. Stale and unused exceptions also fail.

Run `go test ./internal/executionapi` for independent scope, strictness,
retirement, escape and atomicity checks. Full rule semantics, source trust and
upgrade commands are owned by [the engineering contract](../../../docs/design/wire-codegen.md).
Retain this README when no JSON rules remain so an empty overlay inventory can
still be embedded and tested.
