package methods

import "testing"

// TestGetBlockByNumberDefinition pins the method's self-contained definition: the
// file owns the RPC spelling, the spec rides the block wrapper with its own
// admission binding, the descriptor agrees, and the spec compiles into the
// plan the registry installs.
func TestGetBlockByNumberDefinition(t *testing.T) {
	spec := getBlockByNumberSpec()
	descriptor := getBlockByNumberDescriptor()
	if spec.Method != MethodGetBlockByNumber || descriptor.Name != spec.Method {
		t.Fatalf("method identity is not self-contained: spec=%+v descriptor=%+v", spec, descriptor)
	}
	if spec.Resolver != ResolverBlock || spec.Admit == nil {
		t.Fatalf("block definition is incomplete: %+v", spec)
	}
	if descriptor.Adapter != "BlockByNumber" {
		t.Fatalf("adapter mapping drifted: %+v", descriptor)
	}
	if _, err := CompilePlan(spec); err != nil {
		t.Fatal(err)
	}
}
