package methods

import (
	"testing"

	"ethguard/internal/spec"
)

// specAbsentAllow lists registered methods with no execution-apis entry, with
// the reason. The R4 interlock below is a ratchet in both directions: a
// registered method gains coverage when the bundle covers it, and an
// allowlisted method must be promoted out of this map the day the spec (or
// our bundle) covers it.
var specAbsentAllow = map[string]string{
	MethodListening: "no execution-apis entry; upstream-local boolean observation",
	MethodPeerCount: "no execution-apis entry; upstream-local quantity observation",
}

// TestSpecMasksCoverCatalog enforces R4: every registered method compiles to
// a spec whitelist mask from the vendored bundle, except the explicitly
// allowlisted spec-absent methods.
func TestSpecMasksCoverCatalog(t *testing.T) {
	masks, err := methodMasks()
	if err != nil {
		t.Fatalf("spec bundle failed to compile: %v", err)
	}
	for _, name := range RegisteredNames() {
		if _, ok := masks[name]; ok {
			continue
		}
		if reason, ok := specAbsentAllow[name]; ok {
			t.Logf("%s: no spec mask (%s)", name, reason)
			continue
		}
		t.Errorf("registered method %q has neither a spec mask nor an allowlist entry", name)
	}
	for name := range specAbsentAllow {
		if _, ok := masks[name]; ok {
			t.Errorf("allowlisted method %q is now covered by a spec mask; promote it out of specAbsentAllow", name)
		}
	}
	if spec.Version() == "" {
		t.Error("spec VERSION pin is empty")
	}
}
