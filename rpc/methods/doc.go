// Package methods holds one policy per corroborated Ethereum JSON-RPC method:
// request binding, wire validation, requiredness, comparison whitelists, and
// the method's resolution strategy.
//
// The parent rpc package owns everything true of any method and any JSON —
// the Policy contract, the Registry, and the shared resolution strategies.
// This package owns the Ethereum side: one eth_*.go per method, plus the
// object slices (block, receipt, transaction, logs) whose field tables and
// admission rules are shared by more than one method or carry a
// cryptographic commitment.
//
// Unknown methods never get a file here; they fall through the registry to
// transparent forwarding, which must produce Forwarded, never Corroborated.
// Write methods are deliberately absent too: eth_sendRawTransaction is
// adjudicated by the write pipeline, not by a read policy.
//
// Shared wire normalization and spec-driven pruning live in
// rpc/internal/wire; shared object semantics in rpc/internal/objects;
// standard-field projection in rpc/projection. A method file composes those,
// it does not reimplement them.
package methods
