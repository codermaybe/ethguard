package methods

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"

	"ethguard/rpc"
)

// MethodChainID is the JSON-RPC method name resolved by ChainIDPolicy.
const MethodChainID = "eth_chainId"

// ChainIDPolicy verifies eth_chainId by full agreement.
func ChainIDPolicy() rpc.Policy { return rpc.Strict(normalizeChainID) }

// normalizeChainID compares chain IDs numerically, accepting equivalent hex
// quantity spellings rather than comparing JSON bytes.
func normalizeChainID(value json.RawMessage) (string, error) {
	var quantity string
	if err := json.Unmarshal(value, &quantity); err != nil {
		return "", fmt.Errorf("chain ID: expected hex quantity: %w", err)
	}
	if len(quantity) < 3 || quantity[:2] != "0x" {
		return "", fmt.Errorf("chain ID: invalid quantity %q", quantity)
	}
	digits := quantity[2:]
	if len(digits)%2 != 0 {
		digits = "0" + digits
	}
	bytes, err := hex.DecodeString(digits)
	if err != nil {
		return "", fmt.Errorf("chain ID: invalid quantity %q: %w", quantity, err)
	}
	return new(big.Int).SetBytes(bytes).String(), nil
}
