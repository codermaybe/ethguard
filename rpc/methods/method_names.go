package methods

// The remaining centralized method-name declarations. Migrated methods own
// their spellings alongside their specs and descriptors; each RPC spelling
// still has exactly one declaration. The write method has no read policy.

// MethodCall is the JSON-RPC method name resolved by CallPolicy.
const MethodCall = "eth_call"

// MethodEstimateGas is the JSON-RPC method name resolved by
// EstimateGasPolicy.
const MethodEstimateGas = "eth_estimateGas"

// MethodSendRawTransaction is the write-side JSON-RPC method. It has no
// Policy in the read registry: the write pipeline owns its collection and
// adjudication (pool.Broadcast + relative-trust resolution), not the
// method-policy registry.
const MethodSendRawTransaction = "eth_sendRawTransaction"
