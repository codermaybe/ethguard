package runtime

import (
	"testing"

	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc/methods"
)

func TestBuildAssemblesRegisteredRuntime(t *testing.T) {
	bundle, err := Build(Inputs{Pool: pool.NewPool()})
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Aggregator == nil {
		t.Fatal("runtime bundle has no aggregator")
	}
	// The default registry drives every registered method from its explicit
	// execution plan and generates no decorator chains: the plans partition
	// the whole catalog, and the policy registry stays empty for unknown
	// methods' transparent forwarding.
	if len(bundle.Plans) != len(methods.RegisteredNames()) {
		t.Fatalf("runtime registered %d plans, want %d", len(bundle.Plans), len(methods.RegisteredNames()))
	}
	if len(bundle.Policies) != 0 {
		t.Fatalf("the default registry compiled %d policy chains, want none", len(bundle.Policies))
	}
	for _, method := range methods.RegisteredNames() {
		if bundle.Plans[method].Resolver == nil {
			t.Fatalf("runtime omitted the %s plan", method)
		}
	}
}
