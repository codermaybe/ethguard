package methods

import (
	"bytes"
	"testing"

	"ethguard/rpc"
	transactionobject "ethguard/rpc/internal/objects/transaction"
)

func TestTransactionPolicyCanonicalizesValueWithoutSplicingSources(t *testing.T) {
	raw := sampleResults(t, "block-receipt-tx", "tx-one")[0]
	withVendor := injectField(t, raw, "vendorExtension", `"outside"`)
	policy := TransactionByHashPolicy(rpc.WithQuorum(2)).(rpc.CorroboratingPolicy)
	decision, err := policy.ResolveCorroborated(rpc.Corroboration{Floor: 2}, []rpc.Observation{
		{Source: rpc.SourceRef{ID: "a"}, Value: raw},
		{Source: rpc.SourceRef{ID: "b"}, Value: withVendor},
	})
	if err != nil || decision.Status != rpc.Corroborated {
		t.Fatalf("decision = %+v err %v", decision, err)
	}
	want, err := transactionobject.Encode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decision.Value, want) {
		t.Fatalf("value = %s, want %s", decision.Value, want)
	}
	if bytes.Contains(decision.Value, []byte("vendorExtension")) {
		t.Fatalf("vendor field survived: %s", decision.Value)
	}
}
