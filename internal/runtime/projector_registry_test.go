package runtime

import (
	"fmt"
	"testing"

	"github.com/codermaybe/ethguard/rpc/methods"
)

// The projector-binding interlock. The default composition is data-driven:
// a plan names its projector by business ID, and the projector registry
// binds what each ID resolves to. These tests hold the two assemblies to
// each other — every declared ID must resolve, every shared family must be
// one identity, and a read without a declared projector must keep the empty
// declaration instead of falling back to the method name.

// TestProjectorRegistryBindsEveryDeclaredID proves the default binding is
// closed over the spec declarations: every projector ID the spec inventory
// names resolves in the assembled registry, and no spec declares an ID the
// registry silently drops.
func TestProjectorRegistryBindsEveryDeclaredID(t *testing.T) {
	projectors, err := buildProjectorRegistry()
	if err != nil {
		t.Fatal(err)
	}
	plans, err := buildMethodRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for method, plan := range plans {
		if plan.ProjectorID == "" {
			continue
		}
		if _, ok := projectors[plan.ProjectorID]; !ok {
			t.Errorf("%s declares projector %q with no runtime binding", method, plan.ProjectorID)
		}
	}
	for id := range projectors {
		bound := false
		for _, plan := range plans {
			if plan.ProjectorID == id {
				bound = true
				break
			}
		}
		if !bound {
			t.Errorf("registry binds projector %q no plan names", id)
		}
	}
}

// TestSharedProjectorIDIsNotTheMethodName proves the binding runs through
// the declared business identity, not the method name:
//
//   - the scalar family's ID differs from both methods' names, and both
//     methods' plans carry exactly that shared ID;
//   - both block methods carry one shared ID although they register under
//     two different method names.
func TestSharedProjectorIDIsNotTheMethodName(t *testing.T) {
	plans, err := buildMethodRegistry()
	if err != nil {
		t.Fatal(err)
	}
	sameID := plans[methods.MethodGetBalance].ProjectorID
	if sameID == "" || sameID == methods.MethodGetBalance {
		t.Fatalf("getBalance projector ID %q is empty or the method name, want a shared business ID %q", sameID, methods.ProjectorScalarStandard)
	}
	for _, method := range []string{methods.MethodCall, methods.MethodGetBalance} {
		if plans[method].ProjectorID != sameID {
			t.Errorf("%s carries projector ID %q shared family requires %q", method, plans[method].ProjectorID, sameID)
		}
	}
	blockID := plans[methods.MethodGetBlockByNumber].ProjectorID
	if blockID == "" || blockID == methods.MethodGetBlockByNumber || blockID == methods.MethodGetBlockByHash {
		t.Fatalf("block projector ID %q is empty or a method name, want the shared %q", blockID, methods.ProjectorBlock)
	}
	if plans[methods.MethodGetBlockByHash].ProjectorID != blockID {
		t.Errorf("block methods share the block projector: %q vs %q", plans[methods.MethodGetBlockByHash].ProjectorID, blockID)
	}
}

// TestProjectorRegistryBindsOneInstancePerFamily proves the registry is the
// single fact source behind the plans: the scalar family binds one shared
// projector instance for both readers, and every schema family binds its
// own distinct instance.
func TestProjectorRegistryBindsOneInstancePerFamily(t *testing.T) {
	projectors, err := buildProjectorRegistry()
	if err != nil {
		t.Fatal(err)
	}
	scalar := projectors[methods.ProjectorScalarStandard]
	if scalar == nil {
		t.Fatal("scalar family lost its runtime binding")
	}
	seen := map[string]string{projectorIdentity(scalar): methods.ProjectorScalarStandard}
	for _, binding := range projectionSchemas() {
		projector, ok := projectors[binding.id]
		if !ok || projector == nil {
			t.Fatalf("%s lost its runtime binding", binding.id)
		}
		if id, bound := seen[projectorIdentity(projector)]; bound {
			t.Errorf("%s and %s bind one projector instance", binding.id, id)
		}
		seen[projectorIdentity(projector)] = binding.id
	}
}

// projectorIdentity is the instance identity of one bound projector value:
// the value's own address, since projector values are not comparable keys.
func projectorIdentity(projector any) string {
	return fmt.Sprintf("%p", projector)
}
