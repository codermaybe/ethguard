package runtime

import (
	stdruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/internal/methods"
	"github.com/codermaybe/ethguard/internal/pool"
)

func TestBuildAssemblesRegisteredRuntime(t *testing.T) {
	p, err := pool.NewWithMembers(0, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	bundle, err := Build(Inputs{Pool: p})
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
		if err := bundle.Plans[method].ValidateExecution(); err != nil {
			t.Fatalf("runtime omitted the %s plan", method)
		}
	}
}

// TestComposeRejectsCollapsedBasis exercises the composition backstop
// directly: Compose normalizes its own inputs, so a caller that
// bypasses the public configuration entry points must still fail construction
// when spelling variants would collapse the declared membership denominator.
func TestComposeRejectsCollapsedBasis(t *testing.T) {
	input := Composition{
		Upstreams: []config.Upstream{
			{ID: "a", Endpoint: "https://node.example"},
			{ID: "b", Endpoint: "https://node.example:443"},
		},
		TipTracking: &TipTracking{Interval: time.Hour},
	}
	// Repetition makes leaked sampler goroutines dominate unrelated runtime
	// activity; the bounded wait accommodates their asynchronous exit.
	before := stdruntime.NumGoroutine()
	const attempts = 8
	for i := 0; i < attempts; i++ {
		_, err := Compose(input)
		if err == nil {
			t.Fatal("expected Compose to reject the collapsed basis")
		}
		if !strings.Contains(err.Error(), "upstreams[1]") || !strings.Contains(err.Error(), "same member as upstreams[0]") {
			t.Fatalf("normalization must name the duplicate, got %v", err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for stdruntime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if after := stdruntime.NumGoroutine(); after > before {
		t.Fatalf("failed Compose leaked goroutines: before=%d after=%d attempts=%d", before, after, attempts)
	}
}
