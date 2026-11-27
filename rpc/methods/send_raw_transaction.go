package methods

// MethodSendRawTransaction is the write-side JSON-RPC method spelling this
// file owns. It has no read-registry spec: the write pipeline owns its
// collection and adjudication (pool.Broadcast + relative-trust resolution),
// and the descriptor below only records that ownership in the inventory.
const MethodSendRawTransaction = "eth_sendRawTransaction"

func sendRawTransactionDescriptor() MethodDescriptor {
	return MethodDescriptor{
		Name:      MethodSendRawTransaction,
		Kind:      WriteMethod,
		Family:    FamilyWrite,
		Coverage:  CoverageRegistered,
		Policy:    "WritePipeline",
		Adapter:   "SendTransaction",
		Alignment: "v1.17.10",
		Notes:     "Root Client.SendRawTransaction; broadcast + relative-trust, no read registry policy.",
	}
}
