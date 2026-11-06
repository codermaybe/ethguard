package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/codermaybe/ethguard/internal/refcontext"
	"github.com/codermaybe/ethguard/internal/uncertain"
	"github.com/codermaybe/ethguard/rpc"
)

// anchorMethodName is the internal registry key for the strict head read. It
// is what observability events name as the establishment call's method.
const anchorMethodName = "anchor"

// headAnchorPolicy is the strict head policy for reference establishment: it
// preserves the winning source's spelling in the decision value (the anchor
// evidence keeps the source's own wire form; the request path lowercases the
// derived hash separately), and requires a corroborated result.
type headAnchorPolicy struct{ rpc.Policy }

func (p headAnchorPolicy) Resolve(obs []rpc.Observation) (rpc.Decision, error) {
	d, err := p.Policy.Resolve(obs)
	d = restoreAnchorSourceValue(d, obs)
	return p.requireCorroborated(d, err)
}

func (p headAnchorPolicy) ResolveCorroborated(c rpc.Corroboration, obs []rpc.Observation) (rpc.Decision, error) {
	var (
		d   rpc.Decision
		err error
	)
	if cp, ok := p.Policy.(rpc.CorroboratingPolicy); ok {
		d, err = cp.ResolveCorroborated(c, obs)
	} else {
		d, err = p.Policy.Resolve(obs)
	}
	d = restoreAnchorSourceValue(d, obs)
	return p.requireCorroborated(d, err)
}

// Anchor comparison uses the canonical head encoder, but the anchor decision
// is an internal reference observation and must preserve the winning source's
// wire spelling. The request path lowercases the derived hash separately.
func restoreAnchorSourceValue(d rpc.Decision, obs []rpc.Observation) rpc.Decision {
	if d.Status != rpc.Corroborated || d.Representative.ID == "" {
		return d
	}
	for _, item := range obs {
		if item.Source.ID == d.Representative.ID {
			d.Value = append(json.RawMessage(nil), item.Value...)
			break
		}
	}
	return d
}

func (p headAnchorPolicy) requireCorroborated(d rpc.Decision, err error) (rpc.Decision, error) {
	if err == nil && d.Status != rpc.Corroborated {
		d.Status = rpc.Unknown
		err = fmt.Errorf("uncorroborated anchor: %w", rpc.ErrConflict)
	}
	return d, err
}

func blockRefFromDecision(d rpc.Decision) (string, uint64, error) {
	var head struct {
		Number string `json:"number"`
		Hash   string `json:"hash"`
	}
	if err := json.Unmarshal(d.Value, &head); err != nil || len(head.Hash) < 3 {
		return "", 0, fmt.Errorf("winning anchor carries no hash: %w", rpc.ErrMalformedResponse)
	}
	height, err := rpc.ParseHexQuantity(head.Number)
	if err != nil {
		return "", 0, fmt.Errorf("invalid anchor height: %w", rpc.ErrMalformedResponse)
	}
	return strings.ToLower(head.Hash), height, nil
}

// StrictHeadService establishes corroborated latest and safe references
// through the client's reference stage: the facade and the invocation's
// business reads share one establishment, so the same fleet, tag, and request
// produce the same evidence on both paths.
type StrictHeadService struct {
	stage    *ReferenceStage
	corrob   *rpc.CorroborationShared
	corrobOK bool
}

// NewStrictHeadService builds the default latest-head establishment service
// over the shared stage.
func NewStrictHeadService(stage *ReferenceStage, corrob *rpc.CorroborationShared) *StrictHeadService {
	return &StrictHeadService{stage: stage, corrob: corrob, corrobOK: corrob != nil}
}

// snapshot captures the service's shared adjudication configuration for one
// round.
func (s *StrictHeadService) snapshot() (rpc.Corroboration, bool) {
	if !s.corrobOK {
		return rpc.Corroboration{}, false
	}
	return s.corrob.Snapshot(), true
}

// Reference establishes the default moving reference (latest). Each facade
// call runs its own invocation: freshness is per call (decision 0026), and
// the invocation's frozen basis is the pool's current voting membership.
func (s *StrictHeadService) Reference(ctx context.Context) (rpc.ReferenceEvidence, error) {
	return s.establish(ctx, rpc.ReferenceLatest)
}

