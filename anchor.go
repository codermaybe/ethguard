package ethguard

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"ethguard/aggregator"
	"ethguard/observe"
	"ethguard/pool"
	"ethguard/rpc"
	"ethguard/rpc/methods"
)

// anchorMethod labels observability events for anchored reads establishing
// their block reference. A fixed literal keeps label cardinality bounded
// (decision 0008); it doubles as the internal registry key under which the
// anchor pipeline resolves, never reaching the wire.
const anchorMethod = "anchor"

// AnchorResolver establishes verified block references behind moving-tag
// reads (latest/safe/finalized). It is the replaceable anchoring strategy:
// implementations decide how a reference is chosen and how stale it may be,
// while call orchestration stays fixed. The default is StrictHeadAnchor;
// WithFinalizedAnchoring opts into conservative selection and
// NewCachedAnchor adds time-bounded reuse — decorators of this same seam.
type AnchorResolver interface {
	// AnchorBlockHash returns one block hash every source will be asked
	// about, or an error explaining why no trusted reference was reached.
	AnchorBlockHash(ctx context.Context) (string, error)
}

// StrictHeadAnchor is the default AnchorResolver: fan out a latest-block
// query through the pool and accept it only when sources agree under the
// block-head policy's strict consensus. If sources sit on different heads
// the resolution fails with conflict evidence rather than guessing which
// head is right.
//
// The pipeline rides the ordinary aggregation flow — collect, resolve,
// record — under the bounded "anchor" label, and its registry holds only
// the unanchored head policy, so establishment cannot recurse.
type StrictHeadAnchor struct {
	aggregate *aggregator.Aggregator
}

// NewStrictHeadAnchor builds the default resolver over p, reporting one
// decision event per establishment into rec.
func NewStrictHeadAnchor(p *pool.Pool, rec observe.Recorder) *StrictHeadAnchor {
	if rec == nil {
		rec = observe.Nop
	}
	return &StrictHeadAnchor{
		aggregate: aggregator.New(p, rpc.Registry{
			anchorMethod: anchorHeadPolicy{methods.BlockByNumberPolicy()},
		}, aggregator.WithRecorder(rec)),
	}
}

// AnchorBlockHash implements AnchorResolver.
func (a *StrictHeadAnchor) AnchorBlockHash(ctx context.Context) (string, error) {
	decision, err := a.aggregate.Aggregate(ctx, rpc.Request{
		Method: anchorMethod,
		Params: []any{"latest", false},
	})
	if err != nil {
		return "", err
	}
	return blockHashFromDecision(decision)
}

// FinalizedHeadAnchor is the conservative AnchorResolver: fan out a
// finalized-tag query and pin the reference at the lowest finalized height
// that still counts as fresh. Post-merge finalized blocks are irreversible,
// so this never places the anchor ahead of true finality — a colluding
// majority cannot confirm what has not been finalized, and a lagging or
// stalled minority can only drag the reference backward within the freshness
// bound, never forward.
//
// Selection rules over the fleet's finalized answers:
//
//   - Sources whose finalized height falls more than MaxFinalizedLag behind
//     the freshest observed finalized height are ignored as stale.
//   - The remaining lowest height wins; sources reporting it must agree on
//     its hash, otherwise the call fails with conflict evidence (a fork).
//   - One uncorroborated report of the winning height still pins the anchor
//     (Selected class): a false pin costs liveness inside the freshness
//     bound, never safety.
//
// The pipeline rides the same aggregation flow and "anchor" label as
// StrictHeadAnchor and cannot recurse for the same reason.
type FinalizedHeadAnchor struct {
	aggregate *aggregator.Aggregator
}

// FinalizedAnchorOption tunes a FinalizedHeadAnchor at construction time.
type FinalizedAnchorOption func(*finalizedAnchorConfig)

type finalizedAnchorConfig struct {
	maxLag uint64
}

// defaultMaxFinalizedLag bounds how far a source's finalized view may trail
// the freshest observed one before being excluded: about six minutes of
// slots at mainnet cadence — generous against clock and propagation skew,
// tight enough to freeze out genuinely stalled providers.
const defaultMaxFinalizedLag = 32

