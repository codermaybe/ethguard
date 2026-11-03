package shapeprobe

import (
	"encoding/json"
	"fmt"

	"github.com/codermaybe/ethguard/rpc/methods"
)

// NormalizeResult calls the registered plan resolver's Normalize. It does
// not define a comparison of its own. Methods outside the registry have no
// dispatch.
func NormalizeResult(method string, result json.RawMessage) (string, error) {
	plan, err := methods.PlanFor(method)
	if err != nil {
		return "", fmt.Errorf("shapeprobe: %s has no registered normalizer: %w", method, err)
	}
	return plan.Resolver.Normalize(result)
}
