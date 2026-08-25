package rpc

import (
	"context"
	"errors"
	"testing"
)

func TestAnchoredRewritesMovingTags(t *testing.T) {
	calls := 0
	anchor := func(context.Context) (string, error) {
		calls++
		return "0xabc", nil
	}
	p := Anchored(1, CanonicalJSON, anchor)

	for _, tag := range []string{"latest", "safe", "finalized", "LATEST"} {
		prepared, err := p.Prepare(context.Background(), Request{
			Method: "eth_getBalance",
			Params: []any{"0xaddr", tag},
		})
		if err != nil {
			t.Fatal(err)
		}
		if prepared.Params[1] != "0xabc" {
			t.Fatalf("tag %q not rewritten: %v", tag, prepared.Params[1])
		}
	}

	// The caller's original params must stay untouched.
	req := Request{Method: "eth_getBalance", Params: []any{"0xaddr", "latest"}}
	p.Prepare(context.Background(), req)
	if req.Params[1] != "latest" {
		t.Fatalf("caller params mutated: %v", req.Params)
	}
	if calls != 5 {
		t.Fatalf("expected five anchor resolutions, got %d", calls)
	}
}

func TestAnchoredPassesExplicitReferencesThrough(t *testing.T) {
	calls := 0
	anchor := func(context.Context) (string, error) {
		calls++
		return "0xanchor", nil
	}
	p := Anchored(1, CanonicalJSON, anchor)

	for _, ref := range []any{"0x1234", "pending", 42} {
		prepared, err := p.Prepare(context.Background(), Request{
			Method: "m",
			Params: []any{"x", ref},
		})
		if err != nil {
			t.Fatal(err)
		}
		if prepared.Params[1] != ref {
			t.Fatalf("explicit reference %v rewritten to %v", ref, prepared.Params[1])
		}
	}
	if calls != 0 {
		t.Fatalf("anchor consulted %d times for explicit references", calls)
	}
}

func TestAnchoredPropagatesAnchorFailure(t *testing.T) {
	anchor := func(context.Context) (string, error) {
		return "", errors.New("heads diverge")
	}
	p := Anchored(0, CanonicalJSON, anchor)
	_, err := p.Prepare(context.Background(), Request{Method: "m", Params: []any{"latest"}})
	if err == nil || err.Error() != "anchor: heads diverge" {
		t.Fatalf("expected wrapped anchor error, got %v", err)
	}
}