// WithMaxFinalizedLag overrides how far behind the freshest observed
// finalized height a source may trail before its view is excluded as stale.
func WithMaxFinalizedLag(blocks uint64) FinalizedAnchorOption {
	return func(c *finalizedAnchorConfig) { c.maxLag = blocks }
}

// NewFinalizedHeadAnchor builds the conservative resolver over p, reporting
// one decision event per establishment into rec.
func NewFinalizedHeadAnchor(p *pool.Pool, rec observe.Recorder, opts ...FinalizedAnchorOption) *FinalizedHeadAnchor {
	cfg := finalizedAnchorConfig{maxLag: defaultMaxFinalizedLag}
	for _, opt := range opts {
		opt(&cfg)
	}
	if rec == nil {
		rec = observe.Nop
	}
	return &FinalizedHeadAnchor{
		aggregate: aggregator.New(p, rpc.Registry{
			anchorMethod: finalizedHeadPolicy{maxLag: cfg.maxLag},
		}, aggregator.WithRecorder(rec)),
	}
}

// AnchorBlockHash implements AnchorResolver.
func (a *FinalizedHeadAnchor) AnchorBlockHash(ctx context.Context) (string, error) {
	decision, err := a.aggregate.Aggregate(ctx, rpc.Request{
		Method: anchorMethod,
		Params: []any{"finalized", false},
	})
	if err != nil {
		return "", err
	}
	return blockHashFromDecision(decision)
}

// finalizedHeadPolicy resolves the fleet's finalized-block answers with
// lowest-fresh-height selection instead of strict consensus.
type finalizedHeadPolicy struct {
	maxLag uint64
}

type finalizedHead struct {
	Number string `json:"number"`
	Hash   string `json:"hash"`
}

func (p finalizedHeadPolicy) Prepare(_ context.Context, req rpc.Request) (rpc.Request, error) {
	prepared := rpc.Request{Method: methods.MethodGetBlockByNumber, Params: req.Params}
	return prepared, nil
}

// Normalize reduces a finalized block object to "<height>|<hash>" with the
// height in decimal, so equivalent hexadecimal spellings cluster together.
func (p finalizedHeadPolicy) Normalize(value json.RawMessage) (string, error) {
	head, err := decodeFinalizedHead(value)
	if err != nil {
		return "", err
	}
	height, err := parseHeight(head.Number)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d|%s", height, strings.ToLower(head.Hash)), nil
}

func (p finalizedHeadPolicy) Resolve(obs []rpc.Observation) (rpc.Decision, error) {
	var decision rpc.Decision
	type view struct {
		height uint64
		hash   string
		value  json.RawMessage
	}
	views := make(map[*pool.Source]view, len(obs))
	for _, item := range obs {
		if item.Err != nil {
			continue
		}
		head, err := decodeFinalizedHead(item.Value)
		if err != nil {
			decision.Faults = append(decision.Faults, fmt.Errorf("normalize: %w", err))
			continue
		}
		height, err := parseHeight(head.Number)
		if err != nil {
			decision.Faults = append(decision.Faults, fmt.Errorf("normalize: %w", err))
			continue
		}
		views[item.Source] = view{height: height, hash: strings.ToLower(head.Hash), value: item.Value}
	}
	if len(views) == 0 {
		return rpc.Decision{}, fmt.Errorf(
			"finalized head: no source produced a valid response (%d attempted): %w",
			len(obs), rpc.ErrNoValidResponse)
	}

	var freshest uint64
	for _, v := range views {
		if v.height > freshest {
			freshest = v.height
		}
	}

	// Cluster surviving views by height, excluding stale ones in the same
	// pass; deterministic iteration comes from sorting heights afterwards.
	clusters := make(map[uint64][]*pool.Source)
	hashes := make(map[uint64]string)
	values := make(map[uint64]json.RawMessage)
	var excluded []*pool.Source
	for src, v := range views {
		switch {
		case freshest-v.height > p.maxLag:
			excluded = append(excluded, src)
			decision.Faults = append(decision.Faults, fmt.Errorf(
				"source %q: stale finalized height %d trails %d beyond %d blocks",
				src.Name(), v.height, freshest, p.maxLag))
		default:
			clusters[v.height] = append(clusters[v.height], src)
			if _, seen := hashes[v.height]; !seen {
				hashes[v.height] = v.hash
				values[v.height] = v.value
			}
		}
	}
	for _, src := range excluded {
		decision.Reject = append(decision.Reject, src)
	}

	heights := make([]uint64, 0, len(clusters))
	for h := range clusters {
		heights = append(heights, h)
	}
	slices.Sort(heights)
	winner := heights[0]

	// A fork at the pinned height is real dissent: fail closed with evidence.
	distinct := make(map[string]bool, len(clusters[winner]))
	for _, v := range views {
		if v.height == winner {
			distinct[v.hash] = true
		}
	}
	if len(distinct) > 1 {
		decision.Value = values[winner]
		decision.Support = clusters[winner]
		return decision, fmt.Errorf("finalized head: %d hashes claimed for height %d: %w",
			len(distinct), winner, rpc.ErrConflict)
	}

	decision.Status = rpc.Verified
	if len(clusters[winner]) == 1 {
		decision.Status = rpc.Selected // no corroboration, no Verified claim
	}
	decision.Value = values[winner]
	decision.Support = clusters[winner]
	return decision, nil
}

