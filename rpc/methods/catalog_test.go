package methods

import (
	"testing"
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
	if len(RegisteredNames()) != 25 {
		t.Fatalf("registered method count = %d, want 25", len(RegisteredNames()))
	}
}
