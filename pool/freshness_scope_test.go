package pool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

// TestVotingTipGateRequestScope proves the gate follows the request's declared
// scheduling scope: only a latest-tip request consults sampled latest tips.
// Explicit heights/hashes, pending, stateless methods, and safe/finalized
// reads are not paused by a lagging latest sample.
func TestVotingTipGateRequestScope(t *testing.T) {
	for _, tc := range []struct {
		name      string
		method    string
		params    []any
		scope     TipFreshnessScope
		wantGated bool
	}{
		{"latest", "eth_getBlockByNumber", []any{"latest", false}, TipFreshnessLatest, true},
		{"safe", "eth_getBlockByNumber", []any{"safe", false}, TipFreshnessExempt, false},
		{"finalized", "eth_getBlockByNumber", []any{"finalized", false}, TipFreshnessExempt, false},
		{"historical height", "eth_getBalance", []any{"0x1", "0x10"}, TipFreshnessExempt, false},
		{"historical hash", "eth_getBalance", []any{"0x1", map[string]any{"blockHash": "0xabc"}}, TipFreshnessExempt, false},
		{"pending", "eth_getBalance", []any{"0x1", "pending"}, TipFreshnessExempt, false},
		{"stateless", "eth_chainId", nil, TipFreshnessExempt, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			members := make([]Member, 3)
			peers := make([]*contractTransport, 3)
			for i := range members {
				peers[i] = &contractTransport{result: json.RawMessage(`"0x1"`)}
				members[i] = Member{Name: fmt.Sprintf("source-%d", i), URL: fmt.Sprintf("https://source-%d.example", i), Dialer: &contractDialer{transport: peers[i]}}
			}
			p, err := NewWithMembers(0, members, WithTipTracking(time.Hour, 16))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(p.Close)
			sources := p.VotingMembers()
			for i, height := range []uint64{100, 100, 10} {
				sources[i].recordTip(height)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			emissions, err := p.CollectVoting(ctx, tc.method, tc.params, CollectOptions{
				Sources:      sources,
				TipFreshness: tc.scope,
			})
			if err != nil || len(emissions) != 3 {
				t.Fatalf("basis = %+v, err = %v", emissions, err)
			}
			calls, _, _ := peers[2].counts()
			if tc.wantGated {
				if emission := emissions[2]; emission.Attempted || !errors.Is(emission.Err, ErrSourceGated) || calls != 0 {
					t.Fatalf("lagging member = %+v, wire calls = %d", emission, calls)
				}
				if h := sources[2].Health(); h.Successes != 0 || h.Failures != 0 {
					t.Fatalf("gate changed health: %+v", h)
				}
				return
			}
			if emission := emissions[2]; !emission.Attempted || emission.Err != nil || calls != 1 {
				t.Fatalf("exempt member = %+v, wire calls = %d", emission, calls)
			}
		})
	}
}

// TestVotingTipGateZeroScopePreservesLegacyGateAll pins the documented
// zero-value behavior for direct pool callers: a collection that does not
// declare request semantics keeps the historical gate-all behavior. The
// production aggregator and reference rounds always set an explicit scope.
func TestVotingTipGateZeroScopePreservesLegacyGateAll(t *testing.T) {
	p := NewPoolWithOptions(0, WithTipTracking(time.Hour, 16))
	t.Cleanup(p.Close)
	for i := range 3 {
		if _, err := p.join(fmt.Sprintf("source-%d", i), fmt.Sprintf("https://source-%d.example", i)); err != nil {
			t.Fatal(err)
		}
	}
	sources := p.VotingMembers()
	for i, height := range []uint64{100, 100, 10} {
		sources[i].recordTip(height)
	}
	emissions, _ := p.CollectVoting(context.Background(), "eth_getBalance", []any{"0x1", "0x10"}, CollectOptions{Sources: sources})
	if len(emissions) != 3 {
		t.Fatalf("basis = %+v", emissions)
	}
	if emission := emissions[2]; emission.Attempted || !errors.Is(emission.Err, ErrSourceGated) {
		t.Fatalf("zero scope did not preserve the legacy gate: %+v", emission)
	}
}

