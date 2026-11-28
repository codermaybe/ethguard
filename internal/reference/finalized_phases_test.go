package reference

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/rpcengine"
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
	views := []HeadView{
		{Source: rpc.SourceRef{Name: "a", ID: "a"}, Height: 16, Hash: "0xaaa"},
		{Source: rpc.SourceRef{Name: "b", ID: "b"}, Height: 16, Hash: "0xbbb"},
	}
	emissions := []HeadObservation{
		{Source: views[0].Source, Attempted: true, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: headJSON("0x10", "0xaaa")}}},
		{Source: views[1].Source, Attempted: true, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: headJSON("0x10", "0xbbb")}}},
	}
	ref, err := Finalized(context.Background(), fakeHeadReader{observations: emissions}, fakeHeadCodec{}, 32, 2, rpc.CorroborationMajority, time.Second, nil)
	if !errors.Is(err, rpc.ErrConflict) || ref.Adjudication.Reason != rpc.ReasonConflictingCandidates || len(ref.Adjudication.Candidates) != 2 {
		t.Fatalf("fork evidence lost: %+v, %v", ref, err)
	}
	for i, group := range ref.Adjudication.Candidates {
		if len(group.Sources) != 1 || group.Sources[0] != views[i].Source {
			t.Fatalf("candidate %d = %+v", i, group)
		}
		if ref.Sources[i].Exchange.Progress != rpc.ExchangeCompleted || ref.Sources[i].Answer.Kind != rpc.AnswerResult || ref.Sources[i].Answer.Interpretation.State != rpc.InterpretationAdmitted || ref.Sources[i].Relation != rpc.VerdictUnassessed {
			t.Fatalf("early comparison fabricated a final vote: %+v", ref.Sources[i])
		}
	}

}

// The voting basis outlives the first round: every first-round participant
// stays in the denominator with its actual response, sources at the
// candidate height carry their raw head, and sources above it are named for
// the confirmation round.
func TestSelectCandidateFreezesVotingBasis(t *testing.T) {
	emissions := []HeadObservation{
		{Source: rpc.SourceRef{Name: "a", ID: "a"}, Attempted: true, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: headJSON("0x10", "0xaaa")}}},
		{Source: rpc.SourceRef{Name: "b", ID: "b"}, Attempted: true, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: headJSON("0x10", "0xaaa")}}},
		{Source: rpc.SourceRef{Name: "c", ID: "c"}, Attempted: true, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: headJSON("0x12", "0xccc")}}},
	}
	views := []HeadView{
		{Source: emissions[0].Source, Height: 16, Hash: "0xaaa", Raw: emissions[0].Response.Answer.Result},
		{Source: emissions[1].Source, Height: 16, Hash: "0xaaa", Raw: emissions[1].Response.Answer.Result},
		{Source: emissions[2].Source, Height: 18, Hash: "0xccc", Raw: emissions[2].Response.Answer.Result},
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
	if sel.observations[0].Response.Err() != nil || string(sel.observations[0].Response.Answer.Result) != string(emissions[0].Response.Answer.Result) {
		t.Fatalf("supporting observation lost its head: %+v", sel.observations[0])
	}
	excluded := sel.observations[2].Response
	if excluded.Answer.Interpretation.State != rpc.InterpretationExcluded || excluded.Exchange != emissions[2].Response.Exchange || string(excluded.Answer.Result) != string(emissions[2].Response.Answer.Result) {
		t.Fatalf("candidate exclusion lost the actual response: %+v", excluded)
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
			{Attempted: true, Source: rpc.SourceRef{Name: "a", ID: "a"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: headJSON("0x10", "0xaaa")}}},
			{Attempted: true, Source: rpc.SourceRef{Name: "b", ID: "b"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: headJSON("0x12", "0xccc"), Interpretation: rpc.Interpretation{State: rpc.InterpretationExcluded, Cause: errors.New("no finalized support for height 16")}}}},
		},
		indexes:  map[string]int{"a": 0, "b": 1},
		missing:  []string{"b"},
		required: 2,
	}
	reader := fakeHeadReader{confirmations: []HeadObservation{
		{Source: rpc.SourceRef{Name: "b", ID: "b"}, Attempted: true, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: headJSON("0x10", "0xbbb")}}},
	}}
	var ref rpc.ReferenceEvidence
	var attempts []observe.SourceEvent
	_, reorgDetected := confirmSelection(context.Background(), reader, fakeHeadCodec{}, sel, 2, rpc.CorroborationMajority, time.Second, &ref, &attempts)
	if !reorgDetected {
		t.Fatal("confirmation hash differing from the candidate must flag a reorg")
	}
	confirmed := sel.observations[1].Response
	if confirmed.Exchange != reader.confirmations[0].Response.Exchange || string(confirmed.Answer.Result) != string(reader.confirmations[0].Response.Answer.Result) || confirmed.Answer.Interpretation.State != rpc.InterpretationRejected || !errors.Is(confirmed.Err(), rpc.ErrReorgDetected) {
		t.Fatalf("confirmation binding failure lost new response: %+v", confirmed)
	}
	found := false
	for _, fault := range rpcengine.UnassessedOutcome(sel.observations).Decision.SourceFaults {
		if fault.Source.ID == "b" && strings.Contains(fault.Err.Error(), "confirm") {
			found = true
		}
	}
	if !found {
		t.Fatalf("reorg fault not attributed to the confirming source: %+v", rpcengine.UnassessedOutcome(sel.observations).Decision.SourceFaults)
	}
}

func TestFailedConfirmationReplacesEarlierResponse(t *testing.T) {
	source := rpc.SourceRef{ID: "a", Name: "a"}
	sel := selection{candidate: 16, candidateHashes: map[string]struct{}{"0xaaa": {}}, required: 1,
		indexes: map[string]int{"a": 0}, missing: []string{"a"},
		observations: []rpc.Observation{{Attempted: true, Source: source, Response: rpc.Response{
			Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted},
			Answer:   rpc.Answer{Kind: rpc.AnswerResult, Result: headJSON("0x12", "0xccc"), Interpretation: rpc.Interpretation{State: rpc.InterpretationExcluded, Cause: errors.New("not this candidate")}},
		}}},
	}
	failed := rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeResponded, HTTPStatus: 200, Cause: context.DeadlineExceeded}}
	reader := fakeHeadReader{confirmations: []HeadObservation{{Source: source, Response: failed, Attempted: true}}}
	var ref rpc.ReferenceEvidence
	var attempts []observe.SourceEvent
	policy, reorg := confirmSelection(context.Background(), reader, fakeHeadCodec{}, sel, 1, rpc.CorroborationMajority, time.Second, &ref, &attempts)
	got := sel.observations[0].Response
	if reorg || got.Exchange != failed.Exchange || got.Answer.Kind != rpc.AnswerUnassessed || got.Answer.Result != nil || got.Answer.Interpretation.State != rpc.InterpretationUnassessed {
		t.Fatalf("confirmation failure retained prior success or invented rejection: %+v", got)
	}
	if _, err := policy.Resolve(sel.observations); err == nil {
		t.Fatal("failed confirmation borrowed a vote from an earlier round")
	}
}
