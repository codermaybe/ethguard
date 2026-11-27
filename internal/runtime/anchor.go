package runtime

import (
	"context"

	"github.com/codermaybe/ethguard/rpc"
)

// The reference resolver facades. The establishment algorithms live in the
// services (strict_head.go, reference_service.go); Compose constructs these
// targets for the client's reference queries.

// ReferenceResolver is the runtime's established-reference seam: what a
// composed client hands to its anchored reads.
type ReferenceResolver interface {
	AnchorBlockHash(context.Context) (string, error)
	AnchorBlockRef(context.Context) (string, uint64, error)
	AnchorReference(context.Context) (rpc.ReferenceEvidence, error)
	AnchorReferenceForTag(context.Context, string) (rpc.ReferenceEvidence, error)
}

// headService is what an anchor facade delegates to: one preset's
// establishment service.
type headService interface {
	Reference(ctx context.Context) (rpc.ReferenceEvidence, error)
	ReferenceForTag(ctx context.Context, tag string) (rpc.ReferenceEvidence, error)
}

// anchorFacade is the shared forwarding body of both presets: the service
// owns the establishment algorithm, the facade only exposes the seam.
type anchorFacade struct {
	service headService
}

func (f anchorFacade) AnchorReference(ctx context.Context) (rpc.ReferenceEvidence, error) {
	return f.service.Reference(ctx)
}

func (f anchorFacade) AnchorReferenceForTag(ctx context.Context, tag string) (rpc.ReferenceEvidence, error) {
	return f.service.ReferenceForTag(ctx, tag)
}

func (f anchorFacade) AnchorBlockHash(ctx context.Context) (string, error) {
	r, e := f.service.Reference(ctx)
	return r.Hash, e
}

func (f anchorFacade) AnchorBlockRef(ctx context.Context) (string, uint64, error) {
	r, e := f.service.Reference(ctx)
	return r.Hash, r.Height, e
}

// StrictHeadAnchor requires corroborated strict latest-head agreement. The
// establishment algorithm is runtime-owned; the type stays as the public
// facade of that service.
type StrictHeadAnchor struct {
	anchorFacade
}

// FinalizedHeadAnchor establishes a common reference from provider-reported
// finalized views.
type FinalizedHeadAnchor struct {
	anchorFacade
}

// FinalizedAnchorOption tunes the finalized preset's establishment.
type FinalizedAnchorOption func(*FinalizedAnchorConfig)

// FinalizedAnchorConfig carries the finalized preset's knobs.
type FinalizedAnchorConfig struct{ MaxLag uint64 }

// DefaultMaxFinalizedLag is the documented lag bound of the finalized preset.
const DefaultMaxFinalizedLag = 32

// WithMaxFinalizedLag bounds lag behind the lower-median reported height.
func WithMaxFinalizedLag(blocks uint64) FinalizedAnchorOption {
	return func(c *FinalizedAnchorConfig) { c.MaxLag = blocks }
}

// FinalizedAnchorLag extracts the lag bound an option list selects.
func FinalizedAnchorLag(opts ...FinalizedAnchorOption) uint64 {
	cfg := FinalizedAnchorConfig{MaxLag: DefaultMaxFinalizedLag}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg.MaxLag
}