// TestScopedGateDoesNotBypassCooldownOrIdentity proves the tip scope is only a
// tip scope: an exempt request still respects the source's cooldown circuit
// and its failed chain identity.
func TestScopedGateDoesNotBypassCooldownOrIdentity(t *testing.T) {
	t.Run("cooldown", func(t *testing.T) {
		peer := &contractTransport{result: json.RawMessage(`"0x1"`), err: errors.New("transport reset")}
		p, err := NewWithMembers(0, []Member{{
			Name:   "source-0",
			URL:    "https://source-0.example",
			Dialer: &contractDialer{transport: peer},
		}}, WithTipTracking(time.Hour, 16))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		source := p.VotingMembers()[0]
		source.recordTip(100)
		for range eligibleFailureThreshold {
			_, _ = p.CollectVoting(context.Background(), "eth_blockNumber", nil, CollectOptions{TipFreshness: TipFreshnessExempt})
		}
		emissions, err := p.CollectVoting(context.Background(), "eth_blockNumber", nil, CollectOptions{TipFreshness: TipFreshnessExempt})
		if !errors.Is(err, ErrNoEligibleSource) {
			t.Fatalf("expected cooling source to gate the fan-out, got %v", err)
		}
		if len(emissions) != 1 || emissions[0].Attempted || !errors.Is(emissions[0].Err, ErrSourceGated) {
			t.Fatalf("cooldown was bypassed: %+v", emissions)
		}
	})

	t.Run("identity", func(t *testing.T) {
		d := &newFakeDialer{dials: make(map[string]int)}
		p := newPoolWithDialerAndChainID(d, 5) // node answers chain 1
		t.Cleanup(p.Close)
		if _, err := p.join("source-0", "https://source-0.example"); err != nil {
			t.Fatal(err)
		}
		source := p.VotingMembers()[0]
		source.recordTip(100)
		if _, err := p.CollectVoting(context.Background(), "eth_blockNumber", nil, CollectOptions{TipFreshness: TipFreshnessExempt}); !errors.Is(err, ErrChainIDMismatch) {
			t.Fatalf("first identity check = %v, want chain mismatch", err)
		}
		emissions, err := p.CollectVoting(context.Background(), "eth_blockNumber", nil, CollectOptions{TipFreshness: TipFreshnessExempt})
		if !errors.Is(err, ErrNoEligibleSource) {
			t.Fatalf("expected identity-invalid source to gate the fan-out, got %v", err)
		}
		if len(emissions) != 1 || emissions[0].Attempted || !errors.Is(emissions[0].Err, ErrChainIDMismatch) {
			t.Fatalf("identity gate was bypassed: %+v", emissions)
		}
	})
}

// TestTipReferenceRequiresFreshBasisCoverage proves expired samples are
// unknown and a minority of fresh high samples cannot manufacture a floor
// that pauses another member.
func TestTipReferenceRequiresFreshBasisCoverage(t *testing.T) {
	p := NewPoolWithOptions(0, WithTipTracking(time.Hour, 16), WithTipSampleMaxAge(time.Minute))
	t.Cleanup(p.Close)
	for i := range 5 {
		if _, err := p.join(fmt.Sprintf("source-%d", i), fmt.Sprintf("https://source-%d.example", i)); err != nil {
			t.Fatal(err)
		}
	}
	sources := p.VotingMembers()
	for i, height := range []uint64{100, 10000, 10000} {
		sources[i].recordTip(height)
		sources[i].stats.mu.Lock()
		sources[i].stats.tipObservedAt = time.Now().Add(-24 * time.Hour)
		sources[i].stats.mu.Unlock()
	}
	now := time.Now()
	reference := p.tipReference(sources, now)
	if reference.floor != 0 || reference.fresh != 0 || reference.unknown != len(sources) {
		t.Fatalf("expired samples affected reference: %+v", reference)
	}
	for _, source := range sources {
		if gate := votingGate(source, reference, now); gate != nil {
			t.Fatalf("unknown sample unexpectedly gated source: %v", gate)
		}
	}

	sources[1].recordTip(10000)
	sources[2].recordTip(10000)
	reference = p.tipReference(sources, now)
	if reference.floor != 0 || reference.fresh != 2 || reference.unknown != 3 {
		t.Fatalf("minority fresh coverage manufactured a floor: %+v", reference)
	}
}
