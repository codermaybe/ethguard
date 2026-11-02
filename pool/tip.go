package pool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/codermaybe/ethguard/config"
	upstream "github.com/codermaybe/ethguard/internal/transport"
)

// Tip-tracking defaults live in config (DefaultTipInterval,
// DefaultTipMaxBehind): one source for both the YAML and Go construction
// paths, so they cannot drift (decision 0004). The poll interval is
// deliberately low-rate: probes ride the ordinary collection path and must
// never compete with traffic.

// maxTipProbeTimeout caps one RefreshTips probe regardless of how rarely the
// fleet is polled: the poll interval paces the sampler, not how long one
// manual probe may hold a caller.
const maxTipProbeTimeout = 30 * time.Second

// PoolOption tunes a Pool at construction time.
type PoolOption func(*poolConfig)

type poolConfig struct {
	tipEnabled         bool
	tipInterval        time.Duration
	maxTipLag          uint64
	tipSampleMaxAge    time.Duration
	tipSampleMaxAgeSet bool
	dialer             upstream.Dialer
}

// WithDialer selects the default connection factory for members without their
// own Dialer. The composition root supplies native HTTP by default; pool
// itself has no dependency on a concrete adapter.
func WithDialer(d upstream.Dialer) PoolOption {
	return func(c *poolConfig) { c.dialer = d }
}

// WithTipTracking enables block-tip sampling and the freshness eligibility
// gate: every interval the pool probes eth_blockNumber across sources, and
// requests declared as latest-tip reads skip sources whose tip trails the
// fleet's robust reference height by more than maxBehind blocks. A zero
// interval or lag selects the default (10s / 32 blocks). Probing reuses the
// ordinary collection path, so it shares connections, chain-ID checks, and
// health accounting; it bypasses the freshness gate itself, so a fully stale
// fleet can still recover. A sampled tip is valid for three sampling
// intervals by default (three missed polls); WithTipSampleMaxAge overrides
// that window explicitly.
func WithTipTracking(every time.Duration, maxBehind uint64) PoolOption {
	return func(c *poolConfig) {
		c.tipEnabled = true
		if every > 0 {
			c.tipInterval = every
		}
		if maxBehind > 0 {
			c.maxTipLag = maxBehind
		}
	}
}

// WithTipSampleMaxAge sets how long one sampled tip remains authoritative for
// the freshness gate. A sample older than the window is unknown: it neither
// proves the source fresh nor stale, and it does not contribute a height to
// the reference. A non-positive value is ignored, leaving the derived default
// (three sampling intervals). The window is a scheduling input only; it does
// not change membership, the voting basis, or the decision.
func WithTipSampleMaxAge(age time.Duration) PoolOption {
	return func(c *poolConfig) {
		if age > 0 {
			c.tipSampleMaxAge = age
			c.tipSampleMaxAgeSet = true
		}
	}
}

// NewPoolWithOptions creates an empty pool tuned by opts. Production
// construction uses NewWithMembers; an empty pool has no membership.
func NewPoolWithOptions(chainID uint64, opts ...PoolOption) *Pool {
	return newPoolWithDialerAndOptions(nil, chainID, opts...)
}

// newPoolWithDialerAndOptions lets tests combine an injected transport with
// option tuning.
func newPoolWithDialerAndOptions(d dialer, chainID uint64, opts ...PoolOption) *Pool {
	cfg := poolConfig{
		tipInterval: config.DefaultTipInterval(),
		maxTipLag:   config.DefaultTipMaxBehind(),
		dialer:      d,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	p := newPoolWithDialerAndChainID(cfg.dialer, chainID)
	p.maxTipLag = cfg.maxTipLag
	if cfg.tipEnabled {
		if !cfg.tipSampleMaxAgeSet {
			cfg.tipSampleMaxAge = 3 * cfg.tipInterval
		}
		p.tipInterval = cfg.tipInterval
		p.tipSampleMaxAge = cfg.tipSampleMaxAge
		p.stopTips = make(chan struct{})
		go p.tipLoop(cfg.tipInterval)
	}
	return p
}

// tipLoop samples tips until the pool closes. The first sample waits one
// interval: dialing stays deferred until a caller actually needs a source.
func (p *Pool) tipLoop(every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopTips:
			return
		case <-ticker.C:
			p.RefreshTips(context.Background())
		}
	}
}

