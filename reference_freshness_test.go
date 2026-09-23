package ethguard_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"ethguard/rpc"
	"ethguard/rpc/methods"
	"ethguard/rpctest"
)

func TestReferenceAnchorFailureKeepsPrepareErrorAndSkipsBusinessFanout(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockByNumber, map[string]any{})
		node.Handle(methods.MethodGetBalance, func(rpctest.Call) (any, error) {
			t.Error("business request sent after anchor failure")
			return "0x1", nil
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	decision, err := client.Call(ctx, methods.MethodGetBalance, "0xaddr", "latest")
	if !errors.Is(err, rpc.ErrNoValidResponse) {
		t.Fatalf("got %v, want anchor ErrNoValidResponse", err)
	}
	if !strings.Contains(err.Error(), "anchor:") {
		t.Fatalf("prepare error %q does not wrap the anchor cause", err)
	}
	if errors.Is(err, rpc.ErrConflict) || errors.Is(err, rpc.ErrInvalidRequest) || errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("anchor failure swallowed by another class: %v", err)
	}
	if decision.Status != rpc.Unknown {
		t.Fatalf("status = %s, want unknown", decision.Status)
	}
	if anchorEstablishments(nodes) == 0 {
		t.Fatal("anchor failure produced no establishment attempt")
	}
	for _, node := range nodes {
		for _, call := range node.Calls() {
			if call.Method == methods.MethodGetBalance {
				t.Fatalf("business fan-out after prepare failure: %+v", call)
			}
		}
	}
}

func TestReferenceEstablishedFreshPerCall(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	agreeOnHead(nodes)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	first, err := client.Call(ctx, methods.MethodGetBalance, "0xaddr", "latest")
	if err != nil {
		t.Fatal(err)
	}
	if first.Reference == nil || first.Reference.Status != rpc.Corroborated || first.Reference.ObservedAt.IsZero() {
		t.Fatalf("first reference = %+v", first.Reference)
	}
	afterFirst := anchorEstablishments(nodes)
	if afterFirst == 0 {
		t.Fatal("first call did not establish a reference")
	}

	second, err := client.Call(ctx, methods.MethodGetBalance, "0xaddr", "latest")
	if err != nil {
		t.Fatal(err)
	}
	afterSecond := anchorEstablishments(nodes)
	if afterSecond != afterFirst*2 {
		t.Fatalf("anchor calls = %d after two identical calls, want %d with no cache", afterSecond, afterFirst*2)
	}
	if second.Reference == nil || second.Reference.ObservedAt.Equal(first.Reference.ObservedAt) {
		t.Fatalf("second call reused the first reference: %+v", second.Reference)
	}
}

func TestReferenceFailureEvidenceIsUnknownWithObservedAt(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockByNumber, map[string]any{})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	decision, err := client.Call(ctx, methods.MethodGetBalance, "0xaddr", "latest")
	if err == nil {
		t.Fatal("expected anchor failure")
	}
	ref := decision.Reference
	if ref == nil {
		t.Fatal("failed call dropped reference evidence")
	}
	if ref.Status != rpc.Unknown {
		t.Fatalf("reference status = %s, want unknown", ref.Status)
	}
	if ref.ObservedAt.IsZero() {
		t.Fatal("failed reference has a zero ObservedAt")
	}
	if len(ref.Faults) == 0 {
		t.Fatalf("failed reference dropped faults: %+v", ref)
	}
	var kept bool
	for _, fault := range ref.Faults {
		if strings.Contains(fault.Error(), "missing number or hash") {
			kept = true
			break
		}
	}
	if !kept {
		t.Fatalf("anchor cause was not retained in faults: %+v", ref.Faults)
	}
	if decision.Status != rpc.Unknown {
		t.Fatalf("decision status = %s, want unknown", decision.Status)
	}
}
