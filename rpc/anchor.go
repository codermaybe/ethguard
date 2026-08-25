package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// AnchorFunc establishes a verified, immutable block reference. It is called
// only when a moving tag actually needs rewriting.
type AnchorFunc func(context.Context) (string, error)

var movingTags = map[string]bool{
	"latest":    true,
	"safe":      true,
	"finalized": true,
}

type anchored struct {
	strictFunc
	tagPos int
	anchor AnchorFunc
}

// Anchored binds a normalizer to strict resolution and rewrites a moving
// block tag at position tagPos to a verified block hash before fan-out.
//
// Sources at different heads answer differently to "latest"; asking them all
// for one agreed hash makes disagreement meaningful evidence instead of
// inevitable noise. Explicit numbers and hashes pass through untouched — the
// caller has already pinned the reference. "pending" also passes through:
// anchoring an unfinalized tag would silently change its meaning.
func Anchored(tagPos int, normalize func(json.RawMessage) (string, error), anchor AnchorFunc) Policy {
	return anchored{
		strictFunc: strictFunc{normalize: normalize},
		tagPos:     tagPos,
		anchor:     anchor,
	}
}

func (a anchored) Prepare(ctx context.Context, req Request) (Request, error) {
	if a.tagPos >= len(req.Params) {
		return req, nil // nothing to rewrite; let the upstream reject malformed params
	}
	tag, ok := req.Params[a.tagPos].(string)
	if !ok || !movingTags[strings.ToLower(tag)] {
		return req, nil
	}
	hash, err := a.anchor(ctx)
	if err != nil {
		return Request{}, fmt.Errorf("anchor: %w", err)
	}
	params := append([]any(nil), req.Params...)
	params[a.tagPos] = hash
	return Request{Method: req.Method, Params: params}, nil
}
