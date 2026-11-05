package runtime

import (
	"testing"

	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc/methods"
	"strings"
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
	for _, method := range methods.RegisteredNames() {
		if bundle.Plans[method].Resolver == nil {
			t.Fatalf("runtime omitted the %s plan", method)
		}
	}
}

// TestComposeRejectsCollapsedBasis exercises the composition backstop
// directly: Compose receives Inputs without config.Validate, so a caller that
// bypasses the configuration layer must still fail construction when pool
// folds spelling variants of one endpoint into fewer members than declared.
func TestComposeRejectsCollapsedBasis(t *testing.T) {
	_, err := Compose(Composition{
		Upstreams: []config.Upstream{
			{ID: "a", Endpoint: "https://node.example"},
			{ID: "b", Endpoint: "https://node.example:443"},
		},
	})
	if err == nil {
		t.Fatal("expected Compose to reject the collapsed basis")
	}
	if !strings.Contains(err.Error(), "collapsed to 1 pool members") || !strings.Contains(err.Error(), "2 configured upstreams") {
		t.Fatalf("backstop must name the fold, got %v", err)
	}
}
