package rpc

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func rejectedEncodingPolicy(t *testing.T, opts ...StrictOption) (Policy, *int) {
	t.Helper()
	calls := new(int)
	normalize := func(raw json.RawMessage) (string, error) {
		var x struct {
			Key string `json:"key"`
		}
		err := json.Unmarshal(raw, &x)
		return x.Key, err
	}
	encode := func(raw json.RawMessage) (json.RawMessage, error) {
		*calls++
		var x struct {
			Value string `json:"value"`
		}
		if err := json.Unmarshal(raw, &x); err != nil {
			return nil, err
		}
		return json.Marshal(x.Value)
	}
	return Strict(normalize, append([]StrictOption{WithCanonicalObject(encode)}, opts...)...), calls
}

func rejectedEncodingObservations() []Observation {
	return []Observation{obs(`{"key":"winner","value":"v1"}`), obs(`{"key":"winner","value":"v1"}`), obs(`{"key":"winner","value":"v1"}`), obs(`{"key":"loser","value":"v2"}`), obs(`{"key":"loser","value":"v3"}`)}
}

func TestStrictRejectedEncodingDiagnosticPreservesAdjudication(t *testing.T) {
	for _, mode := range []CorroborationMode{CorroborationMajority, CorroborationUnanimity} {
		for _, floor := range []int{3, 4} {
			for _, entry := range []string{"Resolve", "ResolveCorroborated"} {
				t.Run(mode.String()+"/floor"+string(rune('0'+floor))+"/"+entry, func(t *testing.T) {
					opts := []StrictOption{WithQuorum(floor)}
					if mode == CorroborationUnanimity {
						opts = append(opts, WithUnanimity())
					}
					policy, calls := rejectedEncodingPolicy(t, opts...)
					observations := rejectedEncodingObservations()
					var d Decision
					var err error
					if entry == "Resolve" {
						d, err = policy.Resolve(observations)
					} else {
						d, err = policy.(CorroboratingPolicy).ResolveCorroborated(Corroboration{Mode: mode, Floor: floor}, observations)
					}
					if mode == CorroborationMajority && floor == 3 {
						if err != nil || d.Status != Corroborated {
							t.Fatalf("winner = %+v err %v", d, err)
						}
					} else if !errors.Is(err, ErrConflict) || errors.Is(err, ErrMalformedResponse) || d.Status != Unknown {
						t.Fatalf("adjudication = %+v err %v", d, err)
					}
					if string(d.Value) != `"v1"` || len(d.Faults) != 1 || !errors.Is(d.Faults[0], ErrMalformedResponse) || !strings.Contains(d.Faults[0].Error(), "rejected cluster") {
						t.Fatalf("diagnostic = %+v", d)
					}
					wantSupport := []SourceRef{observations[0].Source, observations[1].Source, observations[2].Source}
					wantReject := []SourceRef{observations[3].Source, observations[4].Source}
					if !reflect.DeepEqual(d.Support, wantSupport) || !reflect.DeepEqual(d.Reject, wantReject) || *calls != 5 {
						t.Fatalf("votes/calls changed: %+v calls %d", d, *calls)
					}
				})
			}
		}
	}
}

func TestStrictLegacyRecordsRejectedEncodingWithoutChangingConflict(t *testing.T) {
	p, calls := rejectedEncodingPolicy(t)
	d, err := p.Resolve(rejectedEncodingObservations())
	if !errors.Is(err, ErrConflict) || errors.Is(err, ErrMalformedResponse) || d.Status != Unknown || len(d.Support) != 3 || len(d.Reject) != 2 || len(d.Faults) != 1 || *calls != 5 {
		t.Fatalf("legacy = %+v err %v calls %d", d, err, *calls)
	}
}

func TestStrictWinnerEncodingFailureStillFails(t *testing.T) {
	p, calls := rejectedEncodingPolicy(t, WithQuorum(3))
	observations := rejectedEncodingObservations()
	observations[2].Value = json.RawMessage(`{"key":"winner","value":"changed"}`)
	d, err := p.Resolve(observations)
	if !errors.Is(err, ErrMalformedResponse) || d.Status != Unknown || len(d.Value) != 0 || len(d.Support) != 3 || len(d.Reject) != 2 || len(d.Faults) != 2 || *calls != 5 {
		t.Fatalf("winner = %+v err %v calls %d", d, err, *calls)
	}
}

func TestStrictConsistentRejectedEncodingHasNoFault(t *testing.T) {
	p, _ := rejectedEncodingPolicy(t, WithQuorum(3))
	observations := rejectedEncodingObservations()
	observations[4].Value = observations[3].Value
	d, err := p.Resolve(observations)
	if err != nil || d.Status != Corroborated || len(d.Faults) != 0 {
		t.Fatalf("decision = %+v err %v", d, err)
	}
}
