package runtime

import (
	"fmt"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

// The method registry is the ReadPlan registry of the default composition:
// one explicit execution plan per registered read method, compiled from the
// frozen spec inventory. A spec that cannot compose fails the build, so a
// method that lost its plan can never fall through to transparent
// forwarding. Decorator chains are not generated or checked here; the
// hand-written constructors remain the public compatibility surface the
// plan equivalence gate holds the plans to.

// buildMethodRegistry compiles the registered catalog into execution plans.
func buildMethodRegistry() (map[string]rpc.ReadPlan, error) {
	plans := make(map[string]rpc.ReadPlan, len(methods.Specs()))
	for _, spec := range methods.Specs() {
		plan, err := methods.CompilePlan(spec)
		if err != nil {
			return nil, fmt.Errorf("plan for %s: %w", spec.Method, err)
		}
		plans[spec.Method] = plan
	}
	return plans, nil
}
