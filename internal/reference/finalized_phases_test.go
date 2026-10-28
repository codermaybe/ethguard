package reference

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/rpc"
)

func headJSON(number, hash string) json.RawMessage {
	return json.RawMessage(`{"number":"` + number + `","hash":"` + hash + `"}`)
}

// The candidate height carrying two distinct hashes is a fleet dissent over
// what "finalized" means, not a quorum shortfall: selectCandidate must fail
// closed with the conflict sentinel before any confirmation round runs.
func TestSelectCandidateRejectsDistinctHashes(t *testing.T) {
	views := []view{
		{source: rpc.SourceRef{Name: "a", ID: "a"}, height: 16, hash: "0xaaa"},
		{source: rpc.SourceRef{Name: "b", ID: "b"}, height: 16, hash: "0xbbb"},
	}
	var ref rpc.ReferenceEvidence
	if _, err := selectCandidate(nil, views, 32, 2, &ref); !errors.Is(err, rpc.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

// The voting basis outlives the first round: every first-round participant
// stays in the denominator with a placeholder rejection, sources at the
// candidate height carry their raw head, and sources above it are named for
// the confirmation round.
func TestSelectCandidateFreezesVotingBasis(t *testing.T) {
	emissions := []HeadObservation{
		{Source: rpc.SourceRef{Name: "a", ID: "a"}, Value: headJSON("0x10", "0xaaa"), Attempted: true},
		{Source: rpc.SourceRef{Name: "b", ID: "b"}, Value: headJSON("0x10", "0xaaa"), Attempted: true},
		{Source: rpc.SourceRef{Name: "c", ID: "c"}, Value: headJSON("0x12", "0xccc"), Attempted: true},
	}
	views := []view{
		{source: emissions[0].Source, height: 16, hash: "0xaaa", raw: emissions[0].Value},
		{source: emissions[1].Source, height: 16, hash: "0xaaa", raw: emissions[1].Value},
		{source: emissions[2].Source, height: 18, hash: "0xccc", raw: emissions[2].Value},
	}
	var ref rpc.ReferenceEvidence
	sel, err := selectCandidate(emissions, views, 32, 2, &ref)
	if err != nil {
		t.Fatal(err)
	}
	if sel.candidate != 16 || sel.required != 2 {
		t.Fatalf("selection = candidate %d required %d, want 16/2", sel.candidate, sel.required)
	}
	if len(sel.observations) != 3 {
		t.Fatalf("basis holds %d observations, want all 3 participants", len(sel.observations))
	}
	if sel.observations[0].Err != nil || string(sel.observations[0].Value) != string(emissions[0].Value) {
		t.Fatalf("supporting observation lost its head: %+v", sel.observations[0])
	}
	if sel.observations[2].Err == nil {
		t.Fatal("above-candidate member must keep the placeholder rejection")
	}
	if len(sel.missing) != 1 || sel.missing[0] != "c" {
		t.Fatalf("missing = %v, want [c]", sel.missing)
	}
}

// A confirmation that contradicts the established candidate hash is a reorg:
// confirmSelection reports it and attributes the fault, leaving the verdict
// join to the orchestrator.
func TestConfirmSelectionDetectsReorg(t *testing.T) {
	sel := selection{
		candidate:       16,
		candidateHashes: map[string]struct{}{"0xaaa": {}},
		observations: []rpc.Observation{
			{Source: rpc.SourceRef{Name: "a", ID: "a"}, Value: headJSON("0x10", "0xaaa")},
			{Source: rpc.SourceRef{Name: "b", ID: "b"}, Err: errors.New("no finalized support for height 16")},
		},
		indexes:  map[string]int{"a": 0, "b": 1},
		missing:  []string{"b"},
		required: 2,
	}
	reader := fakeHeadReader{confirmations: []HeadObservation{
		{Source: rpc.SourceRef{Name: "b", ID: "b"}, Value: headJSON("0x10", "0xbbb"), Attempted: true},
	}}
	var ref rpc.ReferenceEvidence
	var attempts []observe.SourceEvent
	_, reorgDetected := confirmSelection(context.Background(), reader, sel, 2, rpc.CorroborationMajority, time.Second, &ref, &attempts)
	if !reorgDetected {
		t.Fatal("confirmation hash differing from the candidate must flag a reorg")
	}
	found := false
	for _, fault := range ref.SourceFaults {
		if fault.Source.ID == "b" && strings.Contains(fault.Err.Error(), "confirm") {
			found = true
		}
	}
	if !found {
		t.Fatalf("reorg fault not attributed to the confirming source: %+v", ref.SourceFaults)
	}
}
