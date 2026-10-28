package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/codermaybe/ethguard/aggregator"
	"github.com/codermaybe/ethguard/internal/jsonsafe"
	"github.com/codermaybe/ethguard/internal/refcontext"
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
	// FinalizedStrategy selects the candidate-selection algorithm for the
	// finalized tag. The default (false) establishes every tag through the
	// strict head algorithm, which is the strict-anchor client's behavior.
	FinalizedStrategy bool
}

// ReferenceStage implements the aggregator-owned establishment seam over the
// two establishment algorithms. One stage is constructed per client; each
// establishment runs on the invoking invocation's frozen basis, so the
// reference rounds and the business round share members, thresholds, and
// error assembly. Establishment failure returns the invocation unchanged:
// the business fan-out never starts without its reference.
type ReferenceStage struct {
	pool      *pool.Pool
	maxLag    uint64
	timeout   time.Duration
	rec       observe.Recorder
	finalized bool
}

// NewReferenceStage builds the client's establishment stage.
func NewReferenceStage(cfg ReferenceStageConfig) *ReferenceStage {
	return &ReferenceStage{pool: cfg.Pool, maxLag: cfg.MaxLag, timeout: cfg.Timeout, rec: cfg.Recorder, finalized: cfg.FinalizedStrategy}
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
	// The finalized strategy pins the default reference (latest) at the
	// finalized level too — that is the conservative preset's whole point.
	useFinalizedAlgorithm := s.finalized && request.Kind != rpc.ReferenceSafe
	switch {
	case useFinalizedAlgorithm:
		// The finalized establishment keeps the snapshot's floor and mode,
		// exactly as the facade path does.
		snapshot := invocation.Basis().Corroboration
		ref, err = reference.Finalized(ctx, reader, MethodHeadCodec{}, s.maxLag, snapshot.Floor, snapshot.Mode, s.timeout, s.rec)
	case request.Kind == rpc.ReferenceLatest || request.Kind == rpc.ReferenceSafe || request.Kind == rpc.ReferenceFinalized:
		ref, err = s.establishStrict(ctx, invocation, string(request.Kind))
	default:
		err = fmt.Errorf("reference: unknown reference kind %q: %w", request.Kind, rpc.ErrInvalidRequest)
	}
	if err != nil {
		// A failed establishment keeps its evidence visible to the decision:
		// the business fan-out never starts, and the recorded faults stay on
		// the reference the caller sees. The evidence records the requested
		// tag; the caller's wrapper owns the "anchor:" prefix.
		ref.Tag = taggedOriginTag(string(request.Kind))
		refcontext.Publish(ctx, ref)
		return invocation, err
	}
	// The evidence records the requested tag: latest rides as the default
	// reference with no tag, every other level names itself.
	ref.Tag = taggedOriginTag(string(request.Kind))
	return invocation.WithReference(ref)
}

// AnchorHashReader returns the consumption-only hash reader method policies
// rewrite moving tags with. It reads an already-established reference from
// the in-flight invocation, or establishes one once through the invocation's
// establishment stage — on the same invocation, with no nested aggregate and
// no facade resolver call.
func (s *ReferenceStage) AnchorHashReader() rpc.AnchorFunc {
	return func(ctx context.Context) (string, error) {
		ref, err := s.establishedForTag(ctx)
		if err != nil {
			return "", err
		}
		return ref.Hash, nil
	}
}

// AnchorHeightReader is the height spelling of the same seam, for methods
// whose wire form cannot carry a hash.
func (s *ReferenceStage) AnchorHeightReader() rpc.AnchorHeightFunc {
	return func(ctx context.Context) (uint64, error) {
		ref, err := s.establishedForTag(ctx)
		if err != nil {
			return 0, err
		}
		return ref.Height, nil
	}
}

// establishedForTag reads the established reference for the anchor tag from
// the in-flight invocation. The reader never establishes and never touches
// the network: a missing or non-matching reference reports the pipeline
// sentinel, and the aggregator answers it by running the reference stage on
// the same invocation. The raw tag is what the evidence records (latest
// rides as the default reference with no tag).
func (s *ReferenceStage) establishedForTag(ctx context.Context) (rpc.ReferenceEvidence, error) {
	tag := rpc.AnchorTag(ctx)
	invocation, ok := refcontext.Invocation(ctx)
	if !ok {
		return rpc.ReferenceEvidence{}, fmt.Errorf("anchor: no in-flight invocation for tag %q: %w", tag, rpc.ErrInvalidRequest)
	}
	ref, ok := invocation.Reference()
	if !ok || !referenceMatchesTag(ref, tag) {
		return rpc.ReferenceEvidence{}, rpc.ErrReferenceRequired
	}
	if ref.Status != rpc.Corroborated {
		return ref, fmt.Errorf("uncorroborated reference: %w", rpc.ErrConflict)
	}
	return ref, nil
}

// referenceKindForTag maps the wire tag to the declared reference kind.
func referenceKindForTag(tag string) rpc.ReferenceKind {
	switch strings.ToLower(tag) {
	case string(rpc.ReferenceSafe):
		return rpc.ReferenceSafe
	case string(rpc.ReferenceFinalized):
		return rpc.ReferenceFinalized
	default:
		return rpc.ReferenceLatest
	}
}

