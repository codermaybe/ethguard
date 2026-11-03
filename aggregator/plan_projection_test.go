package aggregator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

// The planned read's projector binding. A plan names its observation
// projector as a business identity; the plan path resolves that ID and
// never reads the method name. These fixtures pin both directions of the
// rule: a projector bound under an ID the method name does not equal is
// still found, and a read that declares no projector stays unprojected even
// when a method-name binding exists in the registry.

// probeProjector is a counting observation projector; its extension set is
// empty because the tests' fact is that projection ran, not what it
// produced.
type probeProjector struct{ projected int }

func (p *probeProjector) ProjectObservation(json.RawMessage, rpc.Evaluation) (rpc.ExtensionSet, error) {
	p.projected++
	return rpc.ExtensionSet{}, nil
}

// probeProjectorPlan compiles the probe spec and declares id as its
// projector's business identity.
func probeProjectorPlan(t *testing.T, id string) rpc.ReadPlan {
	t.Helper()
	spec := probeSpec()
	spec.ObservationProjectorID = id
	plan, err := methods.CompilePlan(spec)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

// TestPlannedReadResolvesTheDeclaredProjectorID proves the plan path binds
// by projector ID: the projector is registered only under an identity that
// differs from both the method name and the legacy method key, and the read
// still projects. The projector state must advance exactly once, so the
// read cannot have found the projector twice or projected the legacy way.
func TestPlannedReadResolvesTheDeclaredProjectorID(t *testing.T) {
	const projectorAlias = "scalar-under-test-identity"
	_, p := probeNodes(t, "0x64", 3)
	plan := probeProjectorPlan(t, projectorAlias)
	projector := &probeProjector{}
	agg := New(p, WithCorroboration(mustShared(t)), WithReferenceStage(&scriptedStage{}),
		WithPlans(map[string]rpc.ReadPlan{probeMethod: plan}),
		WithProjectors(map[string]rpc.ObservationProjector{projectorAlias: projector}))
	decision, aggErr := agg.Aggregate(context.Background(), rpc.Request{Method: probeMethod, Params: []any{"0x1", "pending"}})
	if aggErr != nil {
		t.Fatalf("projected read failed: %v", aggErr)
	}
	if decision.Extensions == nil {
		t.Fatalf("read did not project through the declared %q identity: %+v", projectorAlias, decision)
	}
	if projector.projected != 1 {
		t.Fatalf("projector ran %d times, want once", projector.projected)
	}
}

// TestPlannedReadWithoutProjectorIDStaysUnprojected proves the empty
// declaration is a fact, not a fallback: a plan that declares no projector
// stays unprojected even though a projector is registered under the plain
// method name, and the read neither projects nor records a projection fault.
func TestPlannedReadWithoutProjectorIDStaysUnprojected(t *testing.T) {
	_, p := probeNodes(t, "0x64", 3)
	plan := probeProjectorPlan(t, "")
	projector := &probeProjector{}
	agg := New(p, WithCorroboration(mustShared(t)), WithReferenceStage(&scriptedStage{}),
		WithPlans(map[string]rpc.ReadPlan{probeMethod: plan}),
		WithProjectors(map[string]rpc.ObservationProjector{probeMethod: projector}))
	decision, aggErr := agg.Aggregate(context.Background(), rpc.Request{Method: probeMethod, Params: []any{"0x1", "pending"}})
	if aggErr != nil {
		t.Fatalf("unprojected read failed: %v", aggErr)
	}
	if decision.Extensions != nil {
		t.Fatalf("read without a declared projector projected anyway: %+v", decision.Extensions)
	}
	if projector.projected != 0 {
		t.Fatalf("method-name projector ran %d times, want 0", projector.projected)
	}
	for _, fault := range decision.Faults {
		if strings.Contains(fault.Error(), "projection") {
			t.Fatalf("unprojected read recorded a projection fault: %v", fault)
		}
	}
}
