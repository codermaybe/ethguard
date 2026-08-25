package ethguard_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"ethguard"
)

type stubResolver struct {
	calls int
	hash  string
	err   error
}

func (s *stubResolver) AnchorBlockHash(context.Context) (string, error) {
	s.calls++
	return s.hash, s.err
}

func TestCachedAnchorReusesReferenceWithinTTL(t *testing.T) {
	inner := &stubResolver{hash: "0xabc"}
	cached := ethguard.NewCachedAnchor(inner, time.Hour)
	ctx := context.Background()

	for range 5 {
		hash, err := cached.AnchorBlockHash(ctx)
		if err != nil || hash != "0xabc" {
			t.Fatalf("unexpected result %q %v", hash, err)
		}
	}
	if inner.calls != 1 {
		t.Fatalf("expected one underlying resolution, got %d", inner.calls)
	}
}

func TestCachedAnchorExpiresAfterTTL(t *testing.T) {
	inner := &stubResolver{hash: "0xabc"}
	cached := ethguard.NewCachedAnchor(inner, 20*time.Millisecond)
	ctx := context.Background()

	if _, err := cached.AnchorBlockHash(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	if _, err := cached.AnchorBlockHash(ctx); err != nil {
		t.Fatal(err)
	}
	if inner.calls != 2 {
		t.Fatalf("expected the expired reference to re-establish, got %d resolutions", inner.calls)
	}
}

func TestCachedAnchorDoesNotCacheFailures(t *testing.T) {
	inner := &stubResolver{err: errors.New("heads diverge")}
	cached := ethguard.NewCachedAnchor(inner, time.Hour)
	ctx := context.Background()

	if _, err := cached.AnchorBlockHash(ctx); err == nil {
		t.Fatal("expected the inner error to surface")
	}
	inner.err = nil
	inner.hash = "0xabc"
	if _, err := cached.AnchorBlockHash(ctx); err != nil {
		t.Fatalf("expected retry after failed establishment, got %v", err)
	}
	if inner.calls != 2 {
		t.Fatalf("expected the failure not to be cached, got %d resolutions", inner.calls)
	}
}
