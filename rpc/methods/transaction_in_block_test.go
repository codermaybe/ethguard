package methods

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

func TestTransactionInBlockDefinition(t *testing.T) {
	spec := transactionInBlockSpec()
	descriptor := transactionInBlockDescriptor()
	if spec.Method != MethodGetTransactionByBlockHashAndIndex || descriptor.Name != spec.Method || descriptor.Adapter != "TransactionInBlock" {
		t.Fatalf("method identity is not self-contained: spec=%+v descriptor=%+v", spec, descriptor)
	}
	if spec.Admit == nil || spec.Normalize == nil || spec.Encode == nil || spec.Resolver != ResolverStrict {
		t.Fatalf("method definition is incomplete: %+v", spec)
	}
	if _, err := CompilePlan(spec); err != nil {
		t.Fatal(err)
	}
}

func TestTransactionInBlockPolicyRejectsPendingWithoutAdmission(t *testing.T) {
	pending := sampleResults(t, "block-receipt-tx", "tx-one")[0]
	for _, key := range []string{"blockHash", "blockNumber", "blockTimestamp", "transactionIndex"} {
		pending = injectField(t, pending, key, "null")
	}
	spec := transactionInBlockSpec()
	if _, err := spec.Normalize(pending); !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("pending contributed a block/index key: %v", err)
	}
	if _, err := spec.Encode(pending); !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("pending contributed a block/index Value: %v", err)
	}
	policy := planResolver(t, spec.Method, rpc.WithQuorum(2)).(rpc.CorroboratingPolicy)
	decision, err := policy.ResolveCorroborated(rpc.Corroboration{Floor: 2}, []rpc.Observation{
		{Attempted: true, Source: rpc.SourceRef{ID: "a"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: pending}}},
		{Attempted: true, Source: rpc.SourceRef{ID: "b"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: pending}}},
	})
	if err == nil || len(decision.Decision.Value) != 0 || len(decision.Decision.Support) != 0 {
		t.Fatalf("pending bypassed admission via policy: %+v, %v", decision, err)
	}
	if value, err := spec.Encode(json.RawMessage("null")); err != nil || string(value) != "null" {
		t.Fatalf("root null must remain valid: %s, %v", value, err)
	}
	request := rpc.Request{Method: spec.Method, Params: []any{"0x0000000000000000000000000000000000000000000000000000000000000001", "0x0"}}
	if _, err := spec.Admit(context.Background(), request, pending, nil); !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("pending was admitted for an immutable block/index: %v", err)
	}
}
