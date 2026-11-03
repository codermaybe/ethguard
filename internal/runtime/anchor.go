package runtime

// The reference resolver facades. The establishment algorithms live in the
// services (strict_head.go, reference_service.go); these types are the public
// API's forwarding targets and the composition root's resolver products. The
// root facade aliases them, so external code keeps constructing them through
// the ethguard package.

import (
	"context"

	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
)

// ReferenceResolver is the runtime's established-reference seam: what a
// composed client hands to its anchored reads. The root facade re-exports it
// under the same name.
type ReferenceResolver interface {
	AnchorBlockHash(context.Context) (string, error)
	AnchorBlockRef(context.Context) (string, uint64, error)
	AnchorReference(context.Context) (rpc.ReferenceEvidence, error)
	AnchorReferenceForTag(context.Context, string) (rpc.ReferenceEvidence, error)
}

// StrictHeadAnchor requires corroborated strict latest-head agreement. The
// establishment algorithm is runtime-owned; the type stays as the public
// facade of that service.
type StrictHeadAnchor struct{ service *StrictHeadService }

// NewStrictHeadAnchor builds the default latest-head resolver: its own
// stage, the strict-head strategy, and no shared snapshot (each round
// adjudicates at the establishment's own thresholds).
func NewStrictHeadAnchor(p *pool.Pool, rec observe.Recorder, opts ...rpc.StrictOption) *StrictHeadAnchor {
	stage := NewReferenceStage(ReferenceStageConfig{
		Pool:          p,
		Recorder:      rec,
		StrictOptions: opts,
	})
	return &StrictHeadAnchor{service: NewStrictHeadService(stage, nil)}
}

func (a *StrictHeadAnchor) AnchorReference(ctx context.Context) (rpc.ReferenceEvidence, error) {
	return a.service.Reference(ctx)
}

func (a *StrictHeadAnchor) AnchorReferenceForTag(ctx context.Context, tag string) (rpc.ReferenceEvidence, error) {
	return a.service.ReferenceForTag(ctx, tag)
}

func (a *StrictHeadAnchor) AnchorBlockHash(ctx context.Context) (string, error) {
	r, e := a.AnchorReference(ctx)
	return r.Hash, e
}

func (a *StrictHeadAnchor) AnchorBlockRef(ctx context.Context) (string, uint64, error) {
	r, e := a.AnchorReference(ctx)
	return r.Hash, r.Height, e
}

// FinalizedHeadAnchor establishes a common reference from provider-reported
// finalized views.
type FinalizedHeadAnchor struct {
	service *FinalizedHeadService
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

// NewFinalizedHeadAnchor builds a common finalized-reference resolver: its
// own stage with the finalized strategy and the selected lag bound.
func NewFinalizedHeadAnchor(p *pool.Pool, rec observe.Recorder, opts ...FinalizedAnchorOption) *FinalizedHeadAnchor {
	stage := NewReferenceStage(ReferenceStageConfig{
		Pool:              p,
		Recorder:          rec,
		FinalizedStrategy: true,
		MaxLag:            FinalizedAnchorLag(opts...),
	})
	return &FinalizedHeadAnchor{service: NewFinalizedHeadService(stage)}
}

func (a *FinalizedHeadAnchor) AnchorReference(ctx context.Context) (rpc.ReferenceEvidence, error) {
	return a.service.Reference(ctx)
}

func (a *FinalizedHeadAnchor) AnchorReferenceForTag(ctx context.Context, tag string) (rpc.ReferenceEvidence, error) {
	return a.service.ReferenceForTag(ctx, tag)
}

func (a *FinalizedHeadAnchor) AnchorBlockHash(ctx context.Context) (string, error) {
	r, e := a.AnchorReference(ctx)
	return r.Hash, e
}

func (a *FinalizedHeadAnchor) AnchorBlockRef(ctx context.Context) (string, uint64, error) {
	r, e := a.AnchorReference(ctx)
	return r.Hash, r.Height, e
}
