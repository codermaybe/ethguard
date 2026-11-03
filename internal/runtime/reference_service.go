package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/codermaybe/ethguard/internal/reference"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/pool"
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

// StrictHeadConfig carries the strict-head establishment configuration.
type StrictHeadConfig struct {
	Pool           *pool.Pool
	Recorder       observe.Recorder
	CollectTimeout time.Duration
	Corroboration  *rpc.CorroborationShared
	StrictOptions  []rpc.StrictOption
}

// StrictHeadService establishes corroborated latest and safe references
// through the strict head algorithm. It is the public facade's forwarding
// target: the algorithm itself is the runtime-owned establishment engine,
// shared with the invocation's reference stage.
type StrictHeadService struct {
	round    *StrictHeadEstablishment
	corrob   *rpc.CorroborationShared
	corrobOK bool
}

// NewStrictHeadService builds the default latest-head establishment service.
func NewStrictHeadService(cfg StrictHeadConfig) *StrictHeadService {
	return &StrictHeadService{
		round:  NewStrictHeadEstablishment(cfg.Pool, cfg.Recorder, cfg.CollectTimeout, cfg.StrictOptions...),
		corrob: cfg.Corroboration, corrobOK: cfg.Corroboration != nil,
	}
}

// snapshot captures the service's shared adjudication configuration for one
// round.
func (s *StrictHeadService) snapshot() (rpc.Corroboration, bool) {
	if !s.corrobOK {
		return rpc.Corroboration{}, false
	}
	return s.corrob.Snapshot(), true
}

// Reference establishes the default moving reference (latest).
func (s *StrictHeadService) Reference(ctx context.Context) (rpc.ReferenceEvidence, error) {
	snap, snapOK := s.snapshot()
	round, err := s.round.establish(ctx, nil, snap, snapOK, string(rpc.ReferenceLatest))
	return round.reference, err
}

// ReferenceForTag establishes the reference for exactly the requested tag.
// Each level keeps its meaning — safe is anchored at the safe reference,
// never silently latest — and the returned evidence records the original tag.
func (s *StrictHeadService) ReferenceForTag(ctx context.Context, tag string) (rpc.ReferenceEvidence, error) {
	if tag == "" || tag == string(rpc.ReferenceLatest) {
		return s.Reference(ctx)
	}
	snap, snapOK := s.snapshot()
	round, err := s.round.establish(ctx, nil, snap, snapOK, tag)
	return round.reference, err
}

// FinalizedHeadService establishes a common reference from provider-reported
// finalized views. The strict levels still ride the strict head algorithm
// through the shared establishment engine.
type FinalizedHeadService struct {
	pool    *pool.Pool
	rec     observe.Recorder
	maxLag  uint64
	quorum  int
	timeout time.Duration
	strict  *StrictHeadEstablishment
}

// NewFinalizedHeadService builds the common finalized-reference service.
func NewFinalizedHeadService(p *pool.Pool, rec observe.Recorder, maxLag uint64, quorum int, timeout time.Duration) *FinalizedHeadService {
	return &FinalizedHeadService{
		pool:   p,
		rec:    rec,
		maxLag: maxLag,
		quorum: quorum,
		// The finalized preset defaults to the majority floor until the
		// client re-tunes the resolver with its own corroboration snapshot.
		strict: NewStrictHeadEstablishment(p, rec, timeout),
	}
}

// Reference establishes the finalized reference under the service's own
// adjudication: the composition root tunes quorum and mode at construction
// (slice 2's composition), so no per-call snapshot inheritance exists.
func (s *FinalizedHeadService) Reference(ctx context.Context) (rpc.ReferenceEvidence, error) {
	return s.finalized(ctx, s.quorum, rpc.CorroborationMajority)
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
		if mode == rpc.CorroborationForwarded {
			floor = 0
		} else if floor < 1 {
			floor = 2
		}
		// The same validation the shared snapshot goes through: an invalid
		// mode or floor fails before any request.
		if _, err := rpc.NewCorroborationShared(mode, floor); err != nil {
			return rpc.ReferenceEvidence{Tag: tag, Status: rpc.Unknown}, err
		}
		round, err := s.strict.establish(ctx, nil, rpc.Corroboration{Mode: mode, Floor: floor}, true, tag)
		return round.reference, err
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
