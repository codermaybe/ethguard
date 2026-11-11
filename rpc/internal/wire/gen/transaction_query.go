package main

import (
	"crypto/sha256"
	"fmt"
)

// allowPendingTransactionQueryContext is a fixed generation correction for the
// v1.0.0-beta.7 machine schema, NOT a provider compatibility rule. EIP-1474's
// eth_getTransactionByHash defines null inclusion metadata for pending queries:
// https://eips.ethereum.org/EIPS/eip-1474#eth_gettransactionbyhash
// Geth v1.17.10 internal/ethapi/api.go RPCTransaction/newRPCTransaction also
// emits blockTimestamp:null alongside blockHash/blockNumber/transactionIndex
// when no block is assigned. Keep the verbatim spec bundle as a separate oracle.
//
// Only this method's freshly parsed AST is changed, before walking/deduplication.
// The semantic fingerprint separates it from block-embedded transactions. All
// four fields remain required; method admission owns whole-context consistency.
func allowPendingTransactionQueryContext(version string, root *schema) error {
	if version != "v1.0.0-beta.7" || root == nil {
		return fmt.Errorf("pending transaction query correction: unexpected source version %q or nil schema; review the fixed policy", version)
	}
	const original = "cf4618aa76c97172404b7e2e9bee92b70c2c3b2915ab9f51d7dd31f6e078dc47"
	got := fmt.Sprintf("%x", sha256.Sum256([]byte(fingerprint(root))))
	if got != original {
		return fmt.Errorf("pending transaction query correction: source schema fingerprint %s != %s; review the fixed policy", got, original)
	}
	envelope := root.branches[1]
	for index := range envelope.properties {
		property := &envelope.properties[index]
		switch property.name {
		case "blockHash", "blockNumber", "blockTimestamp", "transactionIndex":
			property.schema = &schema{title: property.schema.title, compKind: "oneOf", branches: []*schema{
				{typeVal: "null"}, property.schema,
			}}
		}
	}
	return nil
}
