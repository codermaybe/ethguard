package methods

import "testing"

// TestPeerCountDefinition pins the method's self-contained definition: the
// file owns the RPC spelling, the spec and the descriptor agree on it, and
// the spec compiles into the plan the registry installs.
func TestPeerCountDefinition(t *testing.T) {
	spec := peerCountSpec()
	descriptor := peerCountDescriptor()
	if spec.Method != MethodPeerCount || descriptor.Name != spec.Method {
		t.Fatalf("method identity is not self-contained: spec=%+v descriptor=%+v", spec, descriptor)
	}
	if spec.Normalize == nil {
		t.Fatalf("method definition is incomplete: %+v", spec)
	}
	if descriptor.Adapter != "PeerCount" {
		t.Fatalf("adapter mapping drifted: %+v", descriptor)
	}
	if _, err := CompilePlan(spec); err != nil {
		t.Fatal(err)
	}
}
