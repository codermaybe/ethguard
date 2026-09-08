package rpc

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestCorroborationSurvivesDecoratorComposition(t *testing.T) {
	decorators := map[string]func(Policy) Policy{
		"hash":    WithAnchor(nil, 0),
		"pending": RejectPending(0),
		"height":  WithAnchorHeight(nil, 0),
		"fields":  WithAnchorHeightFields(nil, 0, "fromBlock", "toBlock"),
	}
	for outerName, outer := range decorators {
		for innerName, inner := range decorators {
			t.Run(outerName+"/"+innerName, func(t *testing.T) {
				p := outer(inner(Strict(CanonicalJSON, WithQuorum(2))))
				cp, ok := p.(CorroboratingPolicy)
				if !ok {
					t.Fatal("lost capability")
				}
				observations := []Observation{obs(`1`), obs(`1`), obs(`2`)}
				if _, err := cp.ResolveCorroborated(Corroboration{Mode: CorroborationUnanimity, Floor: 2}, observations); !errors.Is(err, ErrConflict) {
					t.Fatalf("lost runtime mode: %v", err)
				}
				if _, err := cp.ResolveCorroborated(Corroboration{Floor: 3}, observations); !errors.Is(err, ErrConflict) {
					t.Fatalf("lost runtime floor: %v", err)
				}
				observations[2].Err = errors.New("gated")
				observations = append(observations, errObs(), errObs())
				if _, err := cp.ResolveCorroborated(Corroboration{Floor: 2}, observations); !errors.Is(err, ErrConflict) {
					t.Fatalf("lost voting basis: %v", err)
				}
				for _, nonCorroborating := range []Policy{Median(func(raw json.RawMessage) (string, error) { return string(raw), nil }), ForwardPolicy{}, SelectedPolicy{}} {
					if _, ok := outer(inner(nonCorroborating)).(CorroboratingPolicy); ok {
						t.Fatal("invented capability")
					}
				}
			})
		}
	}
}
