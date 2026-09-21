package pool

import (
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
