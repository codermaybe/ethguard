# RPC method ownership

Each registered read method owns its RPC name, MethodSpec, read-only descriptor, preparation, finite whole-key compatibility and request binding in its own method file. Public signatures remain on root Client/EthClient. Specs and Catalog collect those definitions; they do not expand a second configuration table.

Methods reuse object integrity and canonical representation from rpc/internal/objects and shared internal/ethobject, generated models from ethapi, and scalar/JSON primitives from wire/ethcodec. A method does not call another method's private preparation or correction. No extra compatibility rule means no empty switch is required.

The by-hash transaction's finite timestamp derivation and the in-block method's mined-only rule retain separate acceptance domains. Raw adapters remain available beside reviewed processors. A normalizer key is not an admission verdict, and canonical processing does not authorize silent changes to fault phases or source health.

Current knowledge ownership and route assembly: [topology](../../docs/design/codebase-topology.md). Whole-key rules, algorithm protection and open acceptance-domain issues: [method contracts](../../docs/design/method-contracts.md). Registered behavior and exact generated index are checked through [verification](../../docs/design/method-verification-pipeline.md).

The public generated-model migration is complete. Internal handwritten FeeHistory, AccessList and Log candidates are recorded for the code phase after the documentation checkpoint; this README does not authorize their replacement or a second registry/helper framework.
