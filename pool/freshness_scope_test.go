package pool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

// Migration baseline, not the intended historical/pending contract. Replace
// these expectations when the coordinator supplies request freshness scope.
func TestVotingTipGateCurrentRequestScope(t *testing.T) {
	for _, tc := range []struct {
		name, method string
		params       []any
	}{
		{"latest", "eth_getBalance", []any{"0x1", "latest"}},
		{"safe", "eth_getBalance", []any{"0x1", "safe"}},
		{"finalized", "eth_getBalance", []any{"0x1", "finalized"}},
		{"historical height", "eth_getBalance", []any{"0x1", "0x10"}},
		{"historical hash", "eth_getBalance", []any{"0x1", map[string]any{"blockHash": "0xabc"}}},
		{"pending", "eth_getBalance", []any{"0x1", "pending"}},
		{"stateless", "eth_chainId", nil},
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
			emissions, err := p.CollectVoting(ctx, tc.method, tc.params, CollectOptions{Sources: sources})
			if err != nil || len(emissions) != 3 {
				t.Fatalf("basis = %+v, err = %v", emissions, err)
			}
			for i, emission := range emissions {
				calls, _, _ := peers[i].counts()
				if i == 2 {
					if emission.Attempted || !errors.Is(emission.Err, ErrSourceGated) || calls != 0 {
						t.Fatalf("lagging member = %+v, wire calls = %d", emission, calls)
					}
					if h := sources[i].Health(); h.Successes != 0 || h.Failures != 0 {
						t.Fatalf("gate changed health: %+v", h)
					}
				} else if !emission.Attempted || emission.Err != nil || calls != 1 {
					t.Fatalf("healthy member = %+v, wire calls = %d", emission, calls)
				}
			}
		})
	}
}

// This records two current limitations for the sample-validity migration:
// age is ignored and the median population is sampled sources, not the basis.
func TestTipFloorCurrentSampleCoverageAndAge(t *testing.T) {
	p := NewPoolWithOptions(0, WithTipTracking(time.Hour, 16))
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
	floor := p.staleFloor(sources)
	if floor != 9984 || !errors.Is(votingGate(sources[0], floor, time.Now()), ErrSourceGated) {
		t.Fatalf("partial/aged sample baseline changed: floor = %d", floor)
	}
	if gate := votingGate(sources[3], floor, time.Now()); gate != nil {
		t.Fatalf("unsampled member unexpectedly gated: %v", gate)
	}
}
