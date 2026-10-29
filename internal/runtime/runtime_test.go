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
	// Every registered method is driven by either an explicit execution plan
	// or a compiled policy; a method without either cannot enter the
	// registry. The planned set grows one method at a time, so the two
	// shapes partition the catalog.
	if len(bundle.Plans)+len(bundle.Policies) != len(methods.RegisteredNames()) {
		t.Fatalf("runtime registered %d plans and %d policies, want %d methods covered",
			len(bundle.Plans), len(bundle.Policies), len(methods.RegisteredNames()))
	}
	if bundle.Plans[methods.MethodGetBalance].Resolver == nil {
		t.Fatal("runtime omitted the eth_getBalance plan")
	}
	if bundle.Policies.PolicyFor(methods.MethodChainID) == nil {
		t.Fatal("runtime omitted eth_chainId policy")
	}
}
