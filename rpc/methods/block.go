package methods

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	blockobject "github.com/codermaybe/ethguard/internal/ethobject/block"
	"github.com/codermaybe/ethguard/rpc"
)

// admitBlockResponse validates a full-block response for the method's plan.
// The method supplies hash-versus-number binding explicitly.
func admitBlockResponse(ctx context.Context, req rpc.Request, value json.RawMessage, byHash bool, call rpc.AdmissionCaller) (json.RawMessage, error) {
	if string(value) == "null" {
		return nil, nil
	}
	fullTx := len(req.Params) > 1 && isTrueParam(req.Params[1])
	if !fullTx {
		return nil, nil
	}
	full, err := blockobject.AdmitBlock(ctx, req, value, byHash, call)
	if err != nil {
		return nil, err
	}
	return full.UncleEvidence(), nil
}

// isTrueParam recognizes the fullTx flag in the spellings a generic Call
// can carry: the bool literal and its JSON encoding. A wrapper type carrying
// JSON true must enable the full-block admission exactly like the literal,
// or a whole-block response would skip its cryptographic checks by spelling.
func isTrueParam(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case json.RawMessage:
		return string(bytes.TrimSpace(typed)) == "true"
	default:
		return false
	}
}

// NormalizeBlockHead exposes the block identity contract to the reference
// head codec. Reference reads and standalone Normalize calls share this
// extraction; production block comparison and delivery use complete values.
func NormalizeBlockHead(value json.RawMessage) (string, error) {
	return normalizeBlockIdentity(value)
}

// normalizeBlockIdentity extracts the number/hash identity for standalone
// Normalize and reference consumers. It ignores other fields and rejects a
// missing number or hash. Header/body validation and request binding belong to
// their encoding and admission paths. Both production block specs compare and
// deliver complete canonical values through WithCanonicalValue.
//
// The identity is "<decimal number>|<lowercase hash>". Parsing the height
// numerically makes equivalent hexadecimal spellings (for example 0x010f and
// 0x10f) agree. The height is a uint64-shaped quantity, so the shared
// wire-quantity primitive owns the spelling contract.
func normalizeBlockIdentity(value json.RawMessage) (string, error) {
	if string(value) == "null" {
		return "null", nil
	}
	var head struct {
		Number string `json:"number"`
		Hash   string `json:"hash"`
	}
	if err := json.Unmarshal(value, &head); err != nil {
		return "", err
	}
	if head.Number == "" || len(head.Hash) < 3 {
		return "", fmt.Errorf("block head: missing number or hash")
	}
	height, err := rpc.ParseHexQuantity(head.Number)
	if err != nil {
		return "", fmt.Errorf("block head: %w", err)
	}
	return strconv.FormatUint(height, 10) + "|" + strings.ToLower(head.Hash), nil
}
