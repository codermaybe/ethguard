package methods

import "testing"

// TestGetLogsDefinition pins the method's self-contained definition: the
// file owns the RPC spelling, the spec and the descriptor agree on it, the
// admission hook is bound where the method owns one, and the spec compiles
// into the plan the registry installs.
func TestGetLogsDefinition(t *testing.T) {
	spec := getLogsSpec()
	descriptor := getLogsDescriptor()
	if spec.Method != MethodGetLogs || descriptor.Name != spec.Method {
		t.Fatalf("method identity is not self-contained: spec=%+v descriptor=%+v", spec, descriptor)
	}
	if spec.Normalize == nil {
		t.Fatalf("method definition is incomplete: %+v", spec)
	}
	if descriptor.Adapter != "FilterLogs" {
		t.Fatalf("adapter mapping drifted: %+v", descriptor)
	}
	if _, err := CompilePlan(spec); err != nil {
		t.Fatal(err)
	}
}
