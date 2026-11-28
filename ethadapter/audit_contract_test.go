package ethadapter_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"testing"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/ethadapter"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
)

func contractClient(t *testing.T, node *rpctest.Node, opts ...ethadapter.Option) *ethadapter.Client {
	t.Helper()
	core, err := ethguard.Dial(node.URL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(core.Close)
	return ethadapter.New(core, opts...)
}

func TestIndependentAuditSeparatesPreCoreAndPostConversionFailures(t *testing.T) {
	for _, stage := range []string{"pre", "core", "post", "success"} {
		t.Run(stage, func(t *testing.T) {
			node := rpctest.NewNode(1)
			t.Cleanup(node.Close)
			node.Return("eth_getBalance", "0x2a")
			node.ReturnRaw("eth_syncing", json.RawMessage(`{"startingBlock":"0x0","currentBlock":"0x10000000000000000","highestBlock":"0x10000000000000001"}`))
			if stage == "core" {
				node.Fail("eth_getBalance", -32001, "provider refused the request")
			}
			var events []ethadapter.AuditEvent
			client := contractClient(t, node, ethadapter.WithAudit(func(_ context.Context, event ethadapter.AuditEvent) { events = append(events, event) }))
			ctx, capture := ethguard.WithResult(t.Context())
			var callErr error
			switch stage {
			case "pre":
				_, callErr = client.CallContract(ctx, ethereum.CallMsg{Value: big.NewInt(-1)}, big.NewInt(17))
			case "post":
				_, callErr = client.SyncProgress(ctx)
			default:
				_, callErr = client.BalanceAt(ctx, common.Address{}, big.NewInt(17))
			}
			if len(events) != 1 || events[0].Method == "" || events[0].Duration <= 0 {
				t.Fatalf("one complete audit event required: %#v", events)
			}
			event := events[0]
			result, published, captureErr := capture.Result()
			if captureErr != nil {
				t.Fatalf("adapter overwrote/reused caller capture: %v", captureErr)
			}
			switch stage {
			case "pre":
				requireConversionError(t, callErr)
				if published || len(node.Calls()) != 0 || event.CoreErr != nil || !errors.Is(event.AdapterErr, callErr) || event.CallResult.Err != nil || event.CallResult.Decision != nil {
					t.Fatalf("pre-conversion rejection was presented as a core execution: event=%#v published=%v calls=%d", event, published, len(node.Calls()))
				}
			case "core":
				if callErr == nil || !published || result.Err == nil || event.AdapterErr != nil || !errors.Is(event.CoreErr, callErr) || !errors.Is(event.CallResult.Err, callErr) {
					t.Fatalf("core failure was obscured: err=%v capture=%#v event=%#v", callErr, result, event)
				}
			case "post":
				requireConversionError(t, callErr)
				if !published || result.Err != nil || result.Decision == nil || event.CoreErr != nil || event.CallResult.Err != nil || !errors.Is(event.AdapterErr, callErr) {
					t.Fatalf("post-conversion error rewrote successful core capture: capture=%#v event=%#v", result, event)
				}
			case "success":
				if callErr != nil || !published || result.Err != nil || event.CoreErr != nil || event.AdapterErr != nil || result.Decision == nil || result.Decision.Status != rpc.Forwarded {
					t.Fatalf("successful Forwarded call lost its result: %v %#v %#v", callErr, result, event)
				}
				if !event.EvidenceScope.Forwarded || event.EvidenceScope.ExtensionsObserved || event.EvidenceScope.SourceEvidenceObserved {
					t.Fatalf("Forwarded evidence scope overclaimed: %#v", event.EvidenceScope)
				}
			}
		})
	}
}

func TestIndependentAuditHookTakesPrecedenceOverLogger(t *testing.T) {
	for _, hookLast := range []bool{false, true} {
		t.Run(fmt.Sprintf("hook_last_%v", hookLast), func(t *testing.T) {
			node := rpctest.NewNode(1)
			t.Cleanup(node.Close)
			node.Return("eth_getBalance", "0x2a")
			var output bytes.Buffer
			calls := 0
			hook := ethadapter.WithAudit(func(context.Context, ethadapter.AuditEvent) { calls++ })
			logger := ethadapter.WithLogger(slog.New(slog.NewJSONHandler(&output, nil)))
			opts := []ethadapter.Option{hook, logger}
			if hookLast {
				opts = []ethadapter.Option{logger, hook}
			}
			client := contractClient(t, node, opts...)
			if _, err := client.BalanceAt(t.Context(), common.Address{}, big.NewInt(17)); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || output.Len() != 0 {
				t.Fatalf("hook/logger precedence depends on option order: hook=%d logger=%q", calls, output.String())
			}
		})
	}
}

func TestIndependentAuditScopeReflectsExplicitSourceEvidence(t *testing.T) {
	node := rpctest.NewNode(1)
	t.Cleanup(node.Close)
	node.Return("eth_getBalance", "0x2a")
	var event ethadapter.AuditEvent
	client := contractClient(t, node, ethadapter.WithAudit(func(_ context.Context, observed ethadapter.AuditEvent) { event = observed }))
	ctx, err := ethguard.WithSourceEvidence(t.Context(), 65536)
	if err != nil {
		t.Fatal(err)
	}
	ctx, capture := ethguard.WithResult(ctx)
	if _, err := client.BalanceAt(ctx, common.Address{}, big.NewInt(17)); err != nil {
		t.Fatal(err)
	}
	result, ok, err := capture.Result()
	if err != nil || !ok || result.Uncertain == nil || event.CallResult.Uncertain == nil || !event.EvidenceScope.SourceEvidenceObserved || !event.EvidenceScope.Forwarded {
		t.Fatalf("source evidence was hidden from observation/capture: result=%#v event=%#v ok=%v err=%v", result, event, ok, err)
	}
}

func TestIndependentAuditPanicCannotMakeSuccessfulBroadcastRetryable(t *testing.T) {
	node := rpctest.NewNode(1)
	t.Cleanup(node.Close)
	tx := contractSignedTransactions(t)[0]
	node.Return("eth_sendRawTransaction", tx.Hash().Hex())
	observations := 0
	client := contractClient(t, node, ethadapter.WithAudit(func(context.Context, ethadapter.AuditEvent) {
		observations++
		panic("broken audit consumer")
	}))
	ctx, capture := ethguard.WithResult(t.Context())
	if err := client.SendTransaction(ctx, tx); err != nil {
		t.Fatalf("audit panic changed successful broadcast: %v", err)
	}
	result, ok, err := capture.Result()
	if err != nil || !ok || result.Err != nil || result.WriteDecision == nil || observations != 1 {
		t.Fatalf("broadcast completion changed: result=%#v ok=%v err=%v observations=%d", result, ok, err, observations)
	}
	writes := 0
	for _, call := range node.Calls() {
		if call.Method == "eth_sendRawTransaction" {
			writes++
		}
	}
	if writes != 1 {
		t.Fatalf("one API send must perform one broadcast to this source, got %d", writes)
	}
}

func TestIndependentRevertErrorDataPreservesNativeChainAndEmptyCapture(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  string
		want []byte
		ok   bool
	}{
		{"missing", "", nil, false},
		{"null", "null", nil, false},
		{"empty", `"0x"`, []byte{}, true},
		{"bytes", `"0xdead"`, []byte{0xde, 0xad}, true},
		{"object", `{"reason":"denied"}`, nil, false},
		{"invalid_hex", `"0xzz"`, nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			node := rpctest.NewNode(1)
			t.Cleanup(node.Close)
			node.FailWithData("eth_call", 3, "execution reverted", json.RawMessage(test.raw), test.raw != "")
			client := contractClient(t, node)
			ctx, capture := ethguard.WithResult(t.Context())
			_, err := client.CallContract(ctx, ethereum.CallMsg{}, big.NewInt(17))
			var native *ethguard.RevertError
			if !errors.As(err, &native) {
				t.Fatalf("adapter replaced native revert: %T %v", err, err)
			}
			wrapped := fmt.Errorf("application: %w", err)
			if !errors.Is(wrapped, native) {
				t.Fatal("native error chain is no longer reachable")
			}
			result, published, captureErr := capture.Result()
			if captureErr != nil || !published || result.Err == nil {
				t.Fatalf("missing native error capture: %#v %v %v", result, published, captureErr)
			}
			for index, sourceErr := range []error{wrapped, result.Err} {
				got, ok := ethadapter.RevertErrorData(sourceErr)
				if ok != test.ok || !bytes.Equal(got, test.want) || (test.ok && got == nil) {
					t.Fatalf("revert data=%#v,%v want %#v,%v (capture=%v)", got, ok, test.want, test.ok, index == 1)
				}
				if len(got) > 0 {
					got[0] = 0
					again, _ := ethadapter.RevertErrorData(sourceErr)
					if !bytes.Equal(again, test.want) {
						t.Fatal("helper returned mutable native revert storage")
					}
				}
			}
			var rpcCode interface{ ErrorCode() int }
			if errors.As(err, &rpcCode) {
				t.Fatal("adapter fabricated an RPC error code")
			}
		})
	}
	if data, ok := ethadapter.RevertErrorData(errors.New("not a revert")); ok || data != nil {
		t.Fatal("non-revert error acquired revert semantics")
	}
}
