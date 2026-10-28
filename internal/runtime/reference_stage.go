package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/codermaybe/ethguard/aggregator"
	"github.com/codermaybe/ethguard/internal/jsonsafe"
	"github.com/codermaybe/ethguard/internal/reference"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

// ReferenceStageConfig carries the establishment configuration shared by one
// client's reference stage.
type ReferenceStageConfig struct {
	Pool     *pool.Pool
	MaxLag   uint64
	Timeout  time.Duration
	Recorder observe.Recorder
}

// ReferenceStage implements the aggregator-owned establishment seam over the
// two establishment algorithms. One stage is constructed per client; each
// establishment runs on the invoking invocation's frozen basis, so the
// reference rounds and the business round share members, thresholds, and
// error assembly. Establishment failure returns the invocation unchanged:
// the business fan-out never starts without its reference.
type ReferenceStage struct {
	pool    *pool.Pool
	maxLag  uint64
	timeout time.Duration
	rec     observe.Recorder
}

// NewReferenceStage builds the client's establishment stage.
func NewReferenceStage(cfg ReferenceStageConfig) *ReferenceStage {
	return &ReferenceStage{pool: cfg.Pool, maxLag: cfg.MaxLag, timeout: cfg.Timeout, rec: cfg.Recorder}
}

var _ aggregator.ReferenceStage = (*ReferenceStage)(nil)

// Establish performs the reference rounds for the declared kind and returns
// the invocation carrying the evidence.
func (s *ReferenceStage) Establish(ctx context.Context, invocation rpc.Invocation, request rpc.ReferenceRequest) (rpc.Invocation, error) {
	if !request.IsValid() {
		return invocation, fmt.Errorf("reference: unknown reference kind %q: %w", request.Kind, rpc.ErrInvalidRequest)
	}
	reader, err := PoolHeadReaderForBasis(s.pool, invocation.Basis())
	if err != nil {
		return invocation, err
	}
	var (
		ref rpc.ReferenceEvidence
	)
	switch request.Kind {
	case rpc.ReferenceFinalized:
		// The finalized establishment keeps the snapshot's floor and mode,
		// exactly as the facade path does.
		snapshot := invocation.Basis().Corroboration
		ref, err = reference.Finalized(ctx, reader, MethodHeadCodec{}, s.maxLag, snapshot.Floor, snapshot.Mode, s.timeout, s.rec)
	case rpc.ReferenceLatest, rpc.ReferenceSafe:
		ref, err = s.establishStrict(ctx, invocation, string(request.Kind))
	default:
		err = fmt.Errorf("reference: unknown reference kind %q: %w", request.Kind, rpc.ErrInvalidRequest)
	}
	if err != nil {
		return invocation, err
	}
	return invocation.WithReference(ref)
}

