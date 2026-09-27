package methods

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"ethguard/rpc"
)

var errExecutionReverted = errors.New("execution reverted")

// TestRepresentativeIsFirstCapturedSource pins the source selection rule:
// when two sources share a comparison key but spell the payload differently,
// Value, Evidence, and the withheld data all come from the first source in
// the captured order. Swapping the order must change the Value bytes. The
// test must not assert that reordering leaves the bytes unchanged.
func TestRepresentativeIsFirstCapturedSource(t *testing.T) {
	a := rpc.SourceRef{Name: "a", ID: "a"}
	b := rpc.SourceRef{Name: "b", ID: "b"}

	t.Run("strict", func(t *testing.T) {
		policy := rpc.Strict(identityKey, rpc.WithQuorum(2))
		first := observation(a, `"0xA"`, `{"from":"a"}`)
		second := observation(b, `"0xa"`, `{"from":"b"}`)
		assertFirstSource(t, policy, first, second)
	})

	t.Run("call revert data returned", func(t *testing.T) {
		policy := CallPolicy(nil, rpc.WithQuorum(2))
		first := revertObservation(a, `"0xDEAD"`, `{"from":"a"}`)
		second := revertObservation(b, `"0xdead"`, `{"from":"b"}`)
		assertFirstSource(t, policy, first, second)
	})

	t.Run("call revert data withheld", func(t *testing.T) {
		policy := CallPolicy(nil, rpc.WithQuorum(2))
		// a and b assert the same data, so they form one data cluster; c and d
		// assert different data. N is 4, so the quorum is 3: the cluster of
		// two ranks first but falls short, and its data is withheld. The
		// representative is the first source of that cluster.
		c := rpc.SourceRef{Name: "c", ID: "c"}
		d := rpc.SourceRef{Name: "d", ID: "d"}
		first := revertObservation(a, `"0xAAAA"`, `{"from":"a"}`)
		second := revertObservation(b, `"0xaaaa"`, `{"from":"b"}`)
		other := revertObservation(c, `"0xBEEF"`, `{"from":"c"}`)
		fourth := revertObservation(d, `"0xbeef"`, `{"from":"d"}`)
		decision := resolvePair(t, policy, first, second, other, fourth)
		if bytes.Contains(decision.Value, []byte("0xAAAA")) || bytes.Contains(decision.Value, []byte("0xaaaa")) || bytes.Contains(decision.Value, []byte("0xBEEF")) {
			t.Fatalf("concrete data returned: %s", decision.Value)
		}
		if decision.Representative != a {
			t.Fatalf("representative = %+v, want a", decision.Representative)
		}
		if !bytes.Contains(decision.Evidence, []byte("0xAAAA")) || bytes.Contains(decision.Evidence, []byte("0xaaaa")) || bytes.Contains(decision.Evidence, []byte("0xBEEF")) {
			t.Fatalf("withheld = %s, want only a's data", decision.Evidence)
		}

		swapped := resolvePair(t, policy, second, first, other, fourth)
		if swapped.Representative != b {
			t.Fatalf("swapped representative = %+v, want b", swapped.Representative)
		}
		if !bytes.Contains(swapped.Evidence, []byte("0xaaaa")) || bytes.Contains(swapped.Evidence, []byte("0xAAAA")) {
			t.Fatalf("swapped withheld = %s, want only b's data", swapped.Evidence)
		}
		if bytes.Equal(decision.Evidence, swapped.Evidence) {
			t.Fatalf("swapping the captured order left the withheld bytes unchanged: %s", swapped.Evidence)
		}
	})
}

// TestRepresentativeWithoutEvidenceStaysEmpty pins the empty-evidence rule:
// when the first captured supporter has no admission evidence, the decision's
// evidence stays empty even though a later supporter of the same key has some.
func TestRepresentativeWithoutEvidenceStaysEmpty(t *testing.T) {
	a := rpc.SourceRef{Name: "a", ID: "a"}
	b := rpc.SourceRef{Name: "b", ID: "b"}

	t.Run("strict", func(t *testing.T) {
		policy := rpc.Strict(identityKey, rpc.WithQuorum(2))
		decision := resolvePair(t, policy,
			observation(a, `"0xA"`, ""),
			observation(b, `"0xa"`, `{"from":"b"}`),
		)
		if len(decision.Evidence) != 0 {
			t.Fatalf("evidence = %s, want empty", decision.Evidence)
		}
		if decision.Representative != a {
			t.Fatalf("representative = %+v, want a", decision.Representative)
		}
	})

	t.Run("call revert", func(t *testing.T) {
		policy := CallPolicy(nil, rpc.WithQuorum(2))
		decision := resolvePair(t, policy,
			revertObservation(a, `"0xDEAD"`, ""),
			revertObservation(b, `"0xdead"`, `{"from":"b"}`),
		)
		if len(decision.Evidence) != 0 {
			t.Fatalf("evidence = %s, want empty", decision.Evidence)
		}
		if decision.Representative != a {
			t.Fatalf("representative = %+v, want a", decision.Representative)
		}
	})
}

func identityKey(value json.RawMessage) (string, error) {
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return "", err
	}
	out := make([]byte, len(text))
	for i := range text {
		c := text[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out), nil
}

func observation(source rpc.SourceRef, value, evidence string) rpc.Observation {
	item := rpc.Observation{Source: source, Value: json.RawMessage(value)}
	if evidence != "" {
		item.Evidence = json.RawMessage(evidence)
	}
	return item
}

func revertObservation(source rpc.SourceRef, data, evidence string) rpc.Observation {
	body := &rpc.RPCError{Code: 3, Message: "execution reverted"}
	if data != "" {
		body.Data = json.RawMessage(data)
	}
	item := rpc.Observation{Source: source, Err: errExecutionReverted, RPCError: body}
	if evidence != "" {
		item.Evidence = json.RawMessage(evidence)
	}
	return item
}

func assertFirstSource(t *testing.T, policy rpc.Policy, first, second rpc.Observation) {
	t.Helper()
	decision := resolvePair(t, policy, first, second)
	if !bytes.Equal(decision.Value, payloadOf(first)) {
		t.Fatalf("value = %s, want first source %s", decision.Value, payloadOf(first))
	}
	if !bytes.Equal(decision.Evidence, first.Evidence) {
		t.Fatalf("evidence = %s, want %s", decision.Evidence, first.Evidence)
	}
	if decision.Representative != first.Source {
		t.Fatalf("representative = %+v, want %+v", decision.Representative, first.Source)
	}

	swapped := resolvePair(t, policy, second, first)
	if bytes.Equal(swapped.Value, decision.Value) {
		t.Fatalf("swapping the captured order left the value bytes unchanged: %s", swapped.Value)
	}
	if !bytes.Equal(swapped.Value, payloadOf(second)) {
		t.Fatalf("swapped value = %s, want %s", swapped.Value, payloadOf(second))
	}
	if swapped.Representative != second.Source {
		t.Fatalf("swapped representative = %+v, want %+v", swapped.Representative, second.Source)
	}
}

func resolvePair(t *testing.T, policy rpc.Policy, obs ...rpc.Observation) rpc.Decision {
	t.Helper()
	resolver, ok := policy.(rpc.CorroboratingPolicy)
	if !ok {
		t.Fatalf("%T has no ResolveCorroborated", policy)
	}
	decision, err := resolver.ResolveCorroborated(rpc.Corroboration{Floor: 2}, obs)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return decision
}

func payloadOf(item rpc.Observation) json.RawMessage {
	if item.RPCError != nil {
		return item.RPCError.Raw()
	}
	return item.Value
}
