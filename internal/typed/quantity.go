package typed

import (
	"strconv"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/rpc"
)

// QuantityHeight converts one canonical block-height quantity into its exact
// uint64 value. Values beyond the uint64 range are reported, never
// truncated and never routed through float64.
func QuantityHeight(q ethapi.Quantity) (uint64, error) {
	return rpc.ParseHexQuantity(string(q))
}

// QuantityDecimal spells a canonical quantity in decimal for context keys
// and comparisons that must not depend on hex width or case.
func QuantityDecimal(q ethapi.Quantity) (string, error) {
	height, err := rpc.ParseHexQuantity(string(q))
	if err != nil {
		return "", err
	}
	return strconv.FormatUint(height, 10), nil
}
