package methods

import (
	"testing"

	"github.com/codermaybe/ethguard/internal/ethobject/wiremask"
	"github.com/codermaybe/ethguard/internal/spec"
)

// Every registered read method has a fixed official result definition.
func TestSpecMasksCoverCatalog(t *testing.T) {
	masks, err := wiremask.MethodMasks()
	if err != nil {
		t.Fatalf("spec bundle failed to compile: %v", err)
	}
	for _, name := range RegisteredNames() {
		if _, ok := masks[name]; ok {
			continue
		}
		t.Errorf("registered method %q has no official spec mask", name)
	}
	if spec.Version() == "" {
		t.Error("spec VERSION pin is empty")
	}
}
