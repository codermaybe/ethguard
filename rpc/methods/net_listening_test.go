package methods

import "testing"

// TestListeningDefinition pins the method's self-contained definition: the
// file owns the RPC spelling, the spec and the descriptor agree on it, and
// the spec compiles into the plan the registry installs.
func TestListeningDefinition(t *testing.T) {
	spec := listeningSpec()
	descriptor := listeningDescriptor()
	if spec.Method != MethodListening || descriptor.Name != spec.Method {
		t.Fatalf("method identity is not self-contained: spec=%+v descriptor=%+v", spec, descriptor)
	}
	if spec.Normalize == nil {
		t.Fatalf("method definition is incomplete: %+v", spec)
	}
	if descriptor.Adapter != "" {
		t.Fatalf("adapter mapping drifted: %+v", descriptor)
	}
	if _, err := CompilePlan(spec); err != nil {
		t.Fatal(err)
	}
}
