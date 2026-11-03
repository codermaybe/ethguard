package rpc

// Block-reference spellings and tags shared by the plan model and the
// anchoring decorators: the moving-tag set, the tag-carrying context, and the
// rule spellings. The per-request rewrite mechanics live with their owners
// (read_plan.go for the plan model, anchor_decorators.go for the legacy
// decorator family).

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
)

// AnchorFunc establishes a corroborated, immutable block reference. It is called
// only when a moving tag actually needs rewriting.
type AnchorFunc func(context.Context) (string, error)

// AnchorHeightFunc establishes a corroborated block height for methods whose
// wire form accepts a height but not a hash — eth_feeHistory's newestBlock
// is a BlockNumber, not a BlockNumberOrHash, so a hash cannot ride the wire
// (the soak caught the same constraint for eth_getBlockByNumber's number
// position). Both built-in resolvers derive the height from the same
// establishment response; no extra fan-out is required.
type AnchorHeightFunc func(context.Context) (uint64, error)

// movingTags is the anchor-rewrite set: latest, safe, and finalized. It is
// not the spelling authority for every block reference in the repo. The
// other spellings stay local on purpose:
//
//   - methods.validateEIP1898Object / isMovingTag: same three tags are
//     anchored; pending and earliest pass through on EIP-1898 objects.
//   - methods.pendingTag: pending only, including case, JSON, and
//     {blockNumber: pending}. RejectPending uses pendingReference below,
//     which recognizes that same pending shape.
//   - ethadapter.toBlockNumArg / blockRefArg: Geth negative numbers
//     (-1 pending, -2 latest, -3 finalized, -4 safe, -5 earliest as 0x0).
//     Typed BlockByNumber(-1) is rejected in blockByNegativeNumber and
//     never reaches this set.
//
// Do not fold these into one cross-package table: pending is not a moving
// tag, and earliest is an explicit height for some entry points.
var movingTags = map[string]bool{
	"latest":    true,
	"safe":      true,
	"finalized": true,
}

type anchorTagContextKey struct{}

func WithAnchorTag(ctx context.Context, tag string) context.Context {
	return context.WithValue(ctx, anchorTagContextKey{}, tag)
}
func AnchorTag(ctx context.Context) string {
	tag, _ := ctx.Value(anchorTagContextKey{}).(string)
	return tag
}

// singleMovingTag reports the one moving tag present at the positions, or
// that no rewrite is needed. Two different tags make the request invalid;
// the declaration stays silent so Prepare owns the rejection.
func singleMovingTag(req Request, positions []int) (string, bool) {
	tag := ""
	for _, pos := range positions {
		if pos < 0 || pos >= len(req.Params) {
			continue
		}
		value, ok := req.Params[pos].(string)
		if !ok || !movingTags[strings.ToLower(value)] {
			continue
		}
		lowered := strings.ToLower(value)
		if tag != "" && tag != lowered {
			return "", false
		}
		tag = lowered
	}
	return tag, tag != ""
}

// pendingReference reports pending spellings at one block-reference position.
// It matches methods.pendingTag: case-insensitive strings, JSON strings, and
// EIP-1898 objects whose blockNumber is pending. A blockHash object is not
// pending.
func pendingReference(value any) bool {
	switch typed := value.(type) {
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "pending")
	case json.RawMessage:
		var decoded any
		if err := json.Unmarshal(bytes.TrimSpace(typed), &decoded); err != nil {
			return false
		}
		return pendingReference(decoded)
	case map[string]any:
		number, ok := typed["blockNumber"]
		return ok && pendingReference(number)
	default:
		return false
	}
}

func anchorRule(kind string, positions []int) string {
	if len(positions) == 0 {
		return kind
	}
	parts := make([]string, len(positions))
	for i, pos := range positions {
		parts[i] = strconv.Itoa(pos)
	}
	return kind + "@" + strings.Join(parts, ",")
}

func anchorFieldsRule(pos int, fields []string) string {
	return "height@" + strconv.Itoa(pos) + "." + strings.Join(fields, ",")
}
