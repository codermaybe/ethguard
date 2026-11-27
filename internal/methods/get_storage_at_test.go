package methods

import "testing"

// TestGetStorageAtDefinition pins the method's self-contained definition: the
// file owns the RPC spelling, the spec and the descriptor agree on it, the
// declared reference and pending shape compile, and the spec compiles into
// the plan the registry installs.
func TestGetStorageAtDefinition(t *testing.T) {
	spec := getStorageAtSpec()
	descriptor := getStorageAtDescriptor()
	if spec.Method != MethodGetStorageAt || descriptor.Name != spec.Method {
		t.Fatalf("method identity is not self-contained: spec=%+v descriptor=%+v", spec, descriptor)
	}
	if spec.Normalize == nil {
		t.Fatalf("method definition is incomplete: %+v", spec)
	}
	if descriptor.Adapter != "StorageAt" {
		t.Fatalf("adapter mapping drifted: %+v", descriptor)
	}
	if _, err := CompilePlan(spec); err != nil {
		t.Fatal(err)
	}
}