func decodeFinalizedHead(value json.RawMessage) (finalizedHead, error) {
	var head finalizedHead
	if err := json.Unmarshal(value, &head); err != nil {
		return head, err
	}
	if head.Number == "" || len(head.Hash) < 3 {
		return head, fmt.Errorf("finalized head: missing number or hash")
	}
	return head, nil
}

func parseHeight(number string) (uint64, error) {
	digits := strings.TrimPrefix(number, "0x")
	height, err := strconv.ParseUint(digits, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("finalized head: invalid height %q", number)
	}
	return height, nil
}

// anchorHeadPolicy adapts the unanchored block-head policy to the anchor
// pipeline: events report under "anchor", while the fleet is asked the real
// wire method.
type anchorHeadPolicy struct {
	rpc.Policy
}

func (p anchorHeadPolicy) Prepare(ctx context.Context, req rpc.Request) (rpc.Request, error) {
	prepared, err := p.Policy.Prepare(ctx, req)
	if err != nil {
		return rpc.Request{}, err
	}
	prepared.Method = methods.MethodGetBlockByNumber
	return prepared, nil
}

// blockHashFromDecision extracts the anchor hash from a resolved head.
func blockHashFromDecision(decision rpc.Decision) (string, error) {
	var head struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(decision.Value, &head); err != nil || len(head.Hash) < 3 {
		return "", fmt.Errorf("%w: winning anchor block carries no hash", rpc.ErrMalformedResponse)
	}
	return head.Hash, nil
}

// CachedAnchor decorates any AnchorResolver with time-bounded reuse: the
// first establishment inside a TTL window is shared by every moving-tag
// read, so anchored calls cost one fan-out instead of two. Failures are
// never cached; concurrent misses serialize on one establishment.
//
// Safety note: over a FinalizedHeadAnchor the pinned block is irreversible,
// so caching cannot regress safety — it only bounds liveness of progress.
// Over StrictHeadAnchor the head moves, and the TTL bounds how far behind
// the chain reads may land; size it against the caller's freshness needs
// (see decision 0007, anchor caching amendment).
type CachedAnchor struct {
	inner AnchorResolver
	ttl   time.Duration

	mu         sync.Mutex
	hash       string
	validUntil time.Time
}

// NewCachedAnchor wraps inner so its references are reused for ttl.
func NewCachedAnchor(inner AnchorResolver, ttl time.Duration) *CachedAnchor {
	return &CachedAnchor{inner: inner, ttl: ttl}
}

// AnchorBlockHash implements AnchorResolver.
func (c *CachedAnchor) AnchorBlockHash(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hash != "" && time.Now().Before(c.validUntil) {
		return c.hash, nil
	}
	hash, err := c.inner.AnchorBlockHash(ctx)
	if err != nil {
		return "", err
	}
	c.hash = hash
	c.validUntil = time.Now().Add(c.ttl)
	return hash, nil
}