// ReferenceForTag establishes the reference for exactly the requested tag.
// Each level keeps its meaning — safe is anchored at the safe reference,
// never silently latest — and the returned evidence records the original tag.
func (s *StrictHeadService) ReferenceForTag(ctx context.Context, tag string) (rpc.ReferenceEvidence, error) {
	if tag == "" || tag == string(rpc.ReferenceLatest) {
		return s.Reference(ctx)
	}
	return s.establish(ctx, rpc.ReferenceKindForTag(tag))
}

// establish runs one stage round on a fresh invocation: the stage reads the
// frozen basis's members and corroboration snapshot, exactly the inputs the
// invocation path's business reads establish against.
func (s *StrictHeadService) establish(ctx context.Context, kind rpc.ReferenceKind) (rpc.ReferenceEvidence, error) {
	snap, _ := s.snapshot()
	refs := s.stage.memberRefs()
	// The facade establishment carries the same capture the invocation path
	// does: a failed round's partial evidence stays visible on the reference
	// the caller sees.
	ctx, capture := refcontext.New(ctx)
	completed, err := s.stage.Establish(ctx, facadeInvocation(ctx, refs, snap), rpc.ReferenceRequest{Kind: kind})
	if err != nil {
		if ref := capture.Reference(); ref != nil {
			return *ref, err
		}
		return rpc.ReferenceEvidence{}, err
	}
	ref, ok := completed.Reference()
	if !ok {
		return rpc.ReferenceEvidence{}, fmt.Errorf("anchor: establishment completed without evidence: %w", rpc.ErrReferenceRequired)
	}
	return ref, nil
}

// FinalizedHeadService establishes a common reference from provider-reported
// finalized views through the client's finalized-strategy stage. The strict
// levels still ride the strict head algorithm through the same stage.
type FinalizedHeadService struct {
	stage *ReferenceStage
}

// NewFinalizedHeadService builds the common finalized-reference service over
// the shared stage. The stage's strategy (finalized preset with its lag
// bound) and the composition-tuned collect timeout are the stage's own.
func NewFinalizedHeadService(stage *ReferenceStage) *FinalizedHeadService {
	return &FinalizedHeadService{stage: stage}
}

// Reference establishes the finalized reference: the finalized strategy pins
// the default reference at the finalized level — the conservative preset's
// whole point.
func (s *FinalizedHeadService) Reference(ctx context.Context) (rpc.ReferenceEvidence, error) {
	return s.establish(ctx, rpc.ReferenceLatest)
}

// ReferenceForTag routes the tag: latest and safe ride the strict head
// algorithm, finalized rides the candidate-selection algorithm (the stage's
// finalized strategy), anything else fails.
func (s *FinalizedHeadService) ReferenceForTag(ctx context.Context, tag string) (rpc.ReferenceEvidence, error) {
	if tag == "" || tag == "latest" {
		return s.Reference(ctx)
	}
	return s.establish(ctx, rpc.ReferenceKindForTag(tag))
}

func (s *FinalizedHeadService) establish(ctx context.Context, kind rpc.ReferenceKind) (rpc.ReferenceEvidence, error) {
	// The finalized preset defaults to the majority floor with the
	// documented lag bound: the stage's own configuration.
	snap := rpc.Corroboration{Mode: rpc.CorroborationMajority, Floor: 2}
	refs := s.stage.memberRefs()
	ctx, capture := refcontext.New(ctx)
	completed, err := s.stage.Establish(ctx, facadeInvocation(ctx, refs, snap), rpc.ReferenceRequest{Kind: kind})
	if err != nil {
		if ref := capture.Reference(); ref != nil {
			return *ref, err
		}
		return rpc.ReferenceEvidence{}, err
	}
	ref, ok := completed.Reference()
	if !ok {
		return rpc.ReferenceEvidence{}, fmt.Errorf("anchor: establishment completed without evidence: %w", rpc.ErrReferenceRequired)
	}
	return ref, nil
}

// facadeInvocation freezes one facade establishment's basis: the pool's
// current voting members and the service's adjudication snapshot. Freshness
// is per call, so the invocation is created and completed within one
// establish round. When the operation's context carries a material
// collector, the round's raw responses record into it — the establishment
// is this call's material like any other source round.
func facadeInvocation(ctx context.Context, refs []rpc.SourceRef, snap rpc.Corroboration) rpc.Invocation {
	opts := []rpc.InvocationOption(nil)
	if collector := uncertain.CollectorFromContext(ctx); collector != nil {
		opts = []rpc.InvocationOption{rpc.WithInvocationCapture(collector.InvocationCapture())}
	}
	return rpc.NewInvocation(rpc.NewInvocationID(), rpc.FreezeBasis(refs, snap), opts...)
}
