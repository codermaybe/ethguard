package runtime

import (
	"fmt"

	"github.com/codermaybe/ethguard/internal/methods"
	"github.com/codermaybe/ethguard/internal/rpcengine"
)

// The method registry is the ReadPlan registry of the default composition:
// one explicit execution plan per registered read method, compiled from the
// frozen spec inventory. A spec that cannot compose fails the build, so a
// method that lost its plan can never fall through to transparent
// forwarding.

// buildMethodRegistry compiles the registered catalog into execution plans.
func buildMethodRegistry() (map[string]rpcengine.ReadPlan, error) {
	plans := make(map[string]rpcengine.ReadPlan, len(methods.Specs()))
	for _, spec := range methods.Specs() {
		plan, err := methods.CompilePlan(spec)
		if err != nil {
			return nil, fmt.Errorf("plan for %s: %w", spec.Method, err)
		}
		plans[spec.Method] = plan
	}
	return plans, nil
}
