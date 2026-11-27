package rpcengine

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

// The canonical processing seam's contract tests: the result and observation
// constructors' handoff rules, and the strict resolver's canonical route —
// shared kernel with the raw route, canonical bytes as the key, raw hooks
// never consulted. These tests do not wire any production method; they pin
// the seam the transaction pilot will adopt.
func TestCanonicalResultConstructorContracts(t *testing.T) {
	if _, err := NewCanonicalResult(nil, nil); !errors.Is(err, rpc.ErrInvalidCanonicalResult) {
		t.Fatalf("empty value = %v", err)
	}
	if _, err := NewCanonicalResult(json.RawMessage(""), nil); !errors.Is(err, rpc.ErrInvalidCanonicalResult) {
		t.Fatalf("zero-length value = %v", err)
	}
	if _, err := NewCanonicalResult(json.RawMessage(`{"a":`), nil); !errors.Is(err, rpc.ErrInvalidCanonicalResult) {
		t.Fatalf("invalid JSON value = %v", err)
	}
	// A legal root null is a non-empty value.
	result, err := NewCanonicalResult(json.RawMessage("null"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Value()) != "null" {
		t.Fatalf("root null = %q", result.Value())
	}
	if _, err := NewCanonicalResult(json.RawMessage(`"x"`), json.RawMessage(`{`)); !errors.Is(err, rpc.ErrInvalidCanonicalResult) {
		t.Fatalf("invalid evidence = %v", err)
	}
	// Absent evidence reads as nil; explicit null evidence survives.
	absent, err := NewCanonicalResult(json.RawMessage(`"v"`), nil)
	if err != nil || absent.Evidence() != nil {
		t.Fatalf("absent evidence = %s, %v", absent.Evidence(), err)
	}
	nullEvidence, err := NewCanonicalResult(json.RawMessage(`"v"`), json.RawMessage(`null`))
	if err != nil || string(nullEvidence.Evidence()) != "null" {
		t.Fatalf("explicit null evidence = %s, %v", nullEvidence.Evidence(), err)
	}
	// The getters hand out copies: mutating them cannot reach the stored bytes.
	stored, err := NewCanonicalResult(json.RawMessage(`{"k":"v"}`), json.RawMessage(`"e"`))
	if err != nil {
		t.Fatal(err)
	}
	value := stored.Value()
	copyOfValue := append(json.RawMessage(nil), value...)
	value[0] = '['
	if string(stored.Value()) != string(copyOfValue) {
		t.Fatalf("value getter leaked the stored bytes: %s", stored.Value())
	}
	evidence := stored.Evidence()
	evidence[0] = '['
	if string(stored.Evidence()) != `"e"` {
		t.Fatalf("evidence getter leaked the stored bytes: %s", stored.Evidence())
	}
}

func TestCanonicalObservationPairing(t *testing.T) {
	result, err := NewCanonicalResult(json.RawMessage(`"v"`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewCanonicalObservation(rpc.Observation{Attempted: true, Source: rpc.SourceRef{ID: "a"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`null`)}}}, CanonicalResult{}); !errors.Is(err, rpc.ErrInvalidCanonicalResult) {
		t.Fatalf("success without result = %v", err)
	}
	if _, err := NewCanonicalObservation(rpc.Observation{Attempted: true, Source: rpc.SourceRef{ID: "a"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeStarted, Cause: errors.New("transport")}}}, result); !errors.Is(err, rpc.ErrInvalidCanonicalResult) {
		t.Fatalf("failure with result = %v", err)
	}
	if _, err := NewCanonicalObservation(rpc.Observation{Source: rpc.SourceRef{Name: "a"}}, result); !errors.Is(err, rpc.ErrInvalidCanonicalResult) {
		t.Fatalf("missing source identity = %v", err)
	}
	failed := rpc.Observation{Attempted: true, Source: rpc.SourceRef{ID: "a"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted, Cause: errors.New("transport")}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`"raw"`)}}}
	bound, err := NewCanonicalObservation(failed, CanonicalResult{})
	if err != nil {
		t.Fatal(err)
	}
	if bound.Observation().Response.Err() == nil || string(bound.Observation().Response.Answer.Result) != `"raw"` {
		t.Fatalf("failed observation lost its original shape: %+v", bound.Observation())
	}
	successful, err := NewCanonicalObservation(rpc.Observation{Attempted: true, Source: rpc.SourceRef{ID: "b"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`"raw"`)}}}, result)
	if err != nil {
		t.Fatal(err)
	}
	// The canonical bytes are never written back into the observation.
	if string(successful.Observation().Response.Answer.Result) != `"raw"` {
		t.Fatalf("observation value was rewritten: %s", successful.Observation().Response.Answer.Result)
	}
}

