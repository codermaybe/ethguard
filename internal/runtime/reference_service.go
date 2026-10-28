package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/codermaybe/ethguard/aggregator"
	"github.com/codermaybe/ethguard/internal/refcontext"
	"github.com/codermaybe/ethguard/internal/reference"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

// anchorMethodName is the internal registry key for the strict head read. It
// is what observability events name as the establishment call's method.
const anchorMethodName = "anchor"

// headAnchorPolicy is the strict head policy for reference establishment: it
// retargets the anchor request at the block-by-number wire shape, preserves
// the winning source's spelling in the decision value (the anchor evidence
// keeps the source's own wire form; the request path lowercases the derived
// hash separately), and requires a corroborated result.
type headAnchorPolicy struct{ rpc.Policy }

func (p headAnchorPolicy) Prepare(ctx context.Context, req rpc.Request) (rpc.Request, error) {
	prepared, err := p.Policy.Prepare(ctx, req)
	if err != nil {
		return rpc.Request{}, err
	}
	prepared.Method = methods.MethodGetBlockByNumber
	return prepared, nil
}

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

// StrictHeadConfig carries the strict-head establishment configuration.
type StrictHeadConfig struct {
	Pool           *pool.Pool
	Recorder       observe.Recorder
	CollectTimeout time.Duration
	Corroboration  *rpc.CorroborationShared
	StrictOptions  []rpc.StrictOption
}

// StrictHeadService establishes corroborated latest and safe references
// through the strict head algorithm: one header fan-out over the voting
// membership, strict clustering on the head identity, and a Corroborated
// gate. It is the composition-root owner of the establishment aggregate.
type StrictHeadService struct {
	aggregate *aggregator.Aggregator
}

// NewStrictHeadService builds the default latest-head establishment service.
func NewStrictHeadService(cfg StrictHeadConfig) *StrictHeadService {
	return &StrictHeadService{
		aggregate: aggregator.New(cfg.Pool, rpc.Registry{anchorMethodName: headAnchorPolicy{methods.BlockByNumberPolicy(cfg.StrictOptions...)}},
			aggregator.WithRecorder(cfg.Recorder),
			aggregator.WithCollectTimeout(cfg.CollectTimeout),
			aggregator.WithCorroboration(cfg.Corroboration)),
	}
}

// Reference establishes the default moving reference (latest).
func (s *StrictHeadService) Reference(ctx context.Context) (rpc.ReferenceEvidence, error) {
	d, err := s.aggregate.Aggregate(ctx, rpc.Request{Method: anchorMethodName, Params: []any{"latest", false}})
	ref := rpc.ReferenceEvidence{
		Status:           d.Status,
		Origin:           "strict-head",
		ObservedAt:       time.Now(),
		Participants:     d.Participants,
		Support:          d.Support,
		Reject:           d.Reject,
		Faults:           d.Faults,
		SourceFaults:     d.SourceFaults,
		CollectionFaults: d.CollectionFaults,
	}
	if err != nil {
		return ref, err
	}
	ref.Hash, ref.Height, err = blockRefFromDecision(d)
	return ref, err
}

// ReferenceForTag establishes the reference for exactly the requested tag.
// Each level keeps its meaning — safe is anchored at the safe reference,
// never silently latest — and the returned evidence records the original tag.
func (s *StrictHeadService) ReferenceForTag(ctx context.Context, tag string) (rpc.ReferenceEvidence, error) {
	if tag == "" || tag == "latest" {
		return s.Reference(ctx)
	}
	d, err := s.aggregate.Aggregate(ctx, rpc.Request{Method: anchorMethodName, Params: []any{tag, false}})
	ref := rpc.ReferenceEvidence{
		Tag:              tag,
		Status:           d.Status,
		Origin:           "tagged-head",
		ObservedAt:       time.Now(),
		Participants:     d.Participants,
		Support:          d.Support,
		Reject:           d.Reject,
		Faults:           d.Faults,
		SourceFaults:     d.SourceFaults,
		CollectionFaults: d.CollectionFaults,
	}
	if err != nil {
		return ref, err
	}
	ref.Hash, ref.Height, err = blockRefFromDecision(d)
	return ref, err
}

// FinalizedHeadService establishes a common reference from provider-reported
// finalized views.
type FinalizedHeadService struct {
	pool    *pool.Pool
	rec     observe.Recorder
	maxLag  uint64
	quorum  int
	timeout time.Duration
}

// NewFinalizedHeadService builds the common finalized-reference service.
func NewFinalizedHeadService(p *pool.Pool, rec observe.Recorder, maxLag uint64, quorum int, timeout time.Duration) *FinalizedHeadService {
	return &FinalizedHeadService{pool: p, rec: rec, maxLag: maxLag, quorum: quorum, timeout: timeout}
}

// Reference establishes the finalized reference under the invocation's
// corroboration snapshot when one is in flight.
func (s *FinalizedHeadService) Reference(ctx context.Context) (rpc.ReferenceEvidence, error) {
	quorum, mode := s.quorum, rpc.CorroborationMajority
	if value, ok := refcontext.Corroboration(ctx); ok {
		if snapshot, valid := value.(rpc.Corroboration); valid {
			quorum, mode = snapshot.Floor, snapshot.Mode
		}
	}
	return s.finalized(ctx, quorum, mode)
}

// ReferenceForTag routes the tag: latest and safe ride the strict head
// algorithm under the snapshot's adjudication, finalized rides the
// candidate-selection algorithm, anything else fails.
func (s *FinalizedHeadService) ReferenceForTag(ctx context.Context, tag string) (rpc.ReferenceEvidence, error) {
	// latest and safe ride the strict head algorithm under the snapshot's
	// adjudication — one routing for both, differing only in the requested
	// tag the evidence records.
	if tag == "latest" || tag == "safe" {
		mode, floor := rpc.CorroborationMajority, s.quorum
		if value, ok := refcontext.Corroboration(ctx); ok {
			if snapshot, valid := value.(rpc.Corroboration); valid {
				mode, floor = snapshot.Mode, snapshot.Floor
			}
		}
		if mode == rpc.CorroborationForwarded {
			floor = 0
		} else if floor < 1 {
			floor = 2
		}
		shared, err := rpc.NewCorroborationShared(mode, floor)
		if err != nil {
			return rpc.ReferenceEvidence{Tag: tag, Status: rpc.Unknown}, err
		}
		var opts []rpc.StrictOption
		if floor > 0 {
			opts = append(opts, rpc.WithQuorum(floor))
		}
		if mode == rpc.CorroborationUnanimity {
			opts = append(opts, rpc.WithUnanimity())
		}
		strict := NewStrictHeadService(StrictHeadConfig{
			Pool:           s.pool,
			Recorder:       s.rec,
			CollectTimeout: s.timeout,
			Corroboration:  shared,
			StrictOptions:  opts,
		})
		return strict.ReferenceForTag(ctx, tag)
	}
	if tag == "finalized" || tag == "" {
		ref, err := s.Reference(ctx)
		ref.Tag = tag
		return ref, err
	}
	return rpc.ReferenceEvidence{Tag: tag, Status: rpc.Unknown, Origin: "finalized-anchor"}, fmt.Errorf("anchor: tag %q is not supported by finalized resolver: %w", tag, rpc.ErrInvalidRequest)
}

func (s *FinalizedHeadService) finalized(ctx context.Context, quorum int, mode rpc.CorroborationMode) (rpc.ReferenceEvidence, error) {
	return reference.Finalized(ctx, NewPoolHeadReader(s.pool, nil), MethodHeadCodec{}, s.maxLag, quorum, mode, s.timeout, s.rec)
}
