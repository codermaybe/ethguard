package ethguard_test

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
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

	decision, err := callDecision(client.Call(ctx, methods.MethodGetBalance, common.HexToAddress("0xdad").Hex(), "pending"))
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

// TestPendingLocalCompatEntriesSmoke walks each pending-local compat entry
// name end to end: every one rides the same pending grammar (no anchor
// round), returns a Selected local observation, and carries the CallResult
// contract on the shared path. The per-method semantics are covered at
// resolver level; this pins the named public entries themselves.
func TestPendingLocalCompatEntriesSmoke(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Return(methods.MethodGetBalance, "0x64")
		node.Return(methods.MethodGetTransactionCount, "0x2")
		node.Return(methods.MethodGetCode, "0x")
		node.Return(methods.MethodGetStorageAt, "0x"+"00"+strings.Repeat("ab", 31))
		node.Return(methods.MethodCall, "0x")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	account := common.HexToAddress("0xdad")
	key := common.HexToHash("0x01")

	if balance, result, err := client.PendingBalanceAt(ctx, account); err != nil || balance.Int64() != 100 || result.Decision == nil || result.Decision.Status != rpc.Selected {
		t.Fatalf("PendingBalanceAt = %v %+v %v", balance, result.Decision, err)
	}
	if nonce, result, err := client.PendingNonceAt(ctx, account); err != nil || nonce != 2 || result.Decision == nil || result.Decision.Status != rpc.Selected {
		t.Fatalf("PendingNonceAt = %d %+v %v", nonce, result.Decision, err)
	}
	if code, result, err := client.PendingCodeAt(ctx, account); err != nil || len(code) != 0 || result.Decision == nil || result.Decision.Status != rpc.Selected {
		t.Fatalf("PendingCodeAt = %x %+v %v", code, result.Decision, err)
	}
	if stored, result, err := client.PendingStorageAt(ctx, account, key); err != nil || len(stored) != 32 || result.Decision == nil || result.Decision.Status != rpc.Selected {
		t.Fatalf("PendingStorageAt = %x %+v %v", stored, result.Decision, err)
	}
	to := ethapi.Address(account.Hex())
	toRef := &to
	if data, result, err := client.PendingCallContract(ctx, ethapi.CallMsg{To: &toRef}); err != nil || len(data) != 0 || result.Decision == nil || result.Decision.Status != rpc.Selected {
		t.Fatalf("PendingCallContract = %x %+v %v", data, result.Decision, err)
	}
	for _, node := range nodes {
		for _, call := range node.Calls() {
			if call.Method == methods.MethodGetBlockByNumber {
				t.Fatalf("pending-local entry contacted the anchor path: %+v", call)
			}
		}
	}
}

// TestEstimateGasAtBlockLocalRejectionCarriesTheCallResult pins the
// CallResult.Err contract on the local rejection path: an unsupported block
// number fails before any network contact, and both the root and the view
// surface the same classified error through Err.
func TestEstimateGasAtBlockLocalRejectionCarriesTheCallResult(t *testing.T) {
	client, nodes := fleet(t, 3)
	view := client.EthClient()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, rootResult, rootErr := client.EstimateGasAtBlock(ctx, ethapi.CallMsg{}, big.NewInt(-6))
	_, viewErr := view.EstimateGasAtBlock(ctx, ethapi.CallMsg{}, big.NewInt(-6))
	if !errors.Is(rootErr, ethguard.ErrInvalidRequest) || !errors.Is(viewErr, ethguard.ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest locally, got root %v view %v", rootErr, viewErr)
	}
	if rootResult.Err == nil || !errors.Is(rootResult.Err, ethguard.ErrInvalidRequest) {
		t.Fatalf("root CallResult.Err = %v, want the classified rejection", rootResult.Err)
	}
	for _, node := range nodes {
		if calls := node.Calls(); len(calls) != 0 {
			t.Fatalf("local rejection contacted upstream: %+v", calls)
		}
	}
}

// TestPendingViaAdapterSucceedsDespiteCorroborationGate proves the adapter's
// The typed corroboration requirement does not reject a pending-local Selected
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

	balance, _, err := client.BalanceAt(ctx, addr, big.NewInt(-1))
	if err != nil {
		t.Fatalf("pending BalanceAt: %v", err)
	}
	if balance.Int64() != 100 {
		t.Fatalf("pending balance = %v, want 100", balance)
	}
	nonce, _, err := client.NonceAt(ctx, addr, big.NewInt(-1))
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

	decision, err := callDecision(client.Call(ctx, methods.MethodGetBalance, common.HexToAddress("0xdad").Hex(), "pending"))
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

	decision, err := callDecision(client.Call(ctx, methods.MethodGetBalance, common.HexToAddress("0xdad").Hex(), "pending"))
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

	decision, err := callDecision(client.Call(ctx, methods.MethodGetBalance,
		common.HexToAddress("0xdad").Hex(),
		map[string]any{"blockNumber": "pending", "requireCanonical": true}))
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