// canonicalObservation binds one canonical result to a named source slot.
// raw is the observation's original spelling; value is the processed bytes a
// processor would have produced — the two may differ, which is the seam's
// whole point: the key and the commit material are the processed bytes.
func canonicalObservation(t *testing.T, id, raw, value string) CanonicalObservation {
	t.Helper()
	result, err := NewCanonicalResult(json.RawMessage(value), nil)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := NewCanonicalObservation(rpc.Observation{Attempted: true, Source: rpc.SourceRef{ID: id}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(raw)}}}, result)
	if err != nil {
		t.Fatal(err)
	}
	return bound
}

// TestStrictCanonicalRouteNeverCallsRawHooks pins the seam's core promise:
// the canonical route resolves from the processed bytes alone. The raw
// resolver exposes only processed entries, with no raw execution hooks.
func TestStrictCanonicalRouteNeverCallsRawHooks(t *testing.T) {
	resolver := CanonicalStrict(RuleCompareMethodSpecific)
	outcome, err := resolver.ResolveCanonicalCorroborated(Corroboration{Floor: 2}, []CanonicalObservation{
		// Different raw spellings, one processed shape: the convergence a
		// method processor produces is what the route clusters on.
		canonicalObservation(t, "a", `{"k":"v","n":1}`, `{"k":"v","n":1}`),
		canonicalObservation(t, "b", `{ "n":1, "k":"v" }`, `{"k":"v","n":1}`),
	})
	if err != nil || outcome.Decision.Status != rpc.Corroborated {
		t.Fatalf("canonical resolution = %+v, %v", outcome.Decision, err)
	}

	// The two spellings clustered as one: the canonical bytes were compared
	// directly, not re-encoded from the raw observations.
	if len(outcome.Decision.Support) != 2 || len(outcome.Decision.Reject) != 0 {
		t.Fatalf("cluster shape: %+v", outcome.Decision)
	}
	if string(outcome.Decision.Value) != `{"k":"v","n":1}` {
		t.Fatalf("committed value = %s", outcome.Decision.Value)
	}
	if outcome.Decision.Representative.ID != "a" {
		t.Fatalf("representative = %s, want the first supporter in captured order", outcome.Decision.Representative.ID)
	}
}

// TestStrictCanonicalRouteRejectsBrokenIdentity mirrors the raw route's
// identity rules: an empty source identity and a duplicate are the
// process-conflict sentinel, not per-source skips.
func TestStrictCanonicalRouteRejectsBrokenIdentity(t *testing.T) {
	resolver := CanonicalStrict(RuleCompareMethodSpecific)
	empty := canonicalObservation(t, "a", `"x"`, `"x"`)
	empty.observation.Source.ID = ""
	if _, err := resolver.ResolveCanonicalCorroborated(Corroboration{Floor: 2}, []CanonicalObservation{empty}); !errors.Is(err, rpc.ErrInvalidOutcome) {
		t.Fatalf("empty identity = %v", err)
	}
	dup := []CanonicalObservation{canonicalObservation(t, "a", `"x"`, `"x"`), canonicalObservation(t, "a", `"x"`, `"x"`)}
	if _, err := resolver.ResolveCanonicalCorroborated(Corroboration{Floor: 2}, dup); !errors.Is(err, rpc.ErrInvalidOutcome) {
		t.Fatalf("duplicate identity = %v", err)
	}
}