// establishStrict runs the strict head algorithm on the invocation's frozen
// basis: one voting fan-out, strict clustering on the head identity under the
// invocation's corroboration snapshot, and the Corroborated gate. It assembles
// the same evidence shape the facade establishment produces.
func (s *ReferenceStage) establishStrict(ctx context.Context, invocation rpc.Invocation, tag string) (rpc.ReferenceEvidence, error) {
	started := time.Now()
	ref := rpc.ReferenceEvidence{Tag: taggedOriginTag(tag), Origin: strictOrigin(tag), ObservedAt: started, Status: rpc.Unknown}
	snapshot := invocation.Basis().Corroboration
	members := make([]*pool.Source, 0, invocation.Basis().Size())
	for _, member := range invocation.Basis().Members {
		source, ok := s.pool.SourceByID(member.ID)
		if !ok {
			return ref, fmt.Errorf("reference: voting member %q is not registered: %w", member.ID, rpc.ErrInvalidRequest)
		}
		members = append(members, source)
	}
	emissions, collectErr := s.pool.CollectVoting(ctx, methods.MethodGetBlockByNumber, []any{tag, false}, pool.CollectOptions{Timeout: s.timeout, Sources: members})
	capture := invocation.Capture()
	if capture != nil {
		for _, emission := range emissions {
			errText := ""
			if emission.Err != nil {
				errText = emission.Err.Error()
			}
			capture.Record("response", anchorMethodName, sourceRefOf(emission.Src), emission.Val, errText)
		}
		if collectErr != nil {
			capture.Record("collect", anchorMethodName, rpc.SourceRef{}, nil, collectErr.Error())
		}
	}
	if collectErr != nil {
		err := fmt.Errorf("collect: %w", collectErr)
		ref.Faults = append(ref.Faults, err)
		ref.CollectionFaults = append(ref.CollectionFaults, err)
	}
	for i := range emissions {
		if emissions[i].Err != nil || emissions[i].Val == nil {
			continue
		}
		if err := jsonsafe.Validate(emissions[i].Val); err != nil {
			emissions[i].Err = fmt.Errorf("%w: %v", rpc.ErrMalformedResponse, err)
			emissions[i].Val = nil
		}
	}
	observations := make([]rpc.Observation, len(emissions))
	for i, emission := range emissions {
		observations[i] = rpc.Observation{
			Source: sourceRefOf(emission.Src),
			Value:  emission.Val,
			Err:    emission.Err,
		}
		ref.Participants = append(ref.Participants, observations[i].Source)
	}
	var opts []rpc.StrictOption
	if snapshot.Floor > 0 {
		opts = append(opts, rpc.WithQuorum(snapshot.Floor))
	}
	if snapshot.Mode == rpc.CorroborationUnanimity {
		opts = append(opts, rpc.WithUnanimity())
	}
	strict := rpc.Strict(MethodHeadCodec{}.Normalize, opts...)
	outcome, resolveErr := strict.(rpc.CorroboratingOutcomeResolver).ResolveOutcomeCorroborated(snapshot, observations)
	decision := outcome.Decision
	ref.Support = decision.Support
	supported := make(map[string]bool, len(ref.Support))
	for _, source := range ref.Support {
		supported[source.ID] = true
	}
	for _, source := range ref.Participants {
		if !supported[source.ID] {
			ref.Reject = append(ref.Reject, source)
		}
	}
	// Per-source faults from the resolution's own statement; unattempted
	// members are availability faults (their basis slot was kept).
	verdicts := finalizeStageVerdicts(outcome.Verdicts, emissions)
	for _, verdict := range verdicts {
		if verdict.Kind != rpc.VerdictUnusable {
			continue
		}
		if verdict.Err != nil {
			ref.Faults = append(ref.Faults, fmt.Errorf("source %q: %w", verdict.Source.Name, verdict.Err))
		}
		ref.SourceFaults = append(ref.SourceFaults, rpc.SourceFault{Source: verdict.Source, Err: verdict.Err})
	}
	if resolveErr == nil && decision.Status != rpc.Corroborated {
		resolveErr = fmt.Errorf("uncorroborated anchor: %w", rpc.ErrConflict)
	}
	if resolveErr != nil {
		return ref, resolveErr
	}
	// Preserve the winning source's wire spelling in the evidence value, the
	// same contract the facade establishment keeps.
	if decision.Representative.ID != "" {
		for _, item := range observations {
			if item.Source.ID == decision.Representative.ID {
				decision.Value = append(json.RawMessage(nil), item.Value...)
				break
			}
		}
	}
	hash, height, err := blockRefFromDecision(decision)
	if err != nil {
		return ref, err
	}
	ref.Hash, ref.Height, ref.Status = hash, height, rpc.Corroborated
	return ref, nil
}

// taggedOriginTag keeps the establishment evidence's tag contract: latest
// rides as the default reference and records no tag.
func taggedOriginTag(tag string) string {
	if tag == string(rpc.ReferenceLatest) {
		return ""
	}
	return tag
}

// strictOrigin names the establishment path the way the facade evidence does.
func strictOrigin(tag string) string {
	if tag == string(rpc.ReferenceLatest) {
		return "strict-head"
	}
	return "tagged-head"
}

// finalizeStageVerdicts reclassifies members the fan-out never attempted as
// absent, so gate-paused members stay availability faults.
func finalizeStageVerdicts(verdicts []rpc.SourceVerdict, emissions []pool.Emission) []rpc.SourceVerdict {
	if verdicts == nil {
		return nil
	}
	unattempted := make(map[string]struct{}, len(emissions))
	for _, emission := range emissions {
		if !emission.Attempted && emission.Src != nil {
			unattempted[emission.Src.ID()] = struct{}{}
		}
	}
	out := make([]rpc.SourceVerdict, len(verdicts))
	copy(out, verdicts)
	for i, verdict := range out {
		if _, skipped := unattempted[verdict.Source.ID]; skipped && verdict.Kind == rpc.VerdictUnusable {
			out[i].Kind = rpc.VerdictAbsent
		}
	}
	return out
}

// sourceRefOf copies a pool source's identity for observation records; a nil
// source (an unattempted emission) yields the zero reference.
func sourceRefOf(src *pool.Source) rpc.SourceRef {
	if src == nil {
		return rpc.SourceRef{}
	}
	return rpc.SourceRef{Name: src.Name(), URL: src.URL(), ID: src.ID()}
}
