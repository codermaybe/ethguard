package methods

import "testing"

// TestNetVersionDefinition pins the method's self-contained definition: the
// file owns the RPC spelling, the spec and the descriptor agree on it, and
// the spec compiles into the plan the registry installs.
func TestNetVersionDefinition(t *testing.T) {
	spec := netVersionSpec()
	descriptor := netVersionDescriptor()
	if spec.Method != MethodNetVersion || descriptor.Name != spec.Method {
		t.Fatalf("method identity is not self-contained: spec=%+v descriptor=%+v", spec, descriptor)
	}
	if spec.Normalize == nil {
		t.Fatalf("method definition is incomplete: %+v", spec)
	}
	if descriptor.Adapter != "NetworkID" {
		t.Fatalf("adapter mapping drifted: %+v", descriptor)
	}
	if _, err := CompilePlan(spec); err != nil {
		t.Fatal(err)
	}
}
