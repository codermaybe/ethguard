// Package wire owns method-independent JSON wire handling: bounded
// structural validation is delegated to internal/jsonsafe, value
// normalization to the primitive normalizers here (quantity, data, hash,
// address, bloom), and spec-driven pruning to the mask compiler, which
// builds the comparison whitelists from the pinned execution-apis bundle.
//
// It holds no Ethereum method semantics: which fields a given method
// requires, how a request binds to a response, and what a valid answer
// means all live in rpc/methods. Unknown or vendor fields are pruned out of
// comparison keys without being deleted from the winning payload, where they
// stay available to invocation-scoped Uncertain capture.
package wire