// TestStrictCanonicalRouteKeepsFailedSlots pins the denominator contract:
// a failed observation keeps its slot as an unusable verdict with its own
// error, never shrinks the voting basis, and never acquires a manufactured
// response. Two valid answers beside one failure still corroborate against a
// three-member basis.
func TestStrictCanonicalRouteKeepsFailedSlots(t *testing.T) {
	resolver := CanonicalStrict(RuleCompareMethodSpecific)
	failed := rpc.Observation{Attempted: true, Source: rpc.SourceRef{ID: "c"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeStarted, Cause: errors.New("transport: refused")}}}
	bound, err := NewCanonicalObservation(failed, CanonicalResult{})
	if err != nil {
		t.Fatal(err)
	}
	outcome, resolveErr := resolver.ResolveCanonicalCorroborated(Corroboration{Floor: 2}, []CanonicalObservation{
		canonicalObservation(t, "a", `"x"`, `"x"`),
		bound,
		canonicalObservation(t, "b", `"x"`, `"x"`),
	})
	if resolveErr != nil || outcome.Decision.Status != rpc.Corroborated {
		t.Fatalf("failed slot shrank the basis: %+v, %v", outcome.Decision, resolveErr)
	}
	if len(outcome.Decision.Support) != 2 || len(outcome.Decision.Participants) != 0 || len(outcome.Decision.Faults) != 1 {
		t.Fatalf("fault shape: %+v", outcome.Decision)
	}
	verdict, ok := outcome.VerdictFor("c")
	if !ok || verdict.Kind != rpc.VerdictUnusable || verdict.Cause(bound.Observation().Response) == nil {
		t.Fatalf("failed slot verdict: %+v present=%v", verdict, ok)
	}
	if len(outcome.Verdicts) != 3 {
		t.Fatalf("statement covered %d sources, want all attempted slots", len(outcome.Verdicts))
	}
	if _, ok := outcome.VerdictFor("untried"); ok {
		t.Fatal("a source outside the observations acquired a verdict")
	}
}

// TestStrictCanonicalRouteEquivalenceWithRawRoute proves the two routes share
// one kernel: the same answers resolved through the raw hooks and through
// pre-computed canonical results produce the same decision facts — value,
// support, provenance, representative, status — for both the corroborated and
// the legacy (no-snapshot) entries.
func TestStrictCanonicalRouteEquivalenceWithRawRoute(t *testing.T) {
	normalize := func(raw json.RawMessage) (string, error) {
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return "", err
		}
		encoded, _ := json.Marshal(decoded)
		return string(encoded), nil
	}
	encoder := func(raw json.RawMessage) (json.RawMessage, error) {
		encoded, err := normalize(raw)
		return json.RawMessage(encoded), err
	}
	rawPolicy := Strict(normalize, WithCanonicalValue(encoder), WithQuorum(2))
	// The canonical contract consumes the processed values only.
	canonicalPolicy := CanonicalStrict(RuleCompareMethodSpecific)

	rawObservations := []rpc.Observation{
		{Attempted: true, Source: rpc.SourceRef{ID: "a"}, Evidence: json.RawMessage(`"ea"`), Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`{"k":"v"}`)}}},
		{Attempted: true, Source: rpc.SourceRef{ID: "b"}, Evidence: json.RawMessage(`"eb"`), Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`{ "k":"v" }`)}}},
	}
	var cobs []CanonicalObservation
	for _, item := range rawObservations {
		processedBytes, err := normalize(item.Response.Answer.Result)
		if err != nil {
			t.Fatal(err)
		}
		processed, err := NewCanonicalResult(json.RawMessage(processedBytes), item.Evidence)
		if err != nil {
			t.Fatal(err)
		}
		bound, err := NewCanonicalObservation(item, processed)
		if err != nil {
			t.Fatal(err)
		}
		cobs = append(cobs, bound)
	}

	corrob := Corroboration{Floor: 2}
	rawOutcome, err := rawPolicy.(CorroboratingResolver).ResolveCorroborated(corrob, rawObservations)
	if err != nil {
		t.Fatal(err)
	}
	canonicalOutcome, err := canonicalPolicy.ResolveCanonicalCorroborated(corrob, cobs)
	if err != nil {
		t.Fatal(err)
	}
	rawDecision, canonicalDecision := rawOutcome.Decision, canonicalOutcome.Decision
	if rawDecision.Status != canonicalDecision.Status || string(rawDecision.Value) != string(canonicalDecision.Value) {
		t.Fatalf("corroborated routes diverged: %+v vs %+v", rawDecision, canonicalDecision)
	}
	if string(rawDecision.Provenance) != string(canonicalDecision.Provenance) || rawDecision.Representative.ID != canonicalDecision.Representative.ID {
		t.Fatalf("provenance/representative diverged: %+v vs %+v", rawDecision, canonicalDecision)
	}
	if len(rawDecision.Support) != len(canonicalDecision.Support) || len(rawDecision.Reject) != len(canonicalDecision.Reject) {
		t.Fatalf("cluster membership diverged: %+v vs %+v", rawDecision, canonicalDecision)
	}

	rawLegacy, err := rawPolicy.Resolve(rawObservations)
	if err != nil {
		t.Fatal(err)
	}
	canonicalLegacy, err := canonicalPolicy.ResolveCanonical(cobs)
	if err != nil {
		t.Fatal(err)
	}
	if rawLegacy.Decision.Status != canonicalLegacy.Decision.Status || string(rawLegacy.Decision.Value) != string(canonicalLegacy.Decision.Value) {
		t.Fatalf("legacy routes diverged: %+v vs %+v", rawLegacy.Decision, canonicalLegacy.Decision)
	}
	// The raw route's legacy decision carries the representative's raw value;
	// the commit kernel overwrote it with the canonical bytes through the
	// attached encoder, so the two routes agree byte for byte.
	if string(rawLegacy.Decision.Value) != `{"k":"v"}` {
		t.Fatalf("raw legacy value = %s", rawLegacy.Decision.Value)
	}
}

