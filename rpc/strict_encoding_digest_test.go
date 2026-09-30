package rpc

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func assertEncodingDigests(t *testing.T, fault error, observations []Observation) {
	t.Helper()
	pattern := regexp.MustCompile(`source ("(?:\\.|[^"])*") canonical length ([0-9]+) sha256 ([0-9a-f]{64})`)
	matches := pattern.FindAllStringSubmatch(fault.Error(), -1)
	if len(matches) != len(observations) {
		t.Fatalf("digest count = %d want %d: %v", len(matches), len(observations), fault)
	}
	for i, observation := range observations {
		id, err := strconv.Unquote(matches[i][1])
		if err != nil {
			t.Fatal(err)
		}
		length, err := strconv.Atoi(matches[i][2])
		if err != nil {
			t.Fatal(err)
		}
		var object struct {
			Value string `json:"value"`
		}
		if err := json.Unmarshal(observation.Value, &object); err != nil {
			t.Fatal(err)
		}
		canonical, err := json.Marshal(object.Value)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(canonical)
		if id != observation.Source.ID || length != len(canonical) || matches[i][3] != fmt.Sprintf("%x", digest) {
			t.Fatalf("source %s got %s length %d digest %s want length %d digest %x", observation.Source.ID, id, length, matches[i][3], len(canonical), digest)
		}
		if strings.Contains(fault.Error(), object.Value) {
			t.Fatalf("canonical value leaked: %v", fault)
		}
	}
	if strings.Contains(fault.Error(), `"key"`) {
		t.Fatalf("comparison key leaked: %v", fault)
	}
}

func TestStrictInconsistentClustersRecordActualEncodingDigests(t *testing.T) {
	observations := rejectedEncodingObservations()
	// Different lengths and repeated identical bytes prove source association.
	observations[2].Value = json.RawMessage(`{"key":"winner","value":"different-winner"}`)
	observations[4].Value = json.RawMessage(`{"key":"loser","value":"longer-loser"}`)
	// Quoted IDs must remain unambiguously associated with their digest.
	observations[0].Source.ID = "source \"quoted\""
	for _, entry := range []string{"Resolve", "ResolveCorroborated", "legacy"} {
		t.Run(entry, func(t *testing.T) {
			opts := []StrictOption{WithQuorum(3)}
			if entry == "legacy" {
				opts = nil
			}
			p, calls := rejectedEncodingPolicy(t, opts...)
			var d Decision
			var err error
			if entry == "ResolveCorroborated" {
				d, err = p.(CorroboratingPolicy).ResolveCorroborated(Corroboration{Floor: 3}, observations)
			} else {
				d, err = p.Resolve(observations)
			}
			if !errors.Is(err, ErrMalformedResponse) || !strings.Contains(err.Error(), "result encoding disagrees within cluster") || d.Status != Unknown || len(d.Value) != 0 || len(d.Faults) != 2 || *calls != 5 {
				t.Fatalf("decision = %+v err %v calls %d", d, err, *calls)
			}
			assertEncodingDigests(t, d.Faults[0], observations[3:])
			assertEncodingDigests(t, d.Faults[1], observations[:3])
		})
	}
}