// referenceMatchesTag reports whether established evidence serves this tag:
// each level keeps its meaning, so safe never reuses latest and vice versa.
func referenceMatchesTag(ref rpc.ReferenceEvidence, tag string) bool {
	kind := referenceKindForTag(tag)
	if kind == rpc.ReferenceLatest {
		// The default reference records no tag; anything else names its level.
		return ref.Tag == "" || strings.EqualFold(ref.Tag, string(rpc.ReferenceLatest))
	}
	return strings.EqualFold(ref.Tag, string(kind))
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
	// A terminal collection failure with nothing usable returns the pool's
	// own sentinel instead of a resolver's empty-input error.
	if collectErr != nil && !hasUsableHeadEmission(emissions) {
		s.recordStageEvent(started, ref, emissions, nil, collectErr)
		return ref, collectErr
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
	// Fault assembly mirrors the aggregate-owned establishment: the
	// resolution's per-source statement classifies attempted sources when it
	// is available, and the resolver's own fault list plus the per-source
	// response faults carry the rest.
	if verdicts := finalizeStageVerdicts(outcome.Verdicts, emissions); verdicts != nil {
		attempted := make(map[string]bool, len(emissions))
		for _, emission := range emissions {
			if emission.Src != nil {
				attempted[emission.Src.ID()] = emission.Attempted
			}
		}
		for _, verdict := range verdicts {
			if verdict.Kind != rpc.VerdictUnusable {
				continue
			}
			ref.SourceFaults = append(ref.SourceFaults, rpc.SourceFault{Source: verdict.Source, Err: verdict.Err})
			if attempted[verdict.Source.ID] && verdict.Err != nil {
				ref.Faults = append(ref.Faults, fmt.Errorf("source %q: %w", verdict.Source.Name, verdict.Err))
			}
		}
	} else {
		ref.Faults = append(ref.Faults, decision.Faults...)
		for _, emission := range emissions {
			if !emission.Attempted || emission.Err == nil {
				continue
			}
			source := sourceRefOf(emission.Src)
			ref.Faults = append(ref.Faults, fmt.Errorf("source %q: %w", source.Name, emission.Err))
			ref.SourceFaults = append(ref.SourceFaults, rpc.SourceFault{Source: source, Err: emission.Err})
		}
	}
	if resolveErr == nil && decision.Status != rpc.Corroborated {
		resolveErr = fmt.Errorf("uncorroborated anchor: %w", rpc.ErrConflict)
	}
	if resolveErr != nil {
		s.recordStageEvent(started, ref, emissions, verdictsForEvent(outcome.Verdicts, emissions), resolveErr)
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
		s.recordStageEvent(started, ref, emissions, verdictsForEvent(outcome.Verdicts, emissions), err)
		return ref, err
	}
	ref.Hash, ref.Height, ref.Status = hash, height, rpc.Corroborated
	s.recordStageEvent(started, ref, emissions, verdictsForEvent(outcome.Verdicts, emissions), nil)
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

// hasUsableHeadEmission reports whether any emission delivered a value.
func hasUsableHeadEmission(emissions []pool.Emission) bool {
	for _, emission := range emissions {
		if emission.Val != nil {
			return true
		}
	}
	return false
}

// verdictsForEvent reclassifies unattempted members for the observability
// event, mirroring the coordinator's absent marking.
func verdictsForEvent(verdicts []rpc.SourceVerdict, emissions []pool.Emission) []rpc.SourceVerdict {
	return finalizeStageVerdicts(verdicts, emissions)
}

// recordStageEvent folds one establishment round into exactly one
// observability event under the anchor method label, matching the event the
// establishment aggregate used to emit.
func (s *ReferenceStage) recordStageEvent(started time.Time, ref rpc.ReferenceEvidence, emissions []pool.Emission, verdicts []rpc.SourceVerdict, resolveErr error) {
	if s.rec == nil || s.rec == observe.Nop {
		return
	}
	classifier := func(emission pool.Emission) bool {
		if verdicts == nil {
			return emission.Err == nil && emission.Val != nil
		}
		for _, verdict := range verdicts {
			if emission.Src != nil && verdict.Source.ID == emission.Src.ID() {
				return verdict.Kind == rpc.VerdictSupported || verdict.Kind == rpc.VerdictDissented
			}
		}
		return false
	}
	event := observe.DecisionEvent{
		Method:   anchorMethodName,
		Duration: time.Since(started),
		Faults:   len(ref.Faults),
	}
	if resolveErr != nil {
		event.Status = "failed"
		event.Conflict = errors.Is(resolveErr, rpc.ErrConflict)
	} else {
		event.Status = ref.Status.String()
	}
	for _, emission := range emissions {
		if !emission.Attempted {
			continue
		}
		source := "unknown"
		if emission.Src != nil {
			source = emission.Src.Name()
		}
		event.Attempts = append(event.Attempts, observe.SourceEvent{
			Source: source,
			RTT:    emission.RTT,
			Failed: !classifier(emission),
		})
	}
	s.rec.RecordDecision(event)
}
