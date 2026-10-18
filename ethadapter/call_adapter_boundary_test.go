package ethadapter_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/ethadapter"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

// TestT082CallSuccessUsesCanonicalDataThroughAdapter checks the successful
// DATA projection through the production Aggregator and typed adapter.
func TestT082CallSuccessUsesCanonicalDataThroughAdapter(t *testing.T) {
	client, nodes := fleet(t, 3)
	headFixture(nodes)
	for _, node := range nodes {
		node.ReturnRaw(methods.MethodCall, json.RawMessage(`"0xDEAD"`))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	to := common.HexToAddress("0x1")
	data, err := client.CallContract(ctx, ethereum.CallMsg{To: &to}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := common.Bytes2Hex(data); got != "dead" {
		t.Fatalf("data = %s, want dead", got)
	}
}

// TestT082CallRevertKeepsContextOutOfCoreValue checks that the winning
// revert data is exposed through Value while provider code/message are exposed
// only through the typed RevertError context.
func TestT082CallRevertKeepsContextOutOfCoreValue(t *testing.T) {
	client, nodes := fleet(t, 3)
	headFixture(nodes)
	nodes[0].FailWithData(methods.MethodCall, 3, "execution reverted: provider-a", json.RawMessage(`"0xdead"`), true)
	nodes[1].FailWithData(methods.MethodCall, -32000, "execution reverted: provider-b", json.RawMessage(`"0xdead"`), true)
	nodes[2].FailWithData(methods.MethodCall, 3, "execution reverted: provider-c", nil, false)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	to := common.HexToAddress("0x1")
	decision, err := client.Unwrap().Call(ctx, methods.MethodCall, map[string]any{"to": to.Hex()}, "latest")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != rpc.Corroborated || string(decision.Value) != `{"data":"0xdead"}` {
		t.Fatalf("decision = %s/%s, want corroborated accepted data", decision.Status, decision.Value)
	}
	if bytes.Contains(decision.Value, []byte("code")) || bytes.Contains(decision.Value, []byte("message")) {
		t.Fatalf("provider envelope leaked into Value: %s", decision.Value)
	}
	if decision.Revert == nil || decision.Revert.Code != 3 || decision.Revert.Message != "execution reverted: provider-a" {
		t.Fatalf("revert context = %+v", decision.Revert)
	}

	_, err = client.CallContract(ctx, ethereum.CallMsg{To: &to}, nil)
	var revert *ethadapter.RevertError
	if !errors.As(err, &revert) {
		t.Fatalf("typed error = %v, want RevertError", err)
	}
	if !strings.Contains(err.Error(), "execution reverted: provider-a") || common.Bytes2Hex(revert.Data) != "dead" {
		t.Fatalf("typed revert = %+v", revert)
	}
}

// TestT082CallRevertWithheldDataKeepsProviderEnvelopeOutOfValue checks the
// reductive projection when no concrete data cluster reaches the threshold.
func TestT082CallRevertWithheldDataKeepsProviderEnvelopeOutOfValue(t *testing.T) {
	client, nodes := fleet(t, 3)
	headFixture(nodes)
	nodes[0].FailWithData(methods.MethodCall, 3, "execution reverted: dead", json.RawMessage(`"0xdead"`), true)
	nodes[1].FailWithData(methods.MethodCall, 3, "execution reverted: beef", json.RawMessage(`"0xbeef"`), true)
	nodes[2].FailWithData(methods.MethodCall, 3, "execution reverted: missing", nil, false)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	to := common.HexToAddress("0x1")
	decision, err := client.Unwrap().Call(ctx, methods.MethodCall, map[string]any{"to": to.Hex()}, "latest")
	if err != nil || decision.Status != rpc.Corroborated {
		t.Fatalf("decision = %s/%v, want corroborated revert", decision.Status, err)
	}
	if string(decision.Value) != `{}` {
		t.Fatalf("withheld Value = %s, want empty projected object", decision.Value)
	}
	if bytes.Contains(decision.Value, []byte("code")) || bytes.Contains(decision.Value, []byte("message")) {
		t.Fatalf("provider envelope leaked into Value: %s", decision.Value)
	}
	_, err = client.CallContract(ctx, ethereum.CallMsg{To: &to}, nil)
	var revert *ethadapter.RevertError
	if !errors.As(err, &revert) || revert.Data != nil || revert.ErrorData() != nil {
		t.Fatalf("typed withheld revert = %v", err)
	}
}

// TestT082CallConflictStaysUnknownThroughAdapter checks the typed gate on an
// execution conflict: no representative revert is surfaced as a success.
func TestT082CallConflictStaysUnknownThroughAdapter(t *testing.T) {
	client, nodes := fleet(t, 3)
	headFixture(nodes)
	values := [...]string{"0x0123", "0x0223", "0x0323"}
	for i, node := range nodes {
		node.ReturnRaw(methods.MethodCall, json.RawMessage(`"`+values[i]+`"`))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	to := common.HexToAddress("0x1")
	decision, err := client.Unwrap().Call(ctx, methods.MethodCall, map[string]any{"to": to.Hex()}, "latest")
	if !errors.Is(err, ethguard.ErrConflict) || decision.Status != rpc.Unknown {
		t.Fatalf("decision = %s/%v, want unknown conflict", decision.Status, err)
	}
	_, typedErr := client.CallContract(ctx, ethereum.CallMsg{To: &to}, nil)
	if !errors.Is(typedErr, ethguard.ErrConflict) {
		t.Fatalf("typed error = %v, want conflict", typedErr)
	}
}

// TestT082CallPendingRemainsSelected checks that pending stays on the local
// observation path and never establishes an anchor.
func TestT082CallPendingRemainsSelected(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.ReturnRaw(methods.MethodCall, json.RawMessage(`"0xAB"`))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	decision, err := client.Unwrap().Call(ctx, methods.MethodCall,
		map[string]any{"to": common.HexToAddress("0x1").Hex()}, "pending")
	if err != nil || decision.Status != rpc.Selected || string(decision.Value) != `"0xab"` {
		t.Fatalf("pending decision = %s/%s/%v, want selected canonical DATA", decision.Status, decision.Value, err)
	}
	for _, node := range nodes {
		for _, call := range node.Calls() {
			if call.Method == "eth_getBlockByNumber" {
				t.Fatal("pending call established an anchor")
			}
		}
	}
}
