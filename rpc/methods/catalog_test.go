package methods

import (
	"testing"

	"ethguard/rpc"
)

func TestCatalogHasUniqueNamesAndStableRegistrationSet(t *testing.T) {
	seen := make(map[string]struct{})
	registered := 0
	for _, method := range Catalog() {
		if method.Name == "" {
			t.Fatal("catalog contains an empty method name")
		}
		if _, exists := seen[method.Name]; exists {
			t.Fatalf("catalog contains duplicate method %q", method.Name)
		}
		seen[method.Name] = struct{}{}
		if method.Kind == RegisteredMethod {
			registered++
			if method.Policy == "" {
				t.Fatalf("registered method %q has no policy", method.Name)
			}
		}
	}
	if registered != len(RegisteredNames()) {
		t.Fatalf("registered name count = %d, catalog count = %d", len(RegisteredNames()), registered)
	}
	if len(RegisteredNames()) != 23 {
		t.Fatalf("registered method count = %d, want 23", len(RegisteredNames()))
	}
}

func TestValidateRegistryDetectsMissingRegisteredPolicy(t *testing.T) {
	registry := make(rpc.Registry)
	if err := ValidateRegistry(registry); err == nil {
		t.Fatal("expected an incomplete registry to be rejected")
	}
	for _, name := range RegisteredNames() {
		registry[name] = rpc.ForwardPolicy{}
	}
	if err := ValidateRegistry(registry); err != nil {
		t.Fatalf("complete registry rejected: %v", err)
	}
}
