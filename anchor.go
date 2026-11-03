package ethguard

import (
	"context"
	"fmt"
	"time"

	"github.com/codermaybe/ethguard/internal/runtime"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
)

// ReferenceResolver establishes a corroborated block reference for moving-state reads.
type ReferenceResolver = runtime.ReferenceResolver

// Backward-compatible interface aliases.
type AnchorResolver interface {
	AnchorBlockHash(context.Context) (string, error)
}

type AnchorHeightProvider interface {
	AnchorBlockRef(context.Context) (string, uint64, error)
}

type AnchorTagProvider interface {
	AnchorReferenceForTag(context.Context, string) (rpc.ReferenceEvidence, error)
}

type AnchorEvidenceProvider interface {
	AnchorReference(context.Context) (rpc.ReferenceEvidence, error)
}

// StrictHeadAnchor and FinalizedHeadAnchor are the runtime's resolver
// facades; the establishment algorithms and their composition live in
// internal/runtime. The aliases keep the public construction surface stable.
type StrictHeadAnchor = runtime.StrictHeadAnchor

// NewStrictHeadAnchor builds the default latest-head resolver.
func NewStrictHeadAnchor(p *pool.Pool, rec observe.Recorder, opts ...rpc.StrictOption) *StrictHeadAnchor {
	return runtime.NewStrictHeadAnchor(p, rec, opts...)
}

type FinalizedHeadAnchor = runtime.FinalizedHeadAnchor

type FinalizedAnchorOption = runtime.FinalizedAnchorOption

// WithMaxFinalizedLag bounds lag behind the lower-median reported height.
func WithMaxFinalizedLag(blocks uint64) FinalizedAnchorOption {
	return runtime.WithMaxFinalizedLag(blocks)
}

// NewFinalizedHeadAnchor builds a common finalized-reference resolver.
func NewFinalizedHeadAnchor(p *pool.Pool, rec observe.Recorder, opts ...FinalizedAnchorOption) *FinalizedHeadAnchor {
	return runtime.NewFinalizedHeadAnchor(p, rec, opts...)
}

// resolveReference establishes the moving reference for this call.
// Freshness is invocation-local (decision 0026): this function stores
// nothing for a later call, and it applies no TTL. Moving references are
// established per call, with no reuse and no TTL.
//
// ObservedAt is the establishment time. Tag-aware and strict resolvers
// set it, including on failure; the hash-only branch below sets it before
// the resolver call and clears Status to Unknown if that call fails.
//
// Failure stays inside Prepare. anchorHashOf and anchorHeightOf publish
// the evidence, then the anchored policy returns a wrapped anchor error
// and the aggregator does not fan out the business method. The published
// evidence keeps Status Unknown and the resolver's Faults.
func resolveReference(ctx context.Context, resolver AnchorResolver, tag string) (rpc.ReferenceEvidence, error) {
	if tag != "" && tag != "latest" {
		if provider, ok := resolver.(AnchorTagProvider); ok {
			return provider.AnchorReferenceForTag(ctx, tag)
		}
		return rpc.ReferenceEvidence{Tag: tag, Status: rpc.Unknown}, fmt.Errorf("anchor: resolver %T cannot establish tag %q: %w", resolver, tag, rpc.ErrInvalidRequest)
	}
	if p, ok := resolver.(AnchorEvidenceProvider); ok {
		return p.AnchorReference(ctx)
	}
	ref := rpc.ReferenceEvidence{Status: rpc.Selected, Origin: "custom", ObservedAt: time.Now()}
	var err error
	if p, ok := resolver.(AnchorHeightProvider); ok {
		ref.Hash, ref.Height, err = p.AnchorBlockRef(ctx)
	} else {
		ref.Hash, err = resolver.AnchorBlockHash(ctx)
	}
	if err != nil {
		ref.Status = rpc.Unknown
	}
	return ref, err
}
