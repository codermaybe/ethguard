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
	if len(bundle.Policies) != len(methods.RegisteredNames()) {
		t.Fatalf("runtime registered %d policies, want %d", len(bundle.Policies), len(methods.RegisteredNames()))
	}
	if bundle.Policies.PolicyFor(methods.MethodChainID) == nil {
		t.Fatal("runtime omitted eth_chainId policy")
	}
}
