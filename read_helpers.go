package ethguard

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/internal/jsonsafe"
	"github.com/codermaybe/ethguard/rpc"
)

// callTyped is the typed facade's aggregate entry. The corroboration
// requirement rides the invocation (aggregator.AggregateStrict), never the
// context and never the request; Call stays the generic decision entry, and
// the two never share a requirement carrier. Forwarded mode and the
// pending-local route keep their documented exemptions.
func (c *Client) callTyped(ctx context.Context, method string, params ...any) (rpc.Decision, error) {
	return c.aggregator.AggregateStrict(ctx, rpc.Request{Method: method, Params: params})
}

// aggregateInto runs one corroborated read and decodes the resolved value
// into T. On an aggregation failure the public value is cleared while every
// other formed fact stays; a decode failure keeps the aggregated value — the
// failure came after the aggregation succeeded.
func aggregateInto[T any](ctx context.Context, c *Client, method string, params ...any) (*T, rpc.Decision, error) {
	decision, err := c.callTyped(ctx, method, params...)
	if err != nil {
		decision.Value = nil
		return nil, decision, err
	}
	var out T
	if err := jsonsafe.Decode(decision.Value, &out); err != nil {
		return nil, decision, fmt.Errorf("%w: decode %s: %w", rpc.ErrMalformedResponse, method, err)
	}
	return &out, decision, nil
}

// decisionOf snapshots the aggregator-produced decision onto the result.
// A local rejection before any adjudication constructs an empty CallResult
// directly; this helper is only called with an aggregator outcome, so the
// pointer always carries that outcome's facts.
func decisionOf(decision rpc.Decision) *rpc.Decision {
	return &decision
}

// readQuantity is the shared shape of the quantity reads: validate inside
// the operation, aggregate, project through the owning method's normalizer.
// The projection failure keeps the aggregated facts.
func readQuantity(ctx context.Context, c *Client, view bool, method string, params ...any) (*big.Int, CallResult, error) {
	return operation(c, ctx, view, func(opCtx context.Context) (*big.Int, CallResult, error) {
		return readQuantityExec(opCtx, c, method, params...)
	})
}

func readQuantityExec(ctx context.Context, c *Client, method string, params ...any) (*big.Int, CallResult, error) {
	decision, err := c.callTyped(ctx, method, params...)
	if err != nil {
		decision.Value = nil
		return nil, CallResult{Decision: decisionOf(decision)}, err
	}
	value, projectErr := c.decodedQuantity(method, decision)
	return value, CallResult{Decision: decisionOf(decision)}, projectErr
}

// decodedQuantity reduces one decision's canonical quantity spelling into a
// big.Int through the owning method's normalizer.
func (c *Client) decodedQuantity(method string, decision rpc.Decision) (*big.Int, error) {
	canonical, err := c.Normalize(method, decision.Value)
	if err != nil {
		return nil, fmt.Errorf("ethguard: decode %s: %w", method, err)
	}
	value, ok := new(big.Int).SetString(canonical, 10)
	if !ok {
		return nil, fmt.Errorf("ethguard: invalid %s %q", method, canonical)
	}
	return value, nil
}

// validateAddress re-validates one caller-supplied generated address value
// through the generated constructor and returns its canonical lowercase
// spelling. Directly constructed values can carry any string — including
// the empty one — so every entry validates before it builds a reference or
// contacts the network, and only the canonical value rides the wire. The
// empty string, a wrong prefix, wrong length, or non-hex text is rejected
// locally as ErrInvalidRequest; the explicit all-zero address is legal.
func validateAddress(v ethapi.Address) (ethapi.Address, error) {
	quoted, err := json.Marshal(string(v))
	if err != nil {
		return "", fmt.Errorf("ethguard: invalid address: %w", ErrInvalidRequest)
	}
	canon, err := ethapi.NewAddress(ethcodec.FoldHexSpelling(quoted))
	if err != nil {
		return "", fmt.Errorf("ethguard: invalid address %q: %w", string(v), ErrInvalidRequest)
	}
	return canon, nil
}

