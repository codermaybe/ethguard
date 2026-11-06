package ethguard_test

import (
	"context"
	"errors"
	"strings"
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
	for _, node := range nodes {
		node.Handle(methods.MethodSendRawTransaction, func(c rpctest.Call) (any, error) {
			t.Error("the write method reached a provider through Call")
			return "0x00", nil
		})
		node.Handle("eth_subscribe", func(c rpctest.Call) (any, error) {
			t.Error("a subscription method reached a provider through Call")
			return "sub", nil
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Write: the error points at the dedicated write entry.
	result, err := client.Call(ctx, methods.MethodSendRawTransaction, "0x00")
	if !errors.Is(err, ethguard.ErrInvalidRequest) || !strings.Contains(err.Error(), "SendRawTransaction") {
		t.Fatalf("write route = %v, want ErrInvalidRequest with the write hint", err)
	}
	if result.Decision != nil || result.Err != err {
		t.Fatalf("route rejection result = %+v", result)
	}

	// Subscription: same local class, no hint.
	if _, err := client.Call(ctx, "eth_subscribe", "newHeads"); !errors.Is(err, ethguard.ErrInvalidRequest) {
		t.Fatalf("subscription route = %v, want ErrInvalidRequest", err)
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
