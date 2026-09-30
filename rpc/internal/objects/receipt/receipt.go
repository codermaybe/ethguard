// Package receipt contains the Ethereum receipt object rules shared by RPC methods.
package receipt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"ethguard/rpc"
	"ethguard/rpc/internal/wire"
)

// Pruner removes fields that are outside the method's comparison shape.
type Pruner func(method string, raw json.RawMessage) (string, error)

func Admit(ctxReq rpc.Request, value json.RawMessage, pruner Pruner) error {
	_ = ctxReq
	if IsJSONNull(value) {
		return nil
	}
	if err := Validate(value); err != nil {
		return fmt.Errorf("%w: %v", rpc.ErrMalformedResponse, err)
	}
	return nil
}

func AdmitTransaction(req rpc.Request, value json.RawMessage) error {
	if IsJSONNull(value) {
		return nil
	}
	want, err := RequestedHash(req)
	if err != nil {
		return err
	}
	if err := Validate(value); err != nil {
		return fmt.Errorf("%w: %v", rpc.ErrMalformedResponse, err)
	}
	r, err := decodeReceipt(value)
	if err != nil || r == nil || r.TxHash != want {
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
		if err := Validate(row); err != nil {
			return fmt.Errorf("%w: receipt %d: %v", rpc.ErrMalformedResponse, i, err)
		}
		r, err := decodeReceipt(row)
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
	canonical, err := wire.NormalizeHexQuantity(json.RawMessage(fmt.Sprintf("%q", s)))
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
// whitelist prune and are not rejected here. A value with no canonical
// form errors. Required-field rejection belongs to Validate and Admit.
func Normalize(value json.RawMessage, prune Pruner, method string) (string, error) {
	if IsJSONNull(value) {
		return "null", nil
	}
	var shape map[string]json.RawMessage
	if json.Unmarshal(value, &shape) == nil && shape != nil && len(shape) > 1 && shape["blockHash"] == nil && shape["blockNumber"] == nil && shape["logsBloom"] == nil {
		return prune(method, value)
	}
	if len(shape) <= 1 {
		return "", fmt.Errorf("receipt: incomplete object")
	}
	return prune(method, value)
}

func Validate(value json.RawMessage) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(value, &obj); err != nil || obj == nil {
		return fmt.Errorf("receipt: expected object")
	}
	for _, f := range []string{"transactionHash", "transactionIndex", "blockHash", "blockNumber", "cumulativeGasUsed", "gasUsed", "logsBloom", "logs"} {
		if !PresentNonNull(obj[f]) {
			return fmt.Errorf("receipt: missing %s", f)
		}
	}
	for _, f := range []string{"transactionHash", "blockHash"} {
		if _, err := wire.ValidateWireFixedHex(obj[f], 64, f); err != nil {
			return fmt.Errorf("receipt: %s: %w", f, err)
		}
	}
	for _, f := range []string{"transactionIndex", "blockNumber", "cumulativeGasUsed", "gasUsed"} {
		if _, err := wire.NormalizeWireQuantity(obj[f]); err != nil {
			return fmt.Errorf("receipt: %s: %w", f, err)
		}
	}
	if _, err := wire.ValidateWireFixedHex(obj["logsBloom"], 512, "logsBloom"); err != nil {
		return fmt.Errorf("receipt: logsBloom: %w", err)
	}
	status, root := PresentNonNull(obj["status"]), PresentNonNull(obj["root"])
	if !status && !root {
		return fmt.Errorf("receipt: missing status or root")
	}
	if status {
		if _, err := wire.NormalizeWireQuantity(obj["status"]); err != nil {
			return fmt.Errorf("receipt: status: %w", err)
		}
	} else if _, err := wire.ValidateWireFixedHex(obj["root"], 64, "root"); err != nil {
		return fmt.Errorf("receipt: root: %w", err)
	}
	if s := strings.TrimSpace(string(obj["logs"])); len(s) == 0 || s[0] != '[' {
		return fmt.Errorf("receipt: logs must be an array")
	}
	r, err := decodeReceipt(value)
	if err != nil || r == nil {
		return fmt.Errorf("receipt: invalid payload")
	}
	if r.TxHash == (common.Hash{}) || r.BlockHash == (common.Hash{}) {
		return fmt.Errorf("receipt: missing transaction or block hash")
	}
	for i, l := range r.Logs {
		if l == nil {
			return fmt.Errorf("receipt: null log %d", i)
		}
		if l.TxHash != (common.Hash{}) && l.TxHash != r.TxHash {
			return fmt.Errorf("receipt: log %d transaction hash mismatch", i)
		}
		if l.BlockHash != (common.Hash{}) && l.BlockHash != r.BlockHash {
			return fmt.Errorf("receipt: log %d block hash mismatch", i)
		}
	}
	if r.Bloom != types.CreateBloom(r) {
		return fmt.Errorf("receipt: logsBloom disagrees with logs")
	}
	return nil
}

// decodeReceipt decodes JSON string escapes before geth's hex codecs see the
// temporary payload. UseNumber preserves numeric tokens exactly. This is
// semantic parsing for validation and binding; it does not project fields,
// canonicalize hex, or modify the source payload.
func decodeReceipt(value json.RawMessage) (*types.Receipt, error) {
	plain, err := receiptSemanticJSON(value)
	if err != nil {
		return nil, err
	}
	var r *types.Receipt
	if err := json.Unmarshal(plain, &r); err != nil {
		return nil, err
	}
	return r, nil
}

func receiptSemanticJSON(value json.RawMessage) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	return json.Marshal(decoded)
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

func IsJSONNull(value json.RawMessage) bool { return strings.TrimSpace(string(value)) == "null" }
func PresentNonNull(value json.RawMessage) bool {
	s := strings.TrimSpace(string(value))
	return len(s) > 0 && s != "null"
}
