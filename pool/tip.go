package pool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"
)

// Tip-tracking defaults. The poll interval is deliberately low-rate: probes
// ride the ordinary collection path and must never compete with traffic.
const (
	defaultTipInterval = 10 * time.Second
	defaultMaxTipLag   = 32
)

// PoolOption tunes a Pool at construction time.
type PoolOption func(*poolConfig)

type poolConfig struct {
	tipEnabled  bool
	tipInterval time.Duration
	maxTipLag   uint64
}

// WithTipTracking enables block-tip sampling and the freshness eligibility
// gate: every interval the pool probes eth_blockNumber across sources, and
// requests skip sources whose tip trails the fleet's robust reference height
// by more than maxBehind blocks. A zero interval or lag selects the default
// (10s / 32 blocks). Probing reuses the ordinary collection path, so it
// shares connections, chain-ID checks, and health accounting; it bypasses
// the freshness gate itself, so a fully stale fleet can still recover.
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

// NewPoolWithOptions creates an empty pool tuned by opts.
func NewPoolWithOptions(chainID uint64, opts ...PoolOption) *Pool {
	return newPoolWithDialerAndOptions(standardDialer{}, chainID, opts...)
}

// newPoolWithDialerAndOptions lets tests combine an injected transport with
// option tuning.
func newPoolWithDialerAndOptions(d dialer, chainID uint64, opts ...PoolOption) *Pool {
	cfg := poolConfig{
		tipInterval: defaultTipInterval,
		maxTipLag:   defaultMaxTipLag,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	p := newPoolWithDialerAndChainID(d, chainID)
	p.maxTipLag = cfg.maxTipLag
	if cfg.tipEnabled {
		p.tipIntervalRef = cfg.tipInterval
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
// called manually to force an immediate sample. Per-source results land in
// Health().TipHeight regardless of whether the freshness gate is enabled.
//
// The probe bypasses the freshness gate on purpose: a fleet that drifted
// behind must still be able to refresh its own picture of "behind". It also
// ignores cooldown circuits, so recovery is detected proactively within one
// interval instead of after user traffic happens to retry — but it never
// dials: sources without an established connection stay lazy.
func (p *Pool) RefreshTips(ctx context.Context) {
	p.mu.RLock()
	interval := p.tipIntervalRef
	stop := p.stopTips
	p.mu.RUnlock()
	if stop == nil {
		return // tip sampling not enabled
	}

	probeCtx, cancel := context.WithTimeout(ctx, interval)
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

// staleFloor returns the height below which a source counts as lagging, or
// zero when the gate is off, no tip has been sampled yet, or the reference
// sits within the bound of genesis. The reference is the lower median of the
// fleet's tips: a minority of providers lying about their height cannot move
// it, so they cannot weaponize the gate to vote honest sources out of the
// fan-out — the participation-shrinking defense of the quorum layer extends
// to eligibility.
func (p *Pool) staleFloor(sources []*Source) uint64 {
	if p.maxTipLag == 0 {
		return 0
	}
	tips := make([]uint64, 0, len(sources))
	for _, src := range sources {
		if tip, ok := src.tipHeight(); ok {
			tips = append(tips, tip)
		}
	}
	if len(tips) == 0 {
		return 0
	}
	sort.Slice(tips, func(i, j int) bool { return tips[i] < tips[j] })
	reference := tips[(len(tips)-1)/2]
	if reference <= p.maxTipLag {
		return 0
	}
	return reference - p.maxTipLag
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
