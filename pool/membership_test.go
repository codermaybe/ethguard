package pool

import (
	"context"
	"errors"
	"testing"
)

// NewWithMembers is the only membership admission path: it validates the
// configured set once and the pool never changes it afterwards.
func TestNewWithMembersAdmitsFixedMembership(t *testing.T) {
	p, err := NewWithMembers(0, []Member{
		{Name: "a", URL: "https://a.example"},
		{Name: "b", URL: "https://b.example"},
	})
	if err != nil {
		t.Fatalf("NewWithMembers: %v", err)
	}
	defer p.Close()

	if got := p.Len(); got != 2 {
		t.Fatalf("Len = %d, want 2", got)
	}
	sources := p.Sources()
	if len(sources) != 2 {
		t.Fatalf("Sources = %d, want 2", len(sources))
	}
	for _, src := range sources {
		resolved, ok := p.SourceByID(src.ID())
		if !ok || resolved != src {
			t.Fatalf("SourceByID(%q) = %v, %v; want the configured object", src.ID(), resolved, ok)
		}
	}
	if _, ok := p.SourceByID("https://missing.example"); ok {
		t.Fatal("SourceByID resolved an unconfigured endpoint")
	}

	sources[0] = nil
	if p.Sources()[0] == nil {
		t.Fatal("Sources leaked the pool's member slice")
	}
}

func TestNewWithMembersDeduplicatesEndpointsAndRejectsNameCollisions(t *testing.T) {
	p, err := NewWithMembers(0, []Member{
		{Name: "a", URL: "https://a.example"},
		{Name: "other", URL: "https://a.example/"},
	})
	if err != nil {
		t.Fatalf("NewWithMembers duplicate endpoint: %v", err)
	}
	if got := p.Len(); got != 1 {
		t.Fatalf("Len = %d, want endpoint identity to collapse the duplicate", got)
	}
	p.Close()

	if _, err := NewWithMembers(0, []Member{
		{Name: "a", URL: "https://a.example"},
		{Name: "a", URL: "https://b.example"},
	}); err == nil {
		t.Fatal("expected duplicate source name to fail construction")
	}
	if _, err := NewWithMembers(0, []Member{{Name: "bad", URL: "ftp://example.com"}}); err == nil {
		t.Fatal("expected an unsupported URL scheme to fail construction")
	}
}

func TestClosedPoolKeepsConfiguredMembersAddressable(t *testing.T) {
	p, err := NewWithMembers(0, []Member{{Name: "a", URL: "https://a.example"}})
	if err != nil {
		t.Fatalf("NewWithMembers: %v", err)
	}
	src := p.VotingMembers()[0]
	p.Close()

	if got, ok := p.SourceByID(src.ID()); !ok || got != src {
		t.Fatalf("SourceByID after Close = %v, %v; want the closed member", got, ok)
	}
	if !src.isClosed() {
		t.Fatal("expected Close to close the configured member")
	}
	if _, err := p.CollectVoting(context.Background(), "eth_blockNumber", nil, CollectOptions{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("CollectVoting after Close = %v, want ErrClosed", err)
	}
}
