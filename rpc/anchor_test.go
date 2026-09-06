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

func TestAnchoredHeightRewritesToHexHeight(t *testing.T) {
	calls := 0
	p := WithAnchorHeight(func(context.Context) (uint64, error) {
		calls++
		return 4660, nil
	}, 1)(Strict(CanonicalJSON))

	prepared, err := p.Prepare(context.Background(), Request{Method: "eth_feeHistory", Params: []any{uint64(10), "latest", []float64{25, 75}}})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Params[1] != "0x1234" {
		t.Fatalf("moving tag not rewritten to hex height: %v", prepared.Params)
	}
	if prepared.Params[0] != uint64(10) {
		t.Fatalf("block count changed: %v", prepared.Params[0])
	}
	if calls != 1 {
		t.Fatalf("height consulted %d times for one request", calls)
	}

	prepared, err = p.Prepare(context.Background(), Request{Method: "eth_feeHistory", Params: []any{uint64(10), "0x30", nil}})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Params[1] != "0x30" {
		t.Fatalf("explicit height rewritten: %v", prepared.Params)
	}
	if calls != 1 {
		t.Fatalf("pass-through consulted the height")
	}
}

func TestAnchoredHeightFieldsRewriteFilterBounds(t *testing.T) {
	calls := 0
	p := WithAnchorHeightFields(func(context.Context) (uint64, error) {
		calls++
		return 4660, nil
	}, 0, "fromBlock", "toBlock")(Strict(CanonicalJSON))

	t.Run("moving fields rewrite to the hex height and share one establishment", func(t *testing.T) {
		prepared, err := p.Prepare(context.Background(), Request{Method: "eth_getLogs", Params: []any{
			map[string]any{"fromBlock": "latest", "toBlock": "finalized", "address": "0x1"},
		}})
		if err != nil {
			t.Fatal(err)
		}
		filtered := prepared.Params[0].(map[string]any)
		if filtered["fromBlock"] != "0x1234" || filtered["toBlock"] != "0x1234" {
			t.Fatalf("filter bounds not pinned to the hex height: %v", filtered)
		}
		if filtered["address"] != "0x1" {
			t.Fatalf("untouched field changed: %v", filtered)
		}
		if calls != 1 {
			t.Fatalf("height consulted %d times for one request", calls)
		}
	})

	t.Run("absent fields default to the verified height", func(t *testing.T) {
		prepared, err := p.Prepare(context.Background(), Request{Method: "eth_getLogs", Params: []any{
			map[string]any{"topics": []any{"0xtopic"}},
		}})
		if err != nil {
			t.Fatal(err)
		}
		filtered := prepared.Params[0].(map[string]any)
		if filtered["fromBlock"] != "0x1234" || filtered["toBlock"] != "0x1234" {
			t.Fatalf("missing bounds not defaulted: %v", filtered)
		}
	})

	t.Run("explicit numbers and blockHash pins pass through", func(t *testing.T) {
		before := calls
		prepared, err := p.Prepare(context.Background(), Request{Method: "eth_getLogs", Params: []any{
			map[string]any{"fromBlock": "0x10", "toBlock": "0x20"},
		}})
		if err != nil {
			t.Fatal(err)
		}
		filtered := prepared.Params[0].(map[string]any)
		if filtered["fromBlock"] != "0x10" || filtered["toBlock"] != "0x20" {
			t.Fatalf("explicit bounds rewritten: %v", filtered)
		}
		prepared, err = p.Prepare(context.Background(), Request{Method: "eth_getLogs", Params: []any{
			map[string]any{"blockHash": "0xabc"},
		}})
		if err != nil {
			t.Fatal(err)
		}
		if prepared.Params[0].(map[string]any)["blockHash"] != "0xabc" {
			t.Fatalf("blockHash pin changed: %v", prepared.Params[0])
		}
		if calls != before {
			t.Fatalf("pass-through consulted the height")
		}
	})

	t.Run("non-object params pass through untouched", func(t *testing.T) {
		before := calls
		if _, err := p.Prepare(context.Background(), Request{Method: "eth_getLogs", Params: []any{"latest"}}); err != nil {
			t.Fatal(err)
		}
		if calls != before {
			t.Fatalf("non-object param consulted the height")
		}
	})

	t.Run("blockHash mixed with a range field fails before any upstream contact", func(t *testing.T) {
		before := calls
		_, err := p.Prepare(context.Background(), Request{Method: "eth_getLogs", Params: []any{
			map[string]any{"blockHash": "0xabc", "fromBlock": "latest"},
		}})
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("expected invalid-request rejection, got %v", err)
		}
		if calls != before {
			t.Fatalf("contradictory filter consulted the height: %d", calls)
		}
	})
}

func TestPrepareDecoratorsPreserveRPCErrorDomain(t *testing.T) {
	decorators := map[string]func(Policy) Policy{
		"hash":    WithAnchor(nil, 1),
		"height":  WithAnchorHeight(nil, 1),
		"fields":  WithAnchorHeightFields(nil, 0, "fromBlock", "toBlock"),
		"pending": RejectPending(1),
		"stacked": func(p Policy) Policy { return RejectPending(1)(WithAnchor(nil, 1)(p)) },
	}
	for name, decorate := range decorators {
		t.Run(name, func(t *testing.T) {
			for _, enabled := range []bool{false, true} {
				var opts []StrictOption
				if enabled {
					opts = append(opts, WithRPCErrorBodies())
				}
				policy := decorate(Strict(CanonicalJSON, opts...))
				capability, ok := policy.(RPCErrorPolicy)
				if !ok || capability.IncludeRPCError() != enabled {
					t.Fatalf("error domain lost through decorator (enabled=%v)", enabled)
				}
			}
		})
	}
}
