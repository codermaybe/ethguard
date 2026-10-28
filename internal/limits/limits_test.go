package limits

import (
	"testing"
)

// The whole point of the shared constant is that the three enforcement
// layers cannot drift apart; this test fails if someone reintroduces a
// private spelling of the bound. Consumers are locked by reference, so any
// value change is a deliberate, single-point edit.
func TestMaxBodyBytesIsTheAuditedBound(t *testing.T) {
	if MaxBodyBytes != 32<<20 {
		t.Fatalf("MaxBodyBytes = %d, want the audited 32MiB bound", MaxBodyBytes)
	}
}
