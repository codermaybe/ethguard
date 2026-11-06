package ethguard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

// lifecycleFleet builds a two-source client whose anchor establishment has
// one coherent answer.
func lifecycleFleet(t *testing.T) *Client {
	t.Helper()
	cfg := config.Config{ChainID: 1}
	var nodes []*rpctest.Node
	for i := 0; i < 2; i++ {
		node := rpctest.NewNode(1)
		t.Cleanup(node.Close)
		node.Handle("eth_getBlockByNumber", func(c rpctest.Call) (any, error) {
			var ref string
			if len(c.Params) > 0 {
				_ = json.Unmarshal(c.Params[0], &ref)
			}
			var full bool
			if len(c.Params) > 1 {
				_ = json.Unmarshal(c.Params[1], &full)
			}
			if full || ref != "latest" {
				return nil, rpctest.RPCError(-32602, "unexpected anchor probe "+ref)
			}
			return map[string]any{"number": "0x10", "hash": "0x1111111111111111111111111111111111111111111111111111111111111111"}, nil
		})
		nodes = append(nodes, node)
		cfg.Upstreams = append(cfg.Upstreams, config.Upstream{
			ID:       fmt.Sprintf("node-%c", 'a'+i),
			Endpoint: node.URL(),
		})
	}
	client, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client
}

// TestEstablishAnchorNeverTouchesCapture locks the sub-operation rule: the
// private anchor execution delivers evidence explicitly and never binds,
// consumes, or publishes on a capture owned by an outer operation.
func TestEstablishAnchorNeverTouchesCapture(t *testing.T) {
	client := lifecycleFleet(t)
	ctx, capture := WithResult(context.Background())
	ref, err := client.establishAnchor(ctx, "")
	if err != nil {
		t.Fatalf("establish: %v", err)
	}
	if ref.Status != rpc.Corroborated || ref.Hash == "" {
		t.Fatalf("evidence = %+v", ref)
	}
	if _, published, err := capture.Result(); published || err != nil {
		t.Fatalf("the private anchor touched the capture: %v %v", published, err)
	}
}

// TestOperationPanicPropagatesAndAbortsCapture: a panicking body is not
// masked by a half result — the panic propagates and the capture reports the
// abnormal exit.
func TestOperationPanicPropagatesAndAbortsCapture(t *testing.T) {
	client := lifecycleFleet(t)
	ctx, capture := WithResult(context.Background())
	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("the panic did not propagate")
			}
			if !strings.Contains(r.(string), "boom") {
				t.Fatalf("recovered = %v", r)
			}
		}()
		operation(client, ctx, false, func(opCtx context.Context) (int, CallResult, error) {
			panic("boom")
		})
	}()
	if _, published, err := capture.Result(); published || !errors.Is(err, ErrCaptureAborted) {
		t.Fatalf("aborted capture = %v, %v", published, err)
	}
}

// TestOperationPublishesBusinessFailure: a business failure is a normal
// completion — published once, with Err identical to the trailing error.
func TestOperationPublishesBusinessFailure(t *testing.T) {
	client := lifecycleFleet(t)
	ctx, capture := WithResult(context.Background())
	sentinel := errors.New("business failure")
	value, result, err := operation(client, ctx, false, func(opCtx context.Context) (int, CallResult, error) {
		return 0, CallResult{Decision: &rpc.Decision{Status: rpc.Unknown}}, sentinel
	})
	if value != 0 || err != sentinel || result.Err != sentinel {
		t.Fatalf("delivery mismatch: %v / %v / %v", value, result.Err, err)
	}
	published, ok, cerr := capture.Result()
	if !ok || cerr != nil {
		t.Fatalf("capture delivery = %v, %v", ok, cerr)
	}
	if published.Err != sentinel {
		t.Fatalf("published error = %v", published.Err)
	}
}

// TestOperationReuseLetsTheSecondCallRun: a reused capture invalidates only
// the delivery — the second business call still executes under its normal
// contract.
func TestOperationReuseLetsTheSecondCallRun(t *testing.T) {
	client := lifecycleFleet(t)
	ctx, capture := WithResult(context.Background())
	first, _, err := operation(client, ctx, false, func(opCtx context.Context) (int, CallResult, error) {
		return 1, CallResult{}, nil
	})
	if err != nil || first != 1 {
		t.Fatalf("first call = %v, %v", first, err)
	}
	ran := false
	second, _, err := operation(client, ctx, false, func(opCtx context.Context) (int, CallResult, error) {
		ran = true
		return 2, CallResult{}, nil
	})
	if !ran || err != nil || second != 2 {
		t.Fatalf("second call must run its business contract: ran=%v value=%v err=%v", ran, second, err)
	}
	if _, published, err := capture.Result(); published || !errors.Is(err, ErrCaptureReused) {
		t.Fatalf("reused capture = %v, %v", published, err)
	}
}

// TestOperationMaterialRules: a native call always collects when configured;
// a view call without a capture omits collection; a view call with a capture
// collects. Business behavior never changes with any of these.
func TestOperationMaterialRules(t *testing.T) {
	client := lifecycleFleet(t)
	// The observable is the returned result's material field: a delivered
	// result carries the snapshot, an omitted collection leaves it nil.
	run := func(view bool, withCapture bool) *UncertainResult {
		ctx, err := WithSourceEvidence(context.Background(), 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		if withCapture {
			ctx, _ = WithResult(ctx)
		}
		_, result, _ := operation(client, ctx, view, func(opCtx context.Context) (int, CallResult, error) {
			return 0, CallResult{}, nil
		})
		return result.Uncertain
	}
	if got := run(false, false); got == nil {
		t.Fatal("a native call without a capture must still collect (the returned result is its consumer)")
	}
	if got := run(true, false); got != nil {
		t.Fatal("a view call without a capture must omit collection")
	}
	if got := run(true, true); got == nil {
		t.Fatal("a view call with a capture must collect")
	}
}
