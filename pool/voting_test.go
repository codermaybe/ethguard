package pool

import (
	"context"
	"errors"
	"testing"
	"time"
)

// --- CollectVoting: the fixed voting-basis collection path (decision 0027) ---

func TestCollectVotingReturnsOneEmissionPerMember(t *testing.T) {
	d := &newFakeDialer{dials: make(map[string]int)}
	p := newPoolWithDialerAndChainID(d, 0)
	for _, name := range []string{"a", "b", "c"} {
		if _, err := p.Join(name, "https://"+name+".example"); err != nil {
			t.Fatal(err)
		}
	}

	ems, err := p.CollectVoting(context.Background(), "eth_blockNumber", nil, CollectOptions{})
	if err != nil {
		t.Fatalf("CollectVoting: %v", err)
	}
	if len(ems) != 3 {
		t.Fatalf("expected one emission per member, got %d", len(ems))
	}
	for i, em := range ems {
		if em.Err != nil || !em.Attempted {
			t.Fatalf("emission %d not attempted cleanly: %+v", i, em)
		}
	}
}

// A member whose cooldown circuit is open is not requested and receives no
// health penalty (decision 0027): gating is a pause, not an observation. The
// shared failing method drives both members onto the circuit together, so
// the final collection proves denominator preservation at the boundary: the
// fan-out is impossible (both gated), no upstream was contacted, and no
// health accounting changed.
func TestCollectVotingKeepsCoolingMemberInBasis(t *testing.T) {
	d := &newFakeDialer{dials: make(map[string]int)}
	p := newPoolWithDialerAndChainID(d, 0)
	src, err := p.Join("a", "https://a.example")
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Join("b", "https://b.example")
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < eligibleFailureThreshold; i++ {
		ems, err := p.CollectVoting(context.Background(), "eth_protocolFailure", nil, CollectOptions{})
		if err != nil {
			t.Fatalf("failure round %d: %v", i+1, err)
		}
		if len(ems) != 2 || ems[0].Err == nil || ems[1].Err == nil {
			t.Fatalf("failure round %d: expected both members to fail, got %+v", i+1, ems)
		}
	}
	if src.eligible(time.Now()) || b.eligible(time.Now()) {
		t.Fatal("expected both circuits to open")
	}
	healthBefore := src.Health().Failures

	ems, err := p.CollectVoting(context.Background(), "eth_blockNumber", nil, CollectOptions{})
	if !errors.Is(err, ErrNoEligibleSource) {
		t.Fatalf("expected both members gated to be impossible fan-out, got %v", err)
	}
	if len(ems) != 2 || ems[0].Src != src || ems[1].Src != b {
		t.Fatalf("expected captured members even when all gated, got %+v", ems)
	}
	for _, em := range ems {
		if em.Attempted || !errors.Is(em.Err, ErrSourceGated) {
			t.Fatalf("gated emission must carry no attempt: %+v", em)
		}
	}
	if got := src.Health().Failures; got != healthBefore {
		t.Fatalf("gating must not change health accounting: %d -> %d", healthBefore, got)
	}
}

// Every member gated yields the impossible-fan-out sentinel: no upstream was
// contacted, and the caller learns the basis still exists but cannot vote.
// The pool has no members, so every request is impossible from the start —
// and separately, a member that fails its identity check is gated without an
// upstream round-trip.
func TestCollectVotingAllGatedIsNoEligibleSource(t *testing.T) {
	t.Run("empty pool", func(t *testing.T) {
		p := newPoolWithDialerAndChainID(&newFakeDialer{dials: make(map[string]int)}, 0)
		if _, err := p.CollectVoting(context.Background(), "eth_blockNumber", nil, CollectOptions{}); !errors.Is(err, ErrNoEligibleSource) {
			t.Fatalf("expected no-eligible-source for an empty basis, got %v", err)
		}
	})

	t.Run("identity-gated member", func(t *testing.T) {
		d := &newFakeDialer{dials: make(map[string]int)}
		p := newPoolWithDialerAndChainID(d, 5) // pool expects chain 5, node answers 1
		if _, err := p.Join("a", "https://a.example"); err != nil {
			t.Fatal(err)
		}
		// First contact verifies identity against the wrong answer and
		// quarantines the member.
		if _, err := p.CollectVoting(context.Background(), "eth_blockNumber", nil, CollectOptions{}); !errors.Is(err, ErrChainIDMismatch) {
			t.Fatalf("expected chain mismatch, got %v", err)
		}
		if _, err := p.CollectVoting(context.Background(), "eth_blockNumber", nil, CollectOptions{}); !errors.Is(err, ErrNoEligibleSource) {
			t.Fatalf("expected no-eligible-source when every member is gated, got %v", err)
		}
	})
}

func TestCollectVotingRejectsFixedMembershipOption(t *testing.T) {
	p := newPoolWithDialerAndChainID(&newFakeDialer{dials: make(map[string]int)}, 0)
	if _, err := p.Join("a", "https://a.example"); err != nil {
		t.Fatal(err)
	}
	_, err := p.CollectVoting(context.Background(), "eth_blockNumber", nil, CollectOptions{SourceIDs: []string{"https://a.example"}})
	if err == nil {
		t.Fatal("expected SourceIDs rejection: the voting basis is pool-managed")
	}
}

// --- Remove: explicit membership management (decision 0027) ------------------

func TestPoolRemoveDropsMember(t *testing.T) {
	d := &newFakeDialer{dials: make(map[string]int)}
	p := newPoolWithDialerAndChainID(d, 0)
	if _, err := p.Join("a", "https://a.example"); err != nil {
		t.Fatal(err)
	}
	src, err := p.Join("b", "https://b.example")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Remove("b"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got := p.Len(); got != 1 {
		t.Fatalf("expected one remaining member, got %d", got)
	}
	if src.State() != SourceDown {
		t.Fatal("expected removed source to be closed")
	}
	if _, err := p.Collect(context.Background(), "eth_blockNumber", nil, CollectOptions{}); err != nil {
		t.Fatalf("remaining member should answer: %v", err)
	}
	if err := p.Remove("b"); err == nil {
		t.Fatal("expected removing an unknown member to fail")
	}
}

// A source re-added under the same URL is a new member, not a resurrection:
// identity dedup keys on currently registered members only.
func TestPoolRemoveThenRejoinIsANewMember(t *testing.T) {
	d := &newFakeDialer{dials: make(map[string]int)}
	p := newPoolWithDialerAndChainID(d, 0)
	first, err := p.Join("a", "https://a.example")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Remove("a"); err != nil {
		t.Fatal(err)
	}
	second, err := p.Join("a", "https://a.example")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("rejoined URL must not resurrect the removed member")
	}
}
