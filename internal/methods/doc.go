// Package methods declares each registered Ethereum JSON-RPC read method:
// request binding, wire validation, requiredness, comparison whitelists, and
// the method's resolution strategy.
//
// rpcengine owns the Resolver contract, ReadPlan, and shared resolution
// strategies. rpc owns the public protocol and evidence data.
// This package owns the Ethereum side: one self-contained file per method —
// the RPC spelling, the MethodSpec, the read descriptor, the method's
// preparation and admission — collected by spec_inventory.go and
// catalog.go. Object packages own the shared representation, request binding
// and cryptographic commitments consumed by these method definitions.
//
// Unknown methods never get a file here; the aggregator supplies
// transparent forwarding, which must produce Forwarded, never Corroborated.
// Write methods are deliberately absent too: eth_sendRawTransaction is
// adjudicated by the write pipeline, not by a read policy.
//
// Scalar normalization lives in internal/ethcodec; shared object semantics
// and spec-driven pruning live in internal/ethobject and its wiremask package;
// standard-field projection in internal/projection. A method file composes those,
// it does not reimplement them.
package methods