// RefreshTips samples eth_blockNumber across sources once, updating each
// source's observed tip. It is called by the background loop and may be
// called manually to force an immediate sample — but only when tip tracking
// is enabled: on a pool constructed without WithTipTracking it is a no-op,
// because the sampler and its state do not exist.
//
// The probe bypasses the freshness gate on purpose: a fleet that drifted
// behind must still be able to refresh its own picture of "behind". It also
// ignores cooldown circuits, so recovery is detected proactively within one
// interval instead of after user traffic happens to retry — but it never
// dials: sources without an established connection stay lazy.
func (p *Pool) RefreshTips(ctx context.Context) {
	p.mu.RLock()
	interval := p.tipInterval
	stop := p.stopTips
	p.mu.RUnlock()
	if stop == nil {
		return // tip sampling not enabled
	}

	// The probe timeout is bounded independently of the poll interval: a
	// rarely-polled fleet (WithTipTracking with an hour-scale interval) must
	// not turn one manual RefreshTips call into an hour-long block.
	probeTimeout := interval
	if probeTimeout > maxTipProbeTimeout {
		probeTimeout = maxTipProbeTimeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	emissions, _ := p.collect(probeCtx, "eth_blockNumber", nil, CollectOptions{}, collectPlan{
		warmOnly: true,
	})
	for _, em := range emissions {
		if em.Err != nil || !em.Attempted {
			continue
		}
		height, err := parseTipValue(em.Val)
		if err != nil {
			continue
		}
		em.Src.recordTip(height)
	}
}

// tipReference is the scheduling reference derived from the fleet's fresh
// tip samples. Unknown samples — never observed, or older than the configured
// validity window — contribute no height and sort below every fresh sample.
// That makes a nonzero floor require a majority of the full basis to carry
// fresh samples at or above the reference: a minority of high samples cannot
// shrink the sample population enough to pause honest members.
type tipReference struct {
	floor     uint64
	reference uint64
	fresh     int
	unknown   int
	maxAge    time.Duration
}

// tipReference returns the height below which a source counts as lagging, or
// a zero floor when the gate is off, no fresh sample exists, or the reference
// sits within the bound of genesis. The reference is the lower median over
// the whole basis population, with unknown samples contributing zero: the
// participation-shrinking defense of the quorum layer extends to eligibility.
func (p *Pool) tipReference(sources []*Source, now time.Time) tipReference {
	ref := tipReference{maxAge: p.tipSampleMaxAge}
	if p.maxTipLag == 0 || len(sources) == 0 {
		return ref
	}
	heights := make([]uint64, len(sources))
	for i, src := range sources {
		tip, _, ok := src.freshTip(now, p.tipSampleMaxAge)
		if !ok {
			ref.unknown++
			continue
		}
		heights[i] = tip
		ref.fresh++
	}
	sort.Slice(heights, func(i, j int) bool { return heights[i] < heights[j] })
	reference := heights[(len(heights)-1)/2]
	ref.reference = reference
	if reference <= p.maxTipLag {
		return ref
	}
	ref.floor = reference - p.maxTipLag
	return ref
}

// parseTipValue accepts the wire forms of eth_blockNumber results: the
// canonical hexadecimal quantity string, and plain JSON numbers (in-process
// test transports).
func parseTipValue(value json.RawMessage) (uint64, error) {
	var quantity string
	if err := json.Unmarshal(value, &quantity); err == nil {
		digits := quantity
		base := 10
		if len(digits) > 2 && digits[:2] == "0x" {
			digits, base = digits[2:], 16
		}
		height, err := strconv.ParseUint(digits, base, 64)
		if err != nil {
			return 0, fmt.Errorf("pool: invalid block tip %q", quantity)
		}
		return height, nil
	}
	var numeric uint64
	if err := json.Unmarshal(value, &numeric); err != nil {
		return 0, fmt.Errorf("pool: invalid block tip %s", value)
	}
	return numeric, nil
}
