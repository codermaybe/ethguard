package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/codermaybe/ethguard/internal/rpcengine"
	"github.com/codermaybe/ethguard/internal/uncertain"
	"github.com/codermaybe/ethguard/rpc"
)

// anchorMethodName is the internal registry key for the strict head read. It
// is what observability events name as the establishment call's method.
const anchorMethodName = "anchor"

// headAnchorPolicy is the strict head policy for reference establishment: it
// preserves the winning source's spelling in the decision value (the anchor
// evidence keeps the source's own wire form; the request path lowercases the
// derived hash separately). Reference admissibility is checked against the
// invocation's purpose and frozen basis after the round has formed evidence.
type headAnchorPolicy struct {
	resolver rpcengine.CorroboratingResolver
}

func (p headAnchorPolicy) ResolveCorroborated(c rpcengine.Corroboration, obs []rpc.Observation) (rpcengine.Outcome, error) {
	d, err := p.resolver.ResolveCorroborated(c, obs)
	d.Decision = restoreAnchorSourceValue(d.Decision, obs)
	return d, err
}

// Anchor comparison uses the canonical head encoder, but the anchor decision
// is an internal reference observation and must preserve the winning source's
// wire spelling. The request path lowercases the derived hash separately.
func restoreAnchorSourceValue(d rpc.Decision, obs []rpc.Observation) rpc.Decision {
	if (d.Status != rpc.Corroborated && d.Status != rpc.Forwarded) || d.Representative.ID == "" {
		return d
	}
	for _, item := range obs {
		if item.Source.ID == d.Representative.ID {
			d.Value = append(json.RawMessage(nil), item.Response.Answer.Result...)
			break
		}
	}
	return d
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

// ReferenceService establishes standalone references through the client's
// shared stage. The stage owns tag routing and candidate/confirmation rules;
// the service supplies the construction-time rule and a fresh invocation.
type ReferenceService struct {
	stage *ReferenceStage
	rule  rpcengine.Corroboration
}

// NewReferenceService builds a reference query service over the shared stage.
func NewReferenceService(stage *ReferenceStage, rule rpcengine.Corroboration) *ReferenceService {
	return &ReferenceService{stage: stage, rule: rule}
}

// Reference establishes the default moving reference. The finalized preset
// routes latest through finalized candidate selection and confirmation.
// Freshness is per call, with the pool's current voting membership frozen.
func (s *ReferenceService) Reference(ctx context.Context) (rpc.ReferenceEvidence, error) {
	return s.establish(ctx, rpc.ReferenceLatest, rpcengine.ReferencePurposeStandaloneAnchor)
}

// ReferenceForTag establishes the reference for exactly the requested tag.
// Safe always uses strict safe establishment. The stage records requested
// intent in the evidence; that tag is not independent proof of the chain level.
func (s *ReferenceService) ReferenceForTag(ctx context.Context, tag string) (rpc.ReferenceEvidence, error) {
	if tag == "" || tag == string(rpc.ReferenceLatest) {
		return s.Reference(ctx)
	}
	return s.establish(ctx, rpc.ReferenceKindForTag(tag), rpcengine.ReferencePurposeStandaloneAnchor)
}

// BusinessReferenceForTag establishes the pin for a composite business read.
// It shares the standalone query's stage and frozen construction rule, while
// permitting honest Forwarded evidence on a singleton basis.
func (s *ReferenceService) BusinessReferenceForTag(ctx context.Context, tag string) (rpc.ReferenceEvidence, error) {
	return s.establish(ctx, rpc.ReferenceKindForTag(tag), rpcengine.ReferencePurposeBusiness)
}

// establish runs the shared stage on a fresh invocation with the current
// members and this service's fixed rule. The stage returns its evidence
// beside the error, preserving partial evidence from failed rounds.
func (s *ReferenceService) establish(ctx context.Context, kind rpc.ReferenceKind, purpose rpcengine.ReferencePurpose) (rpc.ReferenceEvidence, error) {
	refs := s.stage.memberRefs()
	evidence, err := s.stage.establish(ctx, facadeInvocation(ctx, refs, s.rule), rpc.ReferenceRequest{Kind: kind}, purpose)
	if err != nil {
		if evidence == nil {
			return rpc.ReferenceEvidence{}, err
		}
		return *evidence, err
	}
	if evidence == nil {
		return rpc.ReferenceEvidence{}, fmt.Errorf("anchor: establishment completed without evidence: %w", rpc.ErrReferenceRequired)
	}
	return *evidence, nil
}

// facadeInvocation freezes one facade establishment's basis: the pool's
// current voting members and the service's adjudication snapshot. Freshness
// is per call, so the invocation is created and completed within one
// establish round. When the operation's context carries a material
// collector, the round's raw responses record into it — the establishment
// is this call's material like any other source round.
func facadeInvocation(ctx context.Context, refs []rpc.SourceRef, snap rpcengine.Corroboration) rpcengine.Invocation {
	opts := []rpcengine.InvocationOption(nil)
	if collector := uncertain.CollectorFromContext(ctx); collector != nil {
		opts = []rpcengine.InvocationOption{rpcengine.WithInvocationCapture(collector)}
	}
	return rpcengine.NewInvocation(rpcengine.NewInvocationID(), rpcengine.FreezeBasis(refs, snap), opts...)
}
