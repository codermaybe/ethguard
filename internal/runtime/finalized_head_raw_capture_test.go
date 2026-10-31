package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	internaluncertain "github.com/codermaybe/ethguard/internal/uncertain"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpctest"
)

// The finalized strategy runs a selection round and, when a member cannot
// support the candidate, a confirmation round. Both rounds must reach the
// invocation's raw capture trail; the reference algorithm itself stays
// unchanged.
func TestFinalizedHeadRoundsReachTheRawCaptureTrail(t *testing.T) {
	nodes := []*rpctest.Node{rpctest.NewNode(1), rpctest.NewNode(1), rpctest.NewNode(1)}
	defer func() {
		for _, node := range nodes {
			node.Close()
		}
	}()
	heights := []int{10000, 9999, 10001}
	for i, node := range nodes {
		height := heights[i]
		node.Handle(methods.MethodGetBlockByNumber, func(call rpctest.Call) (any, error) {
			tag := ""
			if len(call.Params) > 0 {
				_ = json.Unmarshal(call.Params[0], &tag)
			}
			if tag == "finalized" || tag == "latest" {
				return map[string]any{"number": fmt.Sprintf("0x%x", height), "hash": fmt.Sprintf("0x%064x", height)}, nil
			}
			// Confirmation names the candidate height as a hex quantity.
			confirmed := uint64(0)
			_, _ = fmt.Sscanf(tag, "0x%x", &confirmed)
			return map[string]any{"number": fmt.Sprintf("0x%x", confirmed), "hash": fmt.Sprintf("0x%064x", confirmed)}, nil
		})
		node.Return(methods.MethodGetBalance, "0x64")
	}
	p := tracePool(t, nodes)
	bundle, err := Build(Inputs{
		Pool:           p,
		Recorder:       observe.Nop,
		CollectTimeout: 2 * time.Second,
		Corroboration:  mustCorroboration(t, rpc.CorroborationMajority, 2),
		Anchoring:      &AnchoringStrategy{Finalized: true, MaxLag: 32},
	})
	if err != nil {
		t.Fatal(err)
	}
	capture := internaluncertain.New()
	ctx := internaluncertain.WithContext(context.Background(), capture)
	decision, err := bundle.Aggregator.Aggregate(ctx, rpc.Request{
		Method: methods.MethodGetBalance,
		Params: []any{"0x1", "latest"},
	})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if decision.Status != rpc.Corroborated || decision.Reference == nil || decision.Reference.Height != 10000 {
		t.Fatalf("decision = %+v, want corroboration at height 10000", decision)
	}
	if decision.Reference.Origin != "reported-finalized" {
		t.Fatalf("origin = %q, want the finalized algorithm's evidence", decision.Reference.Origin)
	}

	bySource := make(map[string][]uint64)
	records := 0
	for _, response := range capture.Snapshot().Raw {
		if response.Stage != "response" || response.Method != anchorMethodName {
			continue
		}
		records++
		if len(response.Value) == 0 {
			t.Fatalf("finalized round %d kept no raw value (source %s)", records, response.Source.ID)
		}
		if response.Error != "" {
			t.Fatalf("well-formed finalized round kept an error: %q", response.Error)
		}
		var head struct {
			Number string `json:"number"`
		}
		if err := json.Unmarshal(response.Value, &head); err != nil {
			t.Fatalf("captured value %s is not a head object: %v", response.Value, err)
		}
		height, err := rpc.ParseHexQuantity(head.Number)
		if err != nil {
			t.Fatalf("captured head height %q: %v", head.Number, err)
		}
		bySource[response.Source.URL] = append(bySource[response.Source.URL], height)
	}
	// Three selection observations plus one confirmation (the member above
	// the candidate); the candidate's own source is reused, not re-queried,
	// and a member below the candidate cannot support it.
	if records != 4 {
		t.Fatalf("captured %d finalized round responses, want 3 selection + 1 confirmation", records)
	}
	for i, node := range nodes {
		want := map[int][]uint64{
			0: []uint64{10000},
			1: []uint64{9999},
			2: []uint64{10001, 10000},
		}[i]
		if got := bySource[node.URL()]; fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("node %d captured heights %v, want %v", i, got, want)
		}
	}
}
