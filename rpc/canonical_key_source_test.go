package rpc

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// The key source is a declaration at assembly, and the two declarations have to
// keep meaning opposite things. A folding normalizer that returns one key for
// every payload is the worst case: it cannot tell two payloads apart. With
// WithCanonicalValue the encoder decides, so the payloads separate. With
// WithCanonicalObject the normalizer decides, so they land in one cluster, and
// the encoding guard then refuses to commit two different byte strings as one
// result. That second direction is what a block method relies on when its key
// is narrower than its body, so it is a live path to keep tested rather than a
// historical one.
func TestClusterKeySourceGuardrail(t *testing.T) {
	fold := func(json.RawMessage) (string, error) { return "one-key", nil }
	// An encoder that is a faithful function of its input: different payloads
	// give different bytes, equal payloads give equal bytes.
	encode := func(raw json.RawMessage) (json.RawMessage, error) {
		return append(json.RawMessage(nil), raw...), nil
	}
	first := Observation{Source: src(), Value: json.RawMessage(`{"n":1}`)}
	second := Observation{Source: src(), Value: json.RawMessage(`{"n":2}`)}

	t.Run("canonical-bytes separates", func(t *testing.T) {
		policy := Strict(fold, WithCanonicalValue(encode), WithQuorum(1))
		decision, err := policy.(CorroboratingPolicy).ResolveCorroborated(Corroboration{Floor: 1}, []Observation{first, second})
		if !errors.Is(err, ErrConflict) || decision.Status != Unknown || len(decision.Support) != 1 || len(decision.Reject) != 1 || len(decision.Faults) != 0 {
			t.Fatalf("key came from the folding normalizer, not the encoder: %+v err %v", decision, err)
		}
	})

	t.Run("narrow key clusters then refuses to commit", func(t *testing.T) {
		policy := Strict(fold, WithCanonicalObject(encode), WithQuorum(1))
		decision, err := policy.(CorroboratingPolicy).ResolveCorroborated(Corroboration{Floor: 1}, []Observation{first, second})
		if !errors.Is(err, ErrMalformedResponse) || !strings.Contains(err.Error(), "result encoding disagrees within cluster") {
			t.Fatalf("narrow key did not reach the encoding guard: %+v err %v", decision, err)
		}
		if decision.Status != Unknown || len(decision.Value) != 0 {
			t.Fatalf("decision = %+v", decision)
		}
		// Both sources stayed in Support: the key clustered them, and the
		// guard refused the result without re-voting.
		if len(decision.Support) != 2 || len(decision.Reject) != 0 {
			t.Fatalf("guard changed membership: %+v", decision)
		}
	})
}
