package ethguard_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpctest"
)

// The dynamic Call route: the allow-list is the read-plan registry, the
// check completes before any network contact, and a registered read keeps
// its own method contract through the generic entry.

func TestCallRejectsUnroutableMethodsLocally(t *testing.T) {
	client, nodes := fleet(t, 3)
	rejected := []string{
		methods.MethodSendRawTransaction,
		"eth_sendTransaction",        // account-managed write
		"eth_sendRawTransactionSync", // mined/synchronous write extension
		"eth_getProof",               // explicit unsupported verification method
		"eth_simulateV1",             // unsupported execution extension
		"debug_traceCall",            // debug/trace extension
		"trace_replayTransaction",    // trace extension
		"eth_subscribe",              // deferred stream family
	}
	for _, node := range nodes {
		for _, method := range rejected {
			node.Handle(method, func(c rpctest.Call) (any, error) {
				t.Errorf("unsupported method %s reached a provider through Call", c.Method)
				return "unexpected", nil
			})
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, method := range rejected {
		params := []any(nil)
		if method == methods.MethodSendRawTransaction {
			params = []any{"0x00"}
		}
		result, err := client.Call(ctx, method, params...)
		if !errors.Is(err, ethguard.ErrInvalidRequest) {
			t.Fatalf("%s route = %v, want ErrInvalidRequest", method, err)
		}
		if result.Decision != nil || result.Err != err {
			t.Fatalf("%s route rejection result = %+v", method, result)
		}
	}
	// Unknown method: same local class.
	if _, err := client.Call(ctx, "eth_madeUpMethod"); !errors.Is(err, ethguard.ErrInvalidRequest) {
		t.Fatalf("unknown route = %v, want ErrInvalidRequest", err)
	}

	for _, node := range nodes {
		for _, call := range node.Calls() {
			if call.Method != "eth_chainId" {
				t.Errorf("rejected route contacted %s", call.Method)
			}
		}
	}
}

func TestCallRegisteredReadStaysForwardedInSingleSourceMode(t *testing.T) {
	_, nodes := fleet(t, 2)
	for _, node := range nodes {
		node.Return(methods.MethodChainID, "0x1")
	}
	single, err := ethguard.DialUncorroborated(nodes[0].URL())
	if err != nil {
		t.Fatal(err)
	}
	defer single.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// A registered read keeps its contract through Call: in explicit
	// single-source mode the status is Forwarded, never Corroborated.
	result, err := single.Call(ctx, methods.MethodChainID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision == nil || result.Decision.Status != rpc.Forwarded {
		t.Fatalf("registered read through Call = %+v, want Forwarded", result.Decision)
	}
	// The unknown-method bypass stays closed even in single-source mode.
	if _, err := single.Call(ctx, "eth_madeUpMethod"); !errors.Is(err, ethguard.ErrInvalidRequest) {
		t.Fatalf("unknown route in single-source mode = %v, want ErrInvalidRequest", err)
	}
}
