package rpc

import (
	"context"
	"encoding/json"
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

func TestWithAnchorDecoratesMedianAndRewritesMultiplePositionsOnce(t *testing.T) {
	calls := 0
	p := WithAnchor(func(context.Context) (string, error) {
		calls++
		return "0xanchor", nil
	}, 1, 2)(Median(func(v json.RawMessage) (string, error) { return string(v), nil }))
	req := Request{Method: "eth_getLogs", Params: []any{"filter", "latest", "safe"}}
	prepared, err := p.Prepare(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Params[1] != "0xanchor" || prepared.Params[2] != "0xanchor" {
		t.Fatalf("moving positions were not rewritten: %+v", prepared.Params)
	}
	if req.Params[1] != "latest" || req.Params[2] != "safe" {
		t.Fatalf("original params mutated: %+v", req.Params)
	}
	if calls != 1 {
		t.Fatalf("expected one anchor resolution, got %d", calls)
	}
	if got := p.(BlockRefProvider).BlockRef(req, prepared); got != "0xanchor" {
		t.Fatalf("BlockRef = %q, want anchor hash", got)
	}
}

func TestWithAnchorLeavesPendingAndExplicitReferencesUntouched(t *testing.T) {
	calls := 0
	p := WithAnchor(func(context.Context) (string, error) {
		calls++
		return "0xanchor", nil
	}, 0, 1)(Strict(CanonicalJSON))
	for _, params := range [][]any{{"pending", "0x1"}, {"0x1", "0xabc"}} {
		req := Request{Method: "m", Params: params}
		prepared, err := p.Prepare(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		for i := range params {
			if prepared.Params[i] != params[i] {
				t.Fatalf("explicit/pending param changed: before=%v after=%v", params, prepared.Params)
			}
		}
	}
	prepared, err := p.Prepare(context.Background(), Request{Method: "m", Params: []any{42, "latest"}})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Params[0] != 42 || prepared.Params[1] != "0xanchor" {
		t.Fatalf("moving tag not rewritten: %v", prepared.Params)
	}
	if calls != 1 {
		t.Fatalf("anchor consulted unexpectedly: %d", calls)
	}
}

func TestRejectPendingFailsBeforeResolution(t *testing.T) {
	calls := 0
	p := RejectPending(0)(WithAnchor(func(context.Context) (string, error) {
		calls++
		return "0xanchor", nil
	}, 0)(Strict(CanonicalJSON)))
	_, err := p.Prepare(context.Background(), Request{Method: "eth_getBlockReceipts", Params: []any{"pending"}})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected invalid request, got %v", err)
	}
	if calls != 0 {
		t.Fatalf("pending rejection consulted anchor: %d", calls)
	}
}
