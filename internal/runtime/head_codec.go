package runtime

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/codermaybe/ethguard/internal/reference"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

// MethodHeadCodec binds the block method's head contract to the reference
// seam. The composition root owns the binding, so internal/reference decodes
// heads through this interface without importing method policies: validation
// and the comparison key stay the block method's own contract, and the
// height/hash extraction is the codec's.
type MethodHeadCodec struct{}

// Decode validates one raw block-head response and extracts its identity.
// A response with no comparable height/hash is a decode error, never a zero
// view; a null response passes method validation but carries no height, so
// it decodes as malformed for reference purposes.
func (MethodHeadCodec) Decode(raw json.RawMessage) (reference.HeadView, error) {
	if _, err := methods.BlockByNumberPolicy().Normalize(raw); err != nil {
		return reference.HeadView{}, err
	}
	var head struct {
		Number string `json:"number"`
		Hash   string `json:"hash"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return reference.HeadView{}, err
	}
	height, err := rpc.ParseHexQuantity(head.Number)
	if err != nil {
		return reference.HeadView{}, err
	}
	return reference.HeadView{Height: height, Hash: strings.ToLower(head.Hash), Raw: raw}, nil
}

// Normalize reduces a raw head response to the comparison key head clustering
// agrees on — the same key the block method clusters on at the business
// boundary.
func (MethodHeadCodec) Normalize(raw json.RawMessage) (string, error) {
	return methods.BlockByNumberPolicy().Normalize(raw)
}

var _ reference.HeadCodec = MethodHeadCodec{}

// winningHeadOf extracts the corroborated reference identity from a decision
// value, applying the same codec contract.
func winningHeadOf(raw json.RawMessage) (reference.HeadView, error) {
	view, err := MethodHeadCodec{}.Decode(raw)
	if err != nil {
		return reference.HeadView{}, fmt.Errorf("winning reference: %w", rpc.ErrMalformedResponse)
	}
	return view, nil
}
