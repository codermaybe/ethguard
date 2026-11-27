package methods

import "testing"

// TestGetBlockByHashDefinition pins the method's self-contained definition: the
// file owns the RPC spelling, the spec rides the block wrapper with its own
// admission binding, the descriptor agrees, and the spec compiles into the
// plan the registry installs.
func TestGetBlockByHashDefinition(t *testing.T) {
	spec := getBlockByHashSpec()
	descriptor := getBlockByHashDescriptor()
	if spec.Method != MethodGetBlockByHash || descriptor.Name != spec.Method {
		t.Fatalf("method identity is not self-contained: spec=%+v descriptor=%+v", spec, descriptor)
	}
	if spec.Resolver != ResolverStrict || spec.Admit == nil {
		t.Fatalf("block definition is incomplete: %+v", spec)
	}
	if descriptor.Adapter != "BlockByHash" {
		t.Fatalf("adapter mapping drifted: %+v", descriptor)
	}
	if _, err := CompilePlan(spec); err != nil {
		t.Fatal(err)
	}
}
