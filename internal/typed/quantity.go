package typed

import (
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/rpc"
)

// QuantityHeight converts one canonical block-height quantity into its exact
// uint64 value. Values beyond the uint64 range are reported, never
// truncated and never routed through float64.
func QuantityHeight(q ethapi.Quantity) (uint64, error) {
	return rpc.ParseHexQuantity(string(q))
}
