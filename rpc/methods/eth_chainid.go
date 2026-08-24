package methods

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"

	"ethguard/rpc"
)

// ChainIDPolicy compares chain IDs numerically, accepting equivalent hex
// quantity spellings rather than comparing JSON bytes.
type ChainIDPolicy struct{}

func (ChainIDPolicy) Prepare(_ context.Context, req rpc.Request) (rpc.Request, error) {
	return req, nil
}

func (ChainIDPolicy) Normalize(value json.RawMessage) (string, error) {
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

func (p ChainIDPolicy) Resolve(obs []rpc.Observation) (rpc.Decision, error) {
	return rpc.ResolveStrict(p, obs)
}
