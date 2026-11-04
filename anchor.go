package ethguard

import (
	"context"

	"github.com/codermaybe/ethguard/internal/runtime"
	"github.com/codermaybe/ethguard/rpc"
)

// The anchor facade is query-only: a Client owns its runtime-built resolver,
// and callers may ask it for established references but cannot inject or
// replace one. The establishment algorithms live in internal/runtime; the
// option below tunes the preset the composition root installs.

type FinalizedAnchorOption = runtime.FinalizedAnchorOption

// WithMaxFinalizedLag bounds lag behind the lower-median reported height.
func WithMaxFinalizedLag(blocks uint64) FinalizedAnchorOption {
	return runtime.WithMaxFinalizedLag(blocks)
}

// resolveReference establishes the moving reference for this call.
// Freshness is invocation-local (decision 0026): this function stores
// nothing for a later call, and it applies no TTL. Moving references are
// established per call, with no reuse and no TTL.
//
// ObservedAt is the establishment time. Tag-aware and evidence resolvers set
// it, including on failure.
//
// Failure stays inside Prepare: the anchored policy returns a wrapped anchor
// error and the aggregator does not fan out the business method. The
// published evidence keeps Status Unknown and the resolver's Faults.
func resolveReference(ctx context.Context, resolver runtime.ReferenceResolver, tag string) (rpc.ReferenceEvidence, error) {
	if tag != "" && tag != "latest" {
		return resolver.AnchorReferenceForTag(ctx, tag)
	}
	return resolver.AnchorReference(ctx)
}
