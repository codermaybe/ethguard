// Package receipt contains the Ethereum receipt object rules shared by RPC methods.
package receipt

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"

	"github.com/codermaybe/ethguard/internal/ethcodec"
	objectreceipt "github.com/codermaybe/ethguard/internal/ethobject/receipt"
	"github.com/codermaybe/ethguard/rpc"
)

// Pruner removes fields that are outside the method's comparison shape.
type Pruner func(method string, raw json.RawMessage) (string, error)

// PrepareTransaction runs the original AdmitTransaction order once and hands
// out the canonical bytes it validated, so a caller that also commits those
// bytes never encodes the response a second time. The fixed order: a JSON
// null short-circuits to the canonical "null" before any request parsing;
// the requested hash is parsed next so a request error keeps its class and
// precedence over any payload verdict; DecodeWithCanonical then performs the
// complete wire validation (encoder, private geth decode, nil and zero-hash
// checks) and returns the already-validated canonical bytes; finally the
// decoded receipt's transaction hash must match the request. Errors keep the
// original admission texts and wrapping exactly.
func PrepareTransaction(req rpc.Request, value json.RawMessage) (json.RawMessage, error) {
	if IsJSONNull(value) {
		return json.RawMessage("null"), nil
	}
	want, err := RequestedHash(req)
	if err != nil {
		return nil, err
	}
	canonical, r, err := objectreceipt.DecodeWithCanonical(value)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", rpc.ErrMalformedResponse, err)
	}
	if r == nil || r.TxHash != want {
		return nil, fmt.Errorf("%w: receipt transaction hash does not match requested hash", rpc.ErrMalformedResponse)
	}
	return canonical, nil
}

func AdmitTransaction(req rpc.Request, value json.RawMessage) error {
	// The admission entry needs only the verdict; the canonical bytes it
	// prepared are discarded here. The canonical processing entry hands the
	// same bytes to the result instead of re-encoding.
	_, err := PrepareTransaction(req, value)
	return err
}

// AdmitBlock checks each receipt against the requested block. A JSON null
// means the block itself is absent. An empty array is a legal empty block,
// not a missing response. This is per-response integrity against the
// request, not a completeness claim over every receipt that could exist.
func AdmitBlock(req rpc.Request, value json.RawMessage) error {
	_, err := prepareBlockRows(req, value)
	return err
}

// PrepareBlock retains the canonical bytes already checked by block admission.
// It keeps the original order: null, array shape, request, then each receipt's
// complete validation and binding before visiting the next receipt. Like
// AdmitBlock, this controlled method entry relies on the coordinator's prior
// RejectUnsafeJSON check for the whole list's byte and nesting bounds; it does
// not replace that safety stage with per-row checks.
func PrepareBlock(req rpc.Request, value json.RawMessage) (json.RawMessage, error) {
	rows, err := prepareBlockRows(req, value)
	if err != nil {
		return nil, err
	}
	return ethcodec.MarshalNoEscape(rows)
}

// prepareBlockRows reuses the decoded row slice for the checked encodings. A
// nil slice denotes null; an empty non-nil slice denotes []. Admission can
// discard this result without paying for a complete array marshal.
func prepareBlockRows(req rpc.Request, value json.RawMessage) ([]json.RawMessage, error) {
	if IsJSONNull(value) {
		return nil, nil
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(value, &rows); err != nil || rows == nil {
		return nil, fmt.Errorf("%w: block receipts must be an array", rpc.ErrMalformedResponse)
	}
	hash, height, err := RequestedBlockRef(req)
	if err != nil {
		return nil, err
	}
	for i, row := range rows {
		canonical, r, err := objectreceipt.DecodeWithCanonical(row)
		if err != nil || r == nil {
			return nil, fmt.Errorf("%w: receipt %d: %v", rpc.ErrMalformedResponse, i, err)
		}
		if hash != nil && r.BlockHash != *hash {
			return nil, fmt.Errorf("%w: receipt %d block hash does not match requested hash", rpc.ErrMalformedResponse, i)
		}
		if height != nil && (r.BlockNumber == nil || r.BlockNumber.Cmp(height) != 0) {
			return nil, fmt.Errorf("%w: receipt %d block number does not match requested number", rpc.ErrMalformedResponse, i)
		}
		rows[i] = canonical
	}
	return rows, nil
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
// receipt decoding and the Admit functions.
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