// validateHash is the hash-side twin of validateAddress: the generated
// constructor validates the explicitly case-folded 32-byte input, and no
// entry sends the raw spelling.
func validateHash(v ethapi.Hash) (ethapi.Hash, error) {
	quoted, err := json.Marshal(string(v))
	if err != nil {
		return "", fmt.Errorf("ethguard: invalid hash: %w", ErrInvalidRequest)
	}
	canon, err := ethapi.NewHash(ethcodec.FoldHexSpelling(quoted))
	if err != nil {
		return "", fmt.Errorf("ethguard: invalid hash %q: %w", string(v), ErrInvalidRequest)
	}
	return canon, nil
}

// validateAddressHash validates one address together with one storage-slot
// hash — the shared shape of every StorageAt entry. The slot keeps the
// fixed-32-byte Hash contract.
func validateAddressHash(account ethapi.Address, key ethapi.Hash) (ethapi.Address, ethapi.Hash, error) {
	account, err := validateAddress(account)
	if err != nil {
		return "", "", err
	}
	key, err = validateHash(key)
	if err != nil {
		return "", "", err
	}
	return account, key, nil
}

// hashBlockArg is the explicit EIP-1898 form used by the hash-specific
// go-ethereum methods. Keeping the false flag present is part of the wire
// contract: providers must see {blockHash, requireCanonical:false}. The
// hash must already be validated; callers pass the canonical spelling.
func hashBlockArg(blockHash ethapi.Hash) map[string]any {
	return map[string]any{
		"blockHash":        string(blockHash),
		"requireCanonical": false,
	}
}

// freezeBlockReference re-validates one caller-supplied generated reference
// through the generated constructor and returns the frozen canonical value.
// Directly constructed unions can select zero or several branches; the
// marshal-reparse cycle is the single acceptance gate before any wire
// spelling is derived.
func freezeBlockReference(blockRef ethapi.BlockReference) (ethapi.BlockReference, error) {
	raw, err := json.Marshal(blockRef)
	if err != nil {
		return ethapi.BlockReference{}, fmt.Errorf("ethguard: block reference: %w", ErrInvalidRequest)
	}
	frozen, err := ethapi.NewBlockReference(raw)
	if err != nil {
		return ethapi.BlockReference{}, fmt.Errorf("ethguard: block reference: %w", ErrInvalidRequest)
	}
	return frozen, nil
}

// isBareBlockHash reports whether one bare reference string is the
// 32-byte-word spelling: exactly "0x" plus 64 lowercase-or-uppercase hex
// characters. This is the overlap between the quantity and hash anyOf
// branches, resolved by shape, not by first-match parsing.
func isBareBlockHash(ref string) bool {
	if len(ref) != 2+2*common.HashLength || !strings.HasPrefix(ref, "0x") {
		return false
	}
	for _, r := range ref[2:] {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

// toBlockNumArg mirrors go-ethereum's conversion of a block number argument.
func toBlockNumArg(number *big.Int) string {
	if number == nil {
		return "latest"
	}
	if number.Sign() < 0 {
		// Int64 on a value outside int64 range truncates, which could fold an
		// out-of-range negative onto a known tag (-2..-5). Reject it here so
		// checkedBlockNumArg fails the request locally instead.
		if !number.IsInt64() {
			return ""
		}
		switch number.Int64() {
		case -1:
			return "pending"
		case -2:
			return "latest"
		case -3:
			return "finalized"
		case -4:
			return "safe"
		case -5:
			return "0x0"
		default:
			return ""
		}
	}
	return hexutil.EncodeBig(number)
}

func checkedBlockNumArg(number *big.Int) (string, error) {
	arg := toBlockNumArg(number)
	if arg == "" {
		return "", fmt.Errorf("ethguard: unsupported block number %s: %w", number.String(), ErrInvalidRequest)
	}
	return arg, nil
}
