package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	internaluncertain "github.com/codermaybe/ethguard/internal/uncertain"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpctest"
)

// A malformed head body must keep its raw bytes on the invocation's capture
// trail even though admission clears the value before resolution. The
// business read's admission path records before clearing; the reference round
// must keep the same evidence level.
func TestStrictHeadMalformedResponsesKeepRawBytes(t *testing.T) {
	// A duplicate object key is valid JSON at the envelope level and invalid
	// under jsonsafe, so it exercises admission's clear-after-validate step
	// without turning the transport response itself into a decode failure.
	malformed := json.RawMessage(`{"number":"0x64","hash":"0xabc","hash":"0xdef"}`)
	nodes := []*rpctest.Node{rpctest.NewNode(1), rpctest.NewNode(1), rpctest.NewNode(1)}
	defer func() {
		for _, node := range nodes {
			node.Close()
		}
	}()
	for _, node := range nodes {
		node.ReturnRaw(methods.MethodGetBlockByNumber, malformed)
	}
	p := tracePool(t, nodes)
	bundle, err := Build(Inputs{
		Pool:           p,
		Recorder:       observe.Nop,
		CollectTimeout: 2 * time.Second,
		Corroboration:  mustCorroboration(t, rpc.CorroborationMajority, 2),
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
	if err == nil {
		t.Fatal("expected the malformed head round to fail the read")
	}
	if !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("err = %v, want the malformed-response classification", err)
	}
	if decision.Status != rpc.Unknown {
		t.Fatalf("status = %s, want unknown", decision.Status)
	}
	for i, node := range nodes {
		for _, call := range node.Calls() {
			if call.Method == methods.MethodGetBalance {
				t.Fatalf("node %d served the business read after a malformed reference", i)
			}
		}
	}

	var recorded []internaluncertain.Response
	for _, response := range capture.Snapshot().Raw {
		if response.Stage == "response" && response.Method == anchorMethodName {
			recorded = append(recorded, response)
		}
	}
	if len(recorded) != len(nodes) {
		t.Fatalf("captured %d malformed head responses, want one per member", len(recorded))
	}
	for _, response := range recorded {
		if string(response.Value) != string(malformed) {
			t.Fatalf("captured value = %s, want the pre-admission raw bytes %s", response.Value, malformed)
		}
		if !strings.Contains(response.Error, "malformed") {
			t.Fatalf("captured error = %q, want the malformed admission classification", response.Error)
		}
	}
}

// The reference round and the business round must not differ in what they
// keep: a well-formed head still records its raw value exactly once per
// member, with no error text.
func TestStrictHeadWellFormedResponsesKeepOneRawRecordPerMember(t *testing.T) {
	head := json.RawMessage(fmt.Sprintf(`{"number":"0x64","hash":"0x%064x"}`, 100))
	nodes := []*rpctest.Node{rpctest.NewNode(1), rpctest.NewNode(1), rpctest.NewNode(1)}
	defer func() {
		for _, node := range nodes {
			node.Close()
		}
	}()
	for _, node := range nodes {
		node.ReturnRaw(methods.MethodGetBlockByNumber, head)
		node.Return(methods.MethodGetBalance, "0x64")
	}
	p := tracePool(t, nodes)
	bundle, err := Build(Inputs{
		Pool:           p,
		Recorder:       observe.Nop,
		CollectTimeout: 2 * time.Second,
		Corroboration:  mustCorroboration(t, rpc.CorroborationMajority, 2),
	})
	if err != nil {
		t.Fatal(err)
	}
	capture := internaluncertain.New()
	ctx := internaluncertain.WithContext(context.Background(), capture)
	if _, err := bundle.Aggregator.Aggregate(ctx, rpc.Request{
		Method: methods.MethodGetBalance,
		Params: []any{"0x1", "latest"},
	}); err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	headRecords := 0
	for _, response := range capture.Snapshot().Raw {
		if response.Stage == "response" && response.Method == anchorMethodName {
			headRecords++
			if string(response.Value) != string(head) {
				t.Fatalf("captured head value = %s, want %s", response.Value, head)
			}
			if response.Error != "" {
				t.Fatalf("well-formed head kept an error: %q", response.Error)
			}
		}
	}
	if headRecords != len(nodes) {
		t.Fatalf("captured %d head responses, want one per member", headRecords)
	}
}
