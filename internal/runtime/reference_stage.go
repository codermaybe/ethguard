package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/codermaybe/ethguard/aggregator"
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
// returns the evidence the rounds formed beside the error: the caller keeps
// that evidence visible to the decision and never starts the business
// fan-out without its reference.
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

// memberRefs freezes the pool's current voting membership as the basis refs
// one facade establishment runs against.
func (s *ReferenceStage) memberRefs() []rpc.SourceRef {
	members := s.pool.VotingMembers()
	refs := make([]rpc.SourceRef, 0, len(members))
	for _, member := range members {
		refs = append(refs, aggregator.SourceRefOf(member))
	}
	return refs
}

// Establish performs the reference rounds for the declared kind and returns
// the evidence those rounds formed, whether or not the establishment
// succeeded: the caller attaches success evidence to the invocation, and a
// failed round's evidence stays visible to the decision.
func (s *ReferenceStage) Establish(ctx context.Context, invocation rpc.Invocation, request rpc.ReferenceRequest) (*rpc.ReferenceEvidence, error) {
	if !request.IsValid() {
		return nil, fmt.Errorf("reference: unknown reference kind %q: %w", request.Kind, rpc.ErrInvalidRequest)
	}
	// The reference rounds address exactly the invocation's frozen basis. A
	// member removed from the pool since the basis froze stays resolvable as
	// a closed source and fails closed inside the round.
	members, err := s.basisSources(invocation.Basis())
	if err != nil {
		return nil, err
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
		// exactly as the facade path does. The reader wrapper records both
		// selection and confirmation observations on this invocation's trail.
		snapshot := invocation.Basis().Corroboration
		reader := capturedHeadReader{inner: NewPoolHeadReader(s.pool, members), invocation: invocation}
		ref, err = reference.Finalized(ctx, reader, MethodHeadCodec{}, s.maxLag, snapshot.Floor, snapshot.Mode, s.timeout, s.rec)
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
		// A failed establishment hands back the evidence it formed: the
		// caller keeps it visible to the decision and never starts the
		// business fan-out. The evidence records the requested tag; the
		// caller's wrapper owns the "anchor:" prefix.
		return &ref, err
	}
	// The evidence is the caller's: the business read resolves against the
	// established reference on the same basis, and the caller attaches it to
	// the invocation exactly once.
	return &ref, nil
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
		if emission.Response.Err() != nil {
			errText = emission.Response.Err().Error()
		}
		value := emission.Response.Answer.Result
		capture.Record("response", anchorMethodName, aggregator.SourceRefOf(emission.Src), value, errText)
	}
	if collectErr != nil {
		capture.Record("collect", anchorMethodName, rpc.SourceRef{}, nil, collectErr.Error())
	}
}
