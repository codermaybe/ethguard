package methods

import (
	"encoding/json"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

// The resolver capability interlock. The plan declares one resolver class,
// and the class — never a decorator count — decides what the plan exposes:
// only strict, block, and call-revert corroborate under an invocation's
// frozen snapshot, only median and local sample without clustering, and the
// corroborating classes state their per-source outcomes through the same
// resolver value. A plan that violates its own declaration cannot compile.

// TestEveryDeclaredResolverHonorsItsClass walks the frozen inventory: each
// compiled plan's resolver value must expose exactly the corroboration entry
// points its class declares, on the value itself — never through a wrapped
// decorator's forwarding.
func TestEveryDeclaredResolverHonorsItsClass(t *testing.T) {
	for _, spec := range Specs() {
		plan, err := CompilePlan(spec)
		if err != nil {
			t.Fatalf("%s: compile plan: %v", spec.Method, err)
		}
		assertClassBinding(t, spec.Method, plan)
	}
}

// assertClassBinding is the capability contract between the declared class
// and the assembled value, shared with validatePlanBindings so the compile
// rejection and the inventory audit cannot drift.
func assertClassBinding(t *testing.T, method string, plan rpc.ReadPlan) {
	t.Helper()
	_, corroborating := plan.Resolver.(rpc.CorroboratingPolicy)
	_, outcomeCorroborating := plan.Resolver.(rpc.CorroboratingOutcomeResolver)
	switch plan.Corroborating() {
	case true:
		if !corroborating || !outcomeCorroborating {
			t.Fatalf("%s: declared %q, but its value corroborates=%v outcomeCorroborates=%v",
				method, plan.ResolverKind, corroborating, outcomeCorroborating)
		}
	case false:
		if corroborating || outcomeCorroborating {
			t.Fatalf("%s: declared %q, but its value claims a corroboration capability without the class",
				method, plan.ResolverKind)
		}
	}
}

// TestDeclaredResolverClassesMapToTheirValues pins the frozen class table
// the kind switch declares: strict, block, and call-revert corroborate
// through their own resolver values; median and local never do, so their
// values must not expose a corroboration entry point a decorator count could
// later be confused for.
func TestDeclaredResolverClassesMapToTheirValues(t *testing.T) {
	kinds := map[rpc.ResolverKind]bool{
		rpc.ResolverStrict:     true,
		rpc.ResolverBlock:      true,
		rpc.ResolverCallRevert: true,
		rpc.ResolverMedian:     false,
		rpc.ResolverLocal:      false,
	}
	for _, spec := range Specs() {
		plan, err := CompilePlan(spec)
		if err != nil {
			t.Fatalf("%s: compile plan: %v", spec.Method, err)
		}
		want, declared := kinds[plan.ResolverKind]
		if !declared {
			t.Fatalf("%s: resolver kind %q is not part of the frozen class table", spec.Method, plan.ResolverKind)
		}
		if plan.Corroborating() != want {
			t.Fatalf("%s: class %q corroborates=%v, want %v", spec.Method, plan.ResolverKind, plan.Corroborating(), want)
		}
	}
}

// TestCompilePlanRejectsCapabilityDrift pins the compile-time rejection:
// a plan whose resolver value disagrees with its declared class fails to
// assemble, in both directions — a sampling value under a corroborating
// declaration, and a corroborating value under a sampling declaration.
func TestCompilePlanRejectsCapabilityDrift(t *testing.T) {
	cases := []struct {
		name string
		plan rpc.ReadPlan
		spec MethodSpec
	}{
		{
			name: "sampling resolver under a corroborating declaration",
			plan: rpc.ReadPlan{Resolver: rpc.Median(feeHistoryNormalizeProbe), ResolverKind: rpc.ResolverStrict},
			spec: MethodSpec{Method: "eth_broken", Resolver: rpc.ResolverStrict},
		},
		{
			name: "corroborating resolver under a sampling declaration",
			plan: rpc.ReadPlan{Resolver: rpc.Strict(feeHistoryNormalizeProbe), ResolverKind: rpc.ResolverMedian},
			spec: MethodSpec{Method: "eth_broken", Resolver: rpc.ResolverMedian},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validatePlanBindings(tc.plan, tc.spec); err == nil {
				t.Fatal("a plan whose capability disagrees with its declaration compiled")
			}
		})
	}
}

func feeHistoryNormalizeProbe(json.RawMessage) (string, error) { return "", nil }
