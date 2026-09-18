package methods

// This file owns the transaction namespace method constant. Shared
// transaction semantics live in rpc/internal/objects/transaction.

// MethodSendRawTransaction is the write-side JSON-RPC method. It has no
// Policy in the read registry: the write pipeline owns its collection and
// adjudication (pool.Broadcast + relative-trust resolution), not the
// method-policy registry. The constant lives here as the transaction
// namespace's method name until the write side gets its own home.
const MethodSendRawTransaction = "eth_sendRawTransaction"
