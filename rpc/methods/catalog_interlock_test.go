package methods

import (
	"testing"
)

// TestCatalogFamilyCoverageInterlock keeps the coverage inventory honest:
// every registered read method owns a spec in the frozen inventory, every
// spec names a registered catalog entry, and the explicitly unsupported
// verification method stays listed with its family — an omission must never
// masquerade as a corroboration guarantee.
func TestCatalogFamilyCoverageInterlock(t *testing.T) {
	specs := map[string]MethodSpec{}
	for _, spec := range Specs() {
		if _, dup := specs[spec.Method]; dup {
			t.Fatalf("duplicate spec for %s", spec.Method)
		}
		specs[spec.Method] = spec
	}
	catalog := map[string]MethodDescriptor{}
	for _, descriptor := range methodCatalog {
		if _, dup := catalog[descriptor.Name]; dup {
			t.Fatalf("duplicate catalog entry for %s", descriptor.Name)
		}
		catalog[descriptor.Name] = descriptor
	}
	for name, descriptor := range catalog {
		switch descriptor.Kind {
		case RegisteredMethod:
			if descriptor.Family != FamilyRead {
				t.Fatalf("%s: registered read method has family %q, want read", name, descriptor.Family)
			}
			if descriptor.Coverage != CoverageRegistered {
				t.Fatalf("%s: registered method has coverage %q", name, descriptor.Coverage)
			}
			spec, ok := specs[name]
			if !ok {
				t.Fatalf("%s: registered method has no MethodSpec", name)
			}
			if spec.Method != name {
				t.Fatalf("%s: spec method mismatch", name)
			}
			// Coverage evidence: the descriptor's alignment is recorded for
			// every method that maps onto an ethclient-shaped adapter.
			if descriptor.Adapter != "" && descriptor.Alignment == "" {
				t.Fatalf("%s: adapter mapping without an alignment record", name)
			}
		case WriteMethod:
			if descriptor.Family != FamilyWrite || descriptor.Coverage != CoverageRegistered {
				t.Fatalf("%s: write method family/coverage = %q/%q", name, descriptor.Family, descriptor.Coverage)
			}
		case UnsupportedMethod:
			if descriptor.Coverage != CoverageUnsupported {
				t.Fatalf("%s: unsupported method has coverage %q", name, descriptor.Coverage)
			}
			if _, ok := specs[name]; ok {
				t.Fatalf("%s: unsupported method must not own a spec", name)
			}
		}
	}
	for name := range specs {
		if _, ok := catalog[name]; !ok {
			t.Fatalf("%s: spec without a catalog entry", name)
		}
	}
	// The verification family's one method is deliberately unsupported, and
	// its evidence is the listing itself.
	proof, ok := catalog["eth_getProof"]
	if !ok || proof.Family != FamilyVerification || proof.Coverage != CoverageUnsupported {
		t.Fatalf("eth_getProof catalog entry = %+v, want unsupported verification coverage", proof)
	}
}
