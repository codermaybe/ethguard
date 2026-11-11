package methods

import "testing"

// TestGetTransactionReceiptDefinition pins the method's self-contained definition: the
// file owns the RPC spelling, the spec and the descriptor agree on it, the
// admission hook is bound, and the spec compiles into the plan the registry
// installs.
func TestGetTransactionReceiptDefinition(t *testing.T) {
	spec := getTransactionReceiptSpec()
	descriptor := getTransactionReceiptDescriptor()
	if spec.Method != MethodGetTransactionReceipt || descriptor.Name != spec.Method {
		t.Fatalf("method identity is not self-contained: spec=%+v descriptor=%+v", spec, descriptor)
	}
	if spec.Normalize == nil || spec.Admit == nil {
		t.Fatalf("method definition is incomplete: %+v", spec)
	}
	if descriptor.Adapter != "TransactionReceipt" {
		t.Fatalf("adapter mapping drifted: %+v", descriptor)
	}
	if _, err := CompilePlan(spec); err != nil {
		t.Fatal(err)
	}
}
