package pool

import (
	"context"
	"errors"
	"testing"
)

// TestJoinMembershipContracts pins the registry rules Join and Remove share:
// names collide by display name, endpoints deduplicate by normalized URL, and
// unknown removals fail loudly. Join never dials, so no fake transport is
// needed here.
func TestJoinMembershipContracts(t *testing.T) {
	p := NewPool()
	defer p.Close()

	a, err := p.Join("a", "https://a.example")
	if err != nil {
		t.Fatalf("Join a: %v", err)
	}
	if p.Len() != 1 {
		t.Fatalf("Len = %d, want 1", p.Len())
	}

	// Same endpoint under another name returns the existing source: endpoint
	// identity wins, and the basis must not silently double-count one node.
	dup, err := p.Join("other", "https://a.example")
	if err != nil {
		t.Fatalf("Join same endpoint: %v", err)
	}
	if dup != a {
		t.Fatal("expected the same Source for a duplicate endpoint")
	}
	if p.Len() != 1 {
		t.Fatalf("Len = %d, want 1 after duplicate Join", p.Len())
	}

	// Distinct endpoint reusing a name collides on diagnostics identity.
	if _, err := p.Join("a", "https://b.example"); err == nil {
		t.Fatal("expected duplicate-name Join to fail")
	}

	if err := p.Remove("missing"); err == nil {
		t.Fatal("expected removal of an unknown source to fail")
	}
	if err := p.Remove("a"); err != nil {
		t.Fatalf("Remove a: %v", err)
	}
	if p.Len() != 0 {
		t.Fatalf("Len = %d, want 0 after Remove", p.Len())
	}
}

// A removed member stays addressable by identity while it is closed, and a
// later Join of the same endpoint registers a fresh, usable source — the
// invocations that captured the old object keep it, not the revival.
func TestRemovedMemberStaysResolvableForFrozenBases(t *testing.T) {
	p := NewPool()
	defer p.Close()

	src, err := p.Join("a", "https://a.example")
	if err != nil {
		t.Fatalf("Join a: %v", err)
	}
	id := src.ID()
	if err := p.Remove("a"); err != nil {
		t.Fatalf("Remove a: %v", err)
	}

	if p.Len() != 0 {
		t.Fatalf("Len = %d, want 0 after Remove", p.Len())
	}
	if len(p.Sources()) != 0 {
		t.Fatalf("Sources still lists the removed member: %v", p.Sources())
	}
	resolved, ok := p.SourceByID(id)
	if !ok || resolved != src {
		t.Fatal("SourceByID lost the removed member: a frozen basis cannot resolve it")
	}
	if !resolved.isClosed() {
		t.Fatal("the resolved removed member must be closed so its calls fail closed")
	}

	revived, err := p.Join("a", "https://a.example")
	if err != nil {
		t.Fatalf("re-Join: %v", err)
	}
	if revived == src {
		t.Fatal("re-Join must not resuscitate the closed object")
	}
	if again, ok := p.SourceByID(revived.ID()); !ok || again != revived {
		t.Fatal("SourceByID must resolve the new source after re-Join")
	}
	if revived.isClosed() {
		t.Fatal("the revived member must be usable")
	}
}

// A fan-out over an invocation's captured basis keeps a removed member in the
// denominator as a failed-closed emission: the member's slot is visible, not
// silently replaced or dropped.
func TestCollectVotingKeepsRemovedMemberFailClosed(t *testing.T) {
	p := NewPool()
	defer p.Close()

	sources := make([]*Source, 0, 2)
	for _, name := range []string{"a", "b"} {
		src, err := p.Join(name, "https://"+name+".example")
		if err != nil {
			t.Fatalf("Join %s: %v", name, err)
		}
		sources = append(sources, src)
	}
	if err := p.Remove("b"); err != nil {
		t.Fatalf("Remove b: %v", err)
	}
	if p.Len() != 1 {
		t.Fatalf("Len = %d, want the removal to unregister only b", p.Len())
	}

	emissions, collectErr := p.CollectVoting(context.Background(), "eth_getBlockByNumber",
		[]any{"latest", false}, CollectOptions{Sources: sources})
	if len(emissions) != 2 {
		t.Fatalf("emissions = %d, want the captured basis kept whole (2)", len(emissions))
	}
	byID := map[string]Emission{}
	for _, emission := range emissions {
		byID[emission.Src.ID()] = emission
	}
	failed, ok := byID[sources[1].ID()]
	if !ok {
		t.Fatalf("the removed member vanished from its basis slot: %v", emissions)
	}
	if !errors.Is(failed.Err, ErrClosed) || failed.Attempted {
		t.Fatalf("removed member emission = %+v, want a fail-closed ErrClosed emission", failed)
	}
	// The live member keeps its own slot; its call outcome (here: no dialer
	// configured) stays exactly the behavior of a fan-out over a snapshot.
	if _, ok := byID[sources[0].ID()]; !ok {
		t.Fatalf("the live member vanished from the fan-out: %v", emissions)
	}
	if collectErr == nil {
		t.Fatal("expected the fan-out verdict to report the removed member")
	}
}

func TestClosedPoolRejectsMembershipChanges(t *testing.T) {
	p := NewPool()
	if _, err := p.Join("a", "https://a.example"); err != nil {
		t.Fatalf("Join a: %v", err)
	}
	p.Close()
	p.Close() // idempotent

	if _, err := p.Join("b", "https://b.example"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Join after Close = %v, want ErrClosed", err)
	}
	if err := p.Remove("a"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Remove after Close = %v, want ErrClosed", err)
	}
}
