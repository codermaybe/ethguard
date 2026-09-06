package rpc

import (
	"encoding/json"
	"errors"
	"testing"
)

// finObs builds one finalized-head observation reporting the given height and
// hash, with equivalent hexadecimal spellings accepted by the normalizer.
func finObs(height string, hash string) Observation {
	return obs(`{"number":"` + height + `","hash":"` + hash + `"}`)
}

func heightsOf(p Policy, values ...string) (Decision, error) {
	observations := make([]Observation, 0, len(values))
	for _, v := range values {
		observations = append(observations, finObs(v, "0xh"+v))
	}
	return p.Resolve(observations)
}

func TestFinalizedHeadMedianReferenceBeatsInflatedLie(t *testing.T) {
	p := FinalizedHead(32)
	decision, err := p.Resolve([]Observation{
		finObs("0x64", "0xha"),
		finObs("0x64", "0xha"),
		finObs("0x65", "0xhb"),
		finObs("0x7fffffffffffffff", "0xlie"), // absurd inflation
	})
	if err != nil {
		t.Fatalf("an inflated minority lie must not break the pin: %v", err)
	}
	if decision.Status != Verified {
		t.Fatalf("expected verified pin at the honest height, got %v", decision.Status)
	}
	var head struct {
		Number string `json:"number"`
		Hash   string `json:"hash"`
	}
	if err := json.Unmarshal(decision.Value, &head); err != nil || head.Number != "0x64" {
		t.Fatalf("expected the honest lowest height to win, got %s (%v)", decision.Value, err)
	}
	// Neither the liar's view nor the honest-but-different height vanish:
	// both sit above the reference, so both appear as dissent evidence.
	if len(decision.Reject) != 2 {
		t.Fatalf("expected the divergent and inflated reporters as reject evidence, got %+v", decision)
	}
}

func TestFinalizedHeadExcludesViewsTrailingTheMedian(t *testing.T) {
	p := FinalizedHead(16)
	decision, err := p.Resolve([]Observation{
		finObs("0x64", "0xha"),
		finObs("0x64", "0xha"),
		finObs("0x65", "0xhb"),
		finObs("0xa", "0xstale"), // 90 below the reference 100, bound 16
	})
	if err != nil {
		t.Fatalf("a trailing minority must not break the pin: %v", err)
	}
	if decision.Status != Verified {
		t.Fatalf("expected verified pin, got %v", decision.Status)
	}
	if len(decision.Reject) != 2 || len(decision.Faults) != 1 {
		t.Fatalf("expected the stale view as fault and both non-winners as reject evidence, got %+v", decision)
	}
}

func TestFinalizedHeadStalenessBoundaryIsExclusive(t *testing.T) {
	p := FinalizedHead(16)
	// Exactly the bound behind the reference stays valid; one more block
	// behind is excluded. Heights 100/100/84: reference 100, 84 trails by
	// exactly 16.
	boundary, err := heightsOf(p, "0x64", "0x64", "0x54")
	if err != nil {
		t.Fatalf("trailing exactly maxLag must stay valid: %v", err)
	}
	if boundary.Status != Selected || len(boundary.Support) != 1 || len(boundary.Faults) != 0 {
		t.Fatalf("exact boundary must remain the single-source pin: %+v", boundary)
	}
	var head finalizedHead
	if err := json.Unmarshal(boundary.Value, &head); err != nil || head.Number != "0x54" {
		t.Fatalf("expected height 84, got %s", boundary.Value)
	}
	// 83 trails by 17: stale. Remaining views still pin the winner, and the
	// stale view is the only fault (the winner's co-reporter supports).
	decision, err := heightsOf(p, "0x64", "0x64", "0x53")
	if err != nil {
		t.Fatalf("honest majority must still pin: %v", err)
	}
	if decision.Status != Verified || len(decision.Reject) != 1 || len(decision.Faults) != 1 {
		t.Fatalf("expected the 17-behind view excluded, got %+v", decision)
	}
}

func TestFinalizedHeadEvenCountUsesLowerMedian(t *testing.T) {
	// Lower median 100 keeps 84 exactly at the bound; upper median 101
	// would exclude it. Assert the pin itself, not only successful resolution.
	decision, err := heightsOf(FinalizedHead(16), "0x54", "0x64", "0x65", "0x66")
	if err != nil {
		t.Fatal(err)
	}
	var head finalizedHead
	if err := json.Unmarshal(decision.Value, &head); err != nil || head.Number != "0x54" {
		t.Fatalf("expected height 84 at the lower-median boundary, got %s", decision.Value)
	}
	if decision.Status != Selected || len(decision.Support) != 1 || len(decision.Reject) != 3 || len(decision.Faults) != 0 {
		t.Fatalf("expected fresh single-source pin with three other views: %+v", decision)
	}
}

func TestFinalizedHeadZeroHeightIsValid(t *testing.T) {
	p := FinalizedHead(32)
	decision, err := heightsOf(p, "0x0", "0x0", "0x0")
	if err != nil || decision.Status != Verified {
		t.Fatalf("genesis-height views must verify, got %+v err=%v", decision, err)
	}
}

func TestFinalizedHeadEquivalentSpellingsAgree(t *testing.T) {
	p := FinalizedHead(32)
	decision, err := p.Resolve([]Observation{
		finObs("0x010f", "0xha"),
		finObs("0x10f", "0xha"),
	})
	if err != nil || decision.Status != Verified {
		t.Fatalf("equivalent hex spellings must cluster and corroborate, got %+v err=%v", decision, err)
	}
}

func TestFinalizedHeadForkAtSelectedHeightFailsClosed(t *testing.T) {
	p := FinalizedHead(32)
	decision, err := p.Resolve([]Observation{
		finObs("0x64", "0xha"),
		finObs("0x64", "0xhb"), // two hashes, one height
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected fork conflict, got %v", err)
	}
	if len(decision.Support) != 2 {
		t.Fatalf("expected both fork reporters in evidence, got %+v", decision)
	}
}

func TestFinalizedHeadSingleViewIsSelected(t *testing.T) {
	p := FinalizedHead(32)
	decision, err := heightsOf(p, "0x64")
	if err != nil || decision.Status != Selected {
		t.Fatalf("one uncorroborated view must stay selected, got %+v err=%v", decision, err)
	}
}

func TestFinalizedHeadUnparseableHeightsRideAsFaults(t *testing.T) {
	p := FinalizedHead(32)
	decision, err := p.Resolve([]Observation{
		finObs("123", "0xha"), // bare decimal: malformed, not a decimal spelling
		{Source: src(), Err: errors.New("timeout")},
	})
	if !errors.Is(err, ErrNoValidResponse) {
		t.Fatalf("expected no-valid-response class, got %v", err)
	}
	if len(decision.Faults) != 1 {
		t.Fatalf("expected the unparseable height as a fault, got %+v", decision.Faults)
	}
}