// TestStrictCanonicalRouteFloorEnforcement pins the snapshot semantics: the
// frozen floor applies to the canonical route unchanged, and the no-snapshot
// entries keep valid-answer agreement and downgrade a lone answer.
func TestStrictCanonicalRouteFloorEnforcement(t *testing.T) {
	resolver := CanonicalStrict(RuleCompareMethodSpecific)
	cobs := []CanonicalObservation{
		canonicalObservation(t, "a", `"x"`, `"x"`),
		canonicalObservation(t, "b", `"x"`, `"x"`),
	}
	if _, err := resolver.ResolveCanonicalCorroborated(Corroboration{Floor: 3}, cobs); !errors.Is(err, rpc.ErrInsufficientSupport) {
		t.Fatalf("floor 3 over 2 supporters = %v", err)
	}
	legacy, err := resolver.ResolveCanonical(cobs)
	if err != nil || legacy.Decision.Status != rpc.Corroborated {
		t.Fatalf("two agreeing legacy answers = %+v, %v", legacy.Decision, err)
	}
	// A resolver without corroboration options keeps the decision-0006
	// default on its canonical entries: a lone answer stays Selected.
	legacyPolicy := CanonicalStrict(RuleCompareMethodSpecific)
	single, err := legacyPolicy.ResolveCanonical([]CanonicalObservation{canonicalObservation(t, "a", `"x"`, `"x"`)})
	if err != nil || single.Decision.Status != rpc.Selected {
		t.Fatalf("lone legacy answer = %+v, %v (must stay Selected, not claim corroboration)", single.Decision, err)
	}
}

// TestStrictCanonicalRouteDissentAndProvenance pins disagreement handling on
// the canonical route: a losing canonical value is a dissent with its own
// verdict, the winner's provenance is the first supporter's processed
// evidence, and the committed bytes are the winner's canonical value.
func TestStrictCanonicalRouteDissentAndProvenance(t *testing.T) {
	resolver := CanonicalStrict(RuleCompareMethodSpecific)
	winner, err := NewCanonicalResult(json.RawMessage(`"win"`), json.RawMessage(`"winner-evidence"`))
	if err != nil {
		t.Fatal(err)
	}
	loser, err := NewCanonicalResult(json.RawMessage(`"lose"`), json.RawMessage(`"loser-evidence"`))
	if err != nil {
		t.Fatal(err)
	}
	bind := func(id string, result CanonicalResult) CanonicalObservation {
		bound, err := NewCanonicalObservation(rpc.Observation{Attempted: true, Source: rpc.SourceRef{ID: id}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: result.Value()}}}, result)
		if err != nil {
			t.Fatal(err)
		}
		return bound
	}
	outcome, resolveErr := resolver.ResolveCanonicalCorroborated(Corroboration{Floor: 2}, []CanonicalObservation{
		bind("b", winner),
		bind("a", loser),
		bind("c", winner),
	})
	if resolveErr != nil || outcome.Decision.Status != rpc.Corroborated {
		t.Fatalf("majority dispute = %+v, %v", outcome.Decision, resolveErr)
	}
	if string(outcome.Decision.Value) != `"win"` {
		t.Fatalf("committed value = %s", outcome.Decision.Value)
	}
	if string(outcome.Decision.Provenance) != `"winner-evidence"` {
		t.Fatalf("provenance = %s, want the first supporter's processed evidence", outcome.Decision.Provenance)
	}
	if outcome.Decision.Representative.ID != "b" {
		t.Fatalf("representative = %s, want the first captured supporter", outcome.Decision.Representative.ID)
	}
	loserVerdict, ok := outcome.VerdictFor("a")
	if !ok || loserVerdict.Kind != rpc.VerdictDissented {
		t.Fatalf("losing source verdict: %+v present=%v", loserVerdict, ok)
	}
	if len(outcome.Decision.Reject) != 1 || outcome.Decision.Reject[0].ID != "a" {
		t.Fatalf("reject list: %v", outcome.Decision.Reject)
	}
}
