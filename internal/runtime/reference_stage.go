package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/codermaybe/ethguard/aggregator"
	"github.com/codermaybe/ethguard/internal/refcontext"
	"github.com/codermaybe/ethguard/internal/reference"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
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
	// StrictOptions carry the strict head policy options the community's
	// establishment share with the facade's resolver service: identical
	// options at both call sites, so the two paths cannot diverge on the
	// comparison domain.
	StrictOptions []rpc.StrictOption
}

// ReferenceStage implements the aggregator-owned establishment seam over the
// two establishment algorithms. One stage is constructed per client; each
// establishment runs on the invoking invocation's frozen basis, so the
// reference rounds and the business round share members, thresholds, and
// error assembly — the same assembly the facade's resolver service runs,
// because both drive one StrictHeadEstablishment. Establishment failure
// returns the invocation unchanged: the business fan-out never starts
// without its reference.
type ReferenceStage struct {
	pool    *pool.Pool
	maxLag  uint64
	timeout time.Duration
	rec     observe.Recorder
	// finalized selects the candidate algorithm for the finalized tag.
	finalized bool
	// strict is the round the strict levels drive.
	strict *StrictHeadEstablishment
}

// NewReferenceStage builds the client's establishment stage.
func NewReferenceStage(cfg ReferenceStageConfig) *ReferenceStage {
	return &ReferenceStage{
		pool:      cfg.Pool,
		maxLag:    cfg.MaxLag,
		timeout:   cfg.Timeout,
		rec:       cfg.Recorder,
		finalized: cfg.FinalizedStrategy,
		strict:    NewStrictHeadEstablishment(cfg.Pool, cfg.Recorder, cfg.Timeout, cfg.StrictOptions...),
	}
}

var _ aggregator.ReferenceStage = (*ReferenceStage)(nil)

// Establish performs the reference rounds for the declared kind and returns
// the invocation carrying the evidence.
func (s *ReferenceStage) Establish(ctx context.Context, invocation rpc.Invocation, request rpc.ReferenceRequest) (rpc.Invocation, error) {
	if !request.IsValid() {
		return invocation, fmt.Errorf("reference: unknown reference kind %q: %w", request.Kind, rpc.ErrInvalidRequest)
	}
	// The reference rounds address exactly the invocation's frozen basis. A
	// member removed from the pool since the basis froze stays resolvable as
	// a closed source and fails closed inside the round.
	members, err := s.basisSources(invocation.Basis())
	if err != nil {
		return invocation, err
	}
	var (
		ref        rpc.ReferenceEvidence
		emissions  []pool.Emission
		collectErr error
	)
	// The finalized strategy pins the default reference (latest) at the
	// finalized level too — that is the conservative preset's whole point.
	useFinalizedAlgorithm := s.finalized && request.Kind != rpc.ReferenceSafe
	switch {
	case useFinalizedAlgorithm:
		// The finalized establishment keeps the snapshot's floor and mode,
		// exactly as the facade path does.
		snapshot := invocation.Basis().Corroboration
		ref, err = reference.Finalized(ctx, NewPoolHeadReader(s.pool, members), MethodHeadCodec{}, s.maxLag, snapshot.Floor, snapshot.Mode, s.timeout, s.rec)
	case request.Kind == rpc.ReferenceLatest || request.Kind == rpc.ReferenceSafe || request.Kind == rpc.ReferenceFinalized:
		// The strict level rides the shared establishment engine: same
		// members, same snapshot, same assembly as the facade's resolver
		// service.
		snapshot := invocation.Basis().Corroboration
		round, roundErr := s.strict.establish(ctx, members, snapshot, true, string(request.Kind))
		ref, emissions, err = round.reference, round.emissions, roundErr
		collectErr = round.collectErr
	default:
		err = fmt.Errorf("reference: unknown reference kind %q: %w", request.Kind, rpc.ErrInvalidRequest)
	}
	if collectErr != nil || emissions != nil {
		// The raw responses of the round stay on the invocation's own trail,
		// exactly the records the pipeline's collect phase would keep.
		captureEmission(invocation, emissions, collectErr)
	}
	// The evidence records the requested tag: latest rides as the default
	// reference with no tag, every other level names itself.
	ref.Tag = taggedOriginTag(string(request.Kind))
	if err != nil {
		// A failed establishment keeps its evidence visible to the decision:
		// the business fan-out never starts, and the recorded faults stay on
		// the reference the caller sees. The evidence records the requested
		// tag; the caller's wrapper owns the "anchor:" prefix.
		refcontext.Publish(ctx, ref)
		return invocation, err
	}
	// The evidence rides the invocation: the business read resolves against
	// the established reference on the same basis.
	return invocation.WithReference(ref)
}

// basisSources resolves the invocation's frozen basis against the pool, so
// every round addresses exactly the voting basis and nothing else. Unknown
// members fail before any network round starts.
func (s *ReferenceStage) basisSources(basis rpc.Basis) ([]*pool.Source, error) {
	members := make([]*pool.Source, 0, basis.Size())
	for _, member := range basis.Members {
		source, ok := s.pool.SourceByID(member.ID)
		if !ok {
			return nil, fmt.Errorf("reference: voting member %q is not registered: %w", member.ID, rpc.ErrInvalidRequest)
		}
		members = append(members, source)
	}
	return members, nil
}

// captureEmission records the round's raw responses and the collection error
// into the invocation's own raw-data collector, so a stage inside a business
// invocation keeps the same raw trail the pipeline's collect phase keeps.
func captureEmission(invocation rpc.Invocation, emissions []pool.Emission, collectErr error) {
	capture := invocation.Capture()
	if capture == nil {
		return
	}
	for _, emission := range emissions {
		errText := ""
		if emission.Err != nil {
			errText = emission.Err.Error()
		}
		value := emission.Val
		capture.Record("response", anchorMethodName, aggregator.SourceRefOf(emission.Src), value, errText)
	}
	if collectErr != nil {
		capture.Record("collect", anchorMethodName, rpc.SourceRef{}, nil, collectErr.Error())
	}
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
