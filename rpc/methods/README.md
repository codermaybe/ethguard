# RPC Method Ownership

This package owns method-specific preparation, key-level compatibility,
request binding and execution-plan assembly for all registered read methods.
Public Go signatures stay on the root Client and EthClient view; shared object
processing stays in `rpc/internal/objects`, and generic codecs and schema
projection in `rpc/internal/wire`. The ownership source is
[codebase topology](../../docs/design/codebase-topology.md).

## Method file ownership

| File | Owns |
| --- | --- |
| `transaction_by_hash.go` | RPC name, public `TransactionByHash` mapping, MethodSpec, complete-key correction switch and this method's preparation/admission hooks |
| `transaction_in_block.go` | RPC name, public `TransactionInBlock` mapping, MethodSpec and its mined-only preparation/admission hooks |
| Matching `_test.go` files | Each method's identity, rules and refusal boundaries |
| `transaction_encoding_test.go` | Cross-method canonical-output and official-vector checks |
| `testdata/transaction` | Traceable inputs for method rules, with local/provider provenance distinguished |

Every registered read method follows this layout: the RPC name, public
mapping, MethodSpec, read-only MethodDescriptor, and the method's hooks live
in the method's own file. `Specs()` in `spec_inventory.go` and `Catalog()` in
`catalog.go` collect those definitions without repeating any method's
configuration. The centralized method-name table is retired. No new policy
type or registration mechanism is introduced.

Method preparation functions do not call another method's preparation or
correction functions. Both transaction methods use the shared transaction
encoder and validators directly. Pending timestamp derivation belongs only
to by-hash; block/index does not run that correction before rejecting pending.

The transaction two-method key switch — an outer key switch with an inner
switch for that key's whole-value state — is implemented and pinned by
`TestTransactionByHashCorrectionIdempotence` and the merge-contract tests in
`rpc/internal/objects/transaction`. Temporary execution checklists are not
versioned contracts.

## Method and object responsibilities

- Keep request-specific acceptance conditions and the concentrated key switch
  in the owning method. Do not correct internal paths or array elements.
- Use inner state branches for audited whole-key alternatives. Read context
  from an unchanged source snapshot and write corrections to a separate
  candidate. Dispatch missing-rule keys and present keys once each; do not
  process a synthesized key again. No extra rule means no empty switch is
  required, not that the field can skip validation or comparison.
- Share object constraints, payload/hash/signature checks and canonical
  encoding through existing object owners, not through a sibling method file.
- Reuse generic wire codecs; do not duplicate field schemas or build a
  MethodPolicy, callback framework, plugin registry or helpers package.
- Projection membership does not establish value validity. Check validation
  coverage before merging official fields not represented by a typed branch.
  PruneMethod currently also canonicalizes hex; responsibility cleanup must
  account for that behavior rather than describe it as projection only.
- Keep the original response available for source evidence. Normalize,
  Encode and Admit must use the same method preparation semantics.
- Keep typed projection in `internal/typed`, composition in `internal/runtime`,
  and transport, health, quorum and scheduling with their existing owners.

## Coverage and scope

The catalog is a read-only mapping and coverage index, not a second executable
policy table. A JSON-RPC policy can serve several public Go methods; this is
not a one-file-per-Go-wrapper duplication rule. Tests must retain the declared
public business mapping.

Existing root-only extensions, unsupported inventory entries and the write
pipeline keep their present behavior. Their presence is not permission to
invent formal methods for internal helpers or to bypass the public method
allow-list. Any separate surface cleanup needs its own scope review.
