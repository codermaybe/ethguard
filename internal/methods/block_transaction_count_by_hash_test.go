package methods

import "testing"

// TestBlockTransactionCountByHashDefinition pins the method's self-contained definition: the
// file owns the RPC spelling, the spec and the descriptor agree on it, the
// declared reference and pending shape compile, and the spec compiles into
// the plan the registry installs.
func TestBlockTransactionCountByHashDefinition(t *testing.T) {
	spec := blockTransactionCountByHashSpec()
	descriptor := blockTransactionCountByHashDescriptor()
	if spec.Method != MethodGetBlockTransactionCountByHash || descriptor.Name != spec.Method {
		t.Fatalf("method identity is not self-contained: spec=%+v descriptor=%+v", spec, descriptor)
	}
	if spec.Normalize == nil {
		t.Fatalf("method definition is incomplete: %+v", spec)
	}
	if descriptor.Adapter != "TransactionCount" {
		t.Fatalf("adapter mapping drifted: %+v", descriptor)
	}
	if _, err := CompilePlan(spec); err != nil {
		t.Fatal(err)
	}
}
