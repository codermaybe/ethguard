package ethadapter_test

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"ethguard"
	"ethguard/rpc"
	"ethguard/rpc/methods"
)

// TestPendingAgreementIsSelectedNotCorroborated is the R1 acceptance test:
// unanimous pending agreement across every source stays Selected with the
// first source as Support — it can never become Corroborated.
func TestPendingAgreementIsSelectedNotCorroborated(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Return(methods.MethodGetBalance, "0x64")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	decision, err := client.Unwrap().Call(ctx, methods.MethodGetBalance, common.HexToAddress("0xdad").Hex(), "pending")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != rpc.Selected {
		t.Fatalf("pending unanimous: status = %v, want selected (never corroborated)", decision.Status)
	}
	if string(decision.Value) != `"0x64"` {
		t.Fatalf("pending value = %s, want 0x64", decision.Value)
	}
	if len(decision.Support) != 1 || decision.Support[0].Name != "node-a" {
		t.Fatalf("pending support = %+v, want exactly the first source", decision.Support)
	}
	if decision.BlockRef != "" {
		t.Fatalf("pending BlockRef = %q, want empty (nothing rewritten)", decision.BlockRef)
	}
	if len(decision.Participants) != 3 {
		t.Fatalf("pending participants = %d, want 3", len(decision.Participants))
	}
}

// TestPendingViaAdapterSucceedsDespiteCorroborationGate proves the adapter's
// RequireCorroborated context does not reject a pending-local Selected
// result: the corroboration gate applies to mined reads, not to pending
// observations.
func TestPendingViaAdapterSucceedsDespiteCorroborationGate(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Return(methods.MethodGetBalance, "0x64")
		node.Return(methods.MethodGetTransactionCount, "0x7")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	addr := common.HexToAddress("0xdad")

	balance, err := client.BalanceAt(ctx, addr, big.NewInt(-1))
	if err != nil {
		t.Fatalf("pending BalanceAt: %v", err)
	}
	if balance.Int64() != 100 {
		t.Fatalf("pending balance = %v, want 100", balance)
	}
	nonce, err := client.NonceAt(ctx, addr, big.NewInt(-1))
	if err != nil {
		t.Fatalf("pending NonceAt: %v", err)
	}
	if nonce != 7 {
		t.Fatalf("pending nonce = %d, want 7", nonce)
	}
	for _, node := range nodes {
		if len(anchorCalls(node.Calls())) != 0 {
			t.Fatal("pending triggered anchor establishment")
		}
	}
}

// TestPendingDissentSelectsFirst: pending disagreement is not conflict.
func TestPendingDissentSelectsFirst(t *testing.T) {
	client, nodes := fleet(t, 3)
	values := []string{"0x64", "0x65", "0x66"}
	for i, node := range nodes {
		node.Return(methods.MethodGetBalance, values[i])
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	decision, err := client.Unwrap().Call(ctx, methods.MethodGetBalance, common.HexToAddress("0xdad").Hex(), "pending")
	if err != nil {
		t.Fatalf("pending split: %v", err)
	}
	if decision.Status != rpc.Selected || string(decision.Value) != `"0x64"` {
		t.Fatalf("pending split: status = %v value = %s, want selected first answer", decision.Status, decision.Value)
	}
}

// TestPendingAllFailIsUnknown: no usable pending source fails Unknown —
// never a guessed Selected or a forged Corroborated.
func TestPendingAllFailIsUnknown(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Fail(methods.MethodGetBalance, -32000, "pending not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	decision, err := client.Unwrap().Call(ctx, methods.MethodGetBalance, common.HexToAddress("0xdad").Hex(), "pending")
	if err == nil || !errors.Is(err, ethguard.ErrNoValidResponse) {
		t.Fatalf("pending all-fail: got %v, want ErrNoValidResponse", err)
	}
	if decision.Status != rpc.Unknown {
		t.Fatalf("pending all-fail status = %v, want unknown", decision.Status)
	}
}

// TestPendingObjectFormIsLocalObservation: the EIP-1898 pending spelling
// takes the same local path as the bare tag.
func TestPendingObjectFormIsLocalObservation(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Return(methods.MethodGetBalance, "0x64")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	decision, err := client.Unwrap().Call(ctx, methods.MethodGetBalance,
		common.HexToAddress("0xdad").Hex(),
		map[string]any{"blockNumber": "pending", "requireCanonical": true})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != rpc.Selected {
		t.Fatalf("pending object: status = %v, want selected", decision.Status)
	}
	for _, node := range nodes {
		bus := businessCalls(node.Calls(), methods.MethodGetBalance)
		if len(bus) != 1 {
			t.Fatalf("balance calls = %d, want 1", len(bus))
		}
		got := wireObject(t, bus[0].Params[1])
		if got["blockNumber"] != "pending" || got["requireCanonical"] != true {
			t.Fatalf("pending object on wire = %v, want blockNumber pending with requireCanonical", got)
		}
	}
}
