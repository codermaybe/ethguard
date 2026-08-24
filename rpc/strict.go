package rpc

import (
	"context"
	"encoding/json"
	"fmt"

	"ethguard/pool"
)

// StrictPolicy resolves by full agreement: every observation must normalize to the
// same canonical value, otherwise the conflict is reported with evidence.
type StrictPolicy struct{}

func (StrictPolicy) Prepare(_ context.Context, req Request) (Request, error) { return req, nil }

func (StrictPolicy) Normalize(value json.RawMessage) (string, error) {
	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(decoded)
	return string(canonical), err
}

func (p StrictPolicy) Resolve(obs []Observation) (Decision, error) {
	return ResolveStrict(p, obs)
}

// ResolveStrict groups observations by their normalized value and verifies
// that exactly one group remains.
func ResolveStrict(policy Policy, obs []Observation) (Decision, error) {
	groups := make(map[string][]*pool.Source)
	values := make(map[string]json.RawMessage)
	for _, item := range obs {
		if item.Err != nil {
			continue
		}
		key, err := policy.Normalize(item.Value)
		if err != nil {
			continue
		}
		groups[key] = append(groups[key], item.Source)
		values[key] = item.Value
	}
	if len(groups) == 0 {
		return Decision{}, fmt.Errorf("strict: no valid response")
	}
	if len(groups) != 1 {
		var firstKey string
		for key := range groups {
			firstKey = key
			break
		}
		decision := Decision{Status: Verified, Value: values[firstKey], Support: groups[firstKey]}
		for key, sources := range groups {
			if key != firstKey {
				decision.Reject = append(decision.Reject, sources...)
			}
		}
		return decision, ErrConflict
	}
	for key, support := range groups {
		return Decision{Status: Verified, Value: values[key], Support: support}, nil
	}
	panic("unreachable")
}
