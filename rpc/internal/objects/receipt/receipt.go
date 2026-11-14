// Package receipt contains the Ethereum receipt object rules shared by RPC methods.
package receipt

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/rpc"
)

// Pruner removes fields that are outside the method's comparison shape.
type Pruner func(method string, raw json.RawMessage) (string, error)

func AdmitTransaction(req rpc.Request, value json.RawMessage) error {
	if IsJSONNull(value) {
		return nil
	}
	want, err := RequestedHash(req)
	if err != nil {
		return err
	}
	r, err := validateAndDecode(value)
	if err != nil {
		return fmt.Errorf("%w: %v", rpc.ErrMalformedResponse, err)
	}
	if r == nil || r.TxHash != want {
		return fmt.Errorf("%w: receipt transaction hash does not match requested hash", rpc.ErrMalformedResponse)
	}
	return nil
}

// AdmitBlock checks each receipt against the requested block. A JSON null
// means the block itself is absent. An empty array is a legal empty block,
// not a missing response. This is per-response integrity against the
// request, not a completeness claim over every receipt that could exist.
func AdmitBlock(req rpc.Request, value json.RawMessage) error {
	if IsJSONNull(value) {
		return nil
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(value, &rows); err != nil || rows == nil {
		return fmt.Errorf("%w: block receipts must be an array", rpc.ErrMalformedResponse)
	}
	hash, height, err := RequestedBlockRef(req)
	if err != nil {
		return err
	}
	for i, row := range rows {
		r, err := validateAndDecode(row)
		if err != nil || r == nil {
			return fmt.Errorf("%w: receipt %d: %v", rpc.ErrMalformedResponse, i, err)
		}
		if hash != nil && r.BlockHash != *hash {
			return fmt.Errorf("%w: receipt %d block hash does not match requested hash", rpc.ErrMalformedResponse, i)
		}
		if height != nil && (r.BlockNumber == nil || r.BlockNumber.Cmp(height) != 0) {
			return fmt.Errorf("%w: receipt %d block number does not match requested number", rpc.ErrMalformedResponse, i)
		}
	}
	return nil
}

func RequestedHash(req rpc.Request) (common.Hash, error) {
	if len(req.Params) != 1 {
		return common.Hash{}, fmt.Errorf("%w: expected one transaction hash", rpc.ErrInvalidRequest)
	}
	s, ok := req.Params[0].(string)
	if !ok || !common.IsHexHash(s) {
		return common.Hash{}, fmt.Errorf("%w: transaction hash must be a 32-byte hex string", rpc.ErrInvalidRequest)
	}
	return common.HexToHash(s), nil
}

func RequestedBlockRef(req rpc.Request) (*common.Hash, *big.Int, error) {
	if len(req.Params) != 1 {
		return nil, nil, fmt.Errorf("%w: expected one block reference", rpc.ErrInvalidRequest)
	}
	ref := req.Params[0]
	if obj, ok := ref.(map[string]any); ok {
		if raw, ok := obj["blockHash"]; ok {
			if s, ok := raw.(string); ok && common.IsHexHash(s) {
				h := common.HexToHash(s)
				return &h, nil, nil
			}
			if h, ok := raw.(common.Hash); ok {
				return &h, nil, nil
			}
			return nil, nil, fmt.Errorf("%w: invalid block hash", rpc.ErrInvalidRequest)
		}
		if raw, ok := obj["blockNumber"]; ok {
			return ParseBlockHeight(raw)
		}
		return nil, nil, fmt.Errorf("%w: block reference object has no hash or number", rpc.ErrInvalidRequest)
	}
	s, ok := ref.(string)
	if !ok {
		return nil, nil, fmt.Errorf("%w: block reference must be a string or object", rpc.ErrInvalidRequest)
	}
	if common.IsHexHash(s) {
		h := common.HexToHash(s)
		return &h, nil, nil
	}
	if strings.EqualFold(s, "latest") || strings.EqualFold(s, "safe") || strings.EqualFold(s, "finalized") {
		return nil, nil, fmt.Errorf("%w: moving block reference was not pinned before admission", rpc.ErrInvalidRequest)
	}
	return ParseBlockHeight(s)
}

func ParseBlockHeight(value any) (*common.Hash, *big.Int, error) {
	s, ok := value.(string)
	if !ok {
		return nil, nil, fmt.Errorf("%w: block number must be a hex quantity", rpc.ErrInvalidRequest)
	}
	canonical, err := ethcodec.NormalizeHexQuantity(json.RawMessage(fmt.Sprintf("%q", s)))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: invalid block number: %v", rpc.ErrInvalidRequest, err)
	}
	n, ok := new(big.Int).SetString(canonical, 10)
	if !ok {
		return nil, nil, fmt.Errorf("%w: invalid block number", rpc.ErrInvalidRequest)
	}
	return nil, n, nil
}

// Normalize chooses the comparison key. Vendor fields disappear in the
// whitelist prune and are not rejected here. Any non-object payload and an
// object carrying at most one field are incomplete answers, not votes. A
// value with no canonical form errors. Required-field rejection belongs to
// validateAndDecode and the Admit functions.
func Normalize(value json.RawMessage, prune Pruner, method string) (string, error) {
	if IsJSONNull(value) {
		return "null", nil
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(value, &shape); err != nil || shape == nil || len(shape) <= 1 {
		return "", fmt.Errorf("receipt: incomplete object")
	}
	return prune(method, value)
}

// validateAndDecode runs every wire check and returns the decoded receipt, so
// a caller that must bind the receipt to its request does not decode the same
// bytes a second time.
func validateAndDecode(value json.RawMessage) (*types.Receipt, error) {
	canonical, err := Encode(value)
	if err != nil {
		return nil, err
	}
	var r *types.Receipt
	if err := json.Unmarshal(canonical, &r); err != nil || r == nil {
		return nil, fmt.Errorf("receipt: invalid payload")
	}
	if r.TxHash == (common.Hash{}) || r.BlockHash == (common.Hash{}) {
		return nil, fmt.Errorf("receipt: missing transaction or block hash")
	}
	return r, nil
}

func NormalizeBlock(value json.RawMessage, prune Pruner, method string) (string, error) {
	if IsJSONNull(value) {
		return "null", nil
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(value, &rows); err != nil || rows == nil {
		return "", fmt.Errorf("block receipts: expected array")
	}
	for i, row := range rows {
		if IsJSONNull(row) {
			return "", fmt.Errorf("block receipts[%d]: null receipt", i)
		}
	}
	return prune(method, value)
}

// IsJSONNull forwards to the shared wire spelling so every
// object package answers the null question identically.
func IsJSONNull(value json.RawMessage) bool { return ethcodec.IsJSONNull(value) }
