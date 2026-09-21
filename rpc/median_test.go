package rpc

import (
	"encoding/json"
	"errors"
	"testing"
)

// decimal normalizes quoted integer strings, mirroring what the shared hex
// quantity normalizers hand to resolution.
func decimal(v json.RawMessage) (string, error) {
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return "", err
	}
	return s, nil
}

func TestMedianPicksLowerMedianOverDistinctValues(t *testing.T) {
	p := Median(decimal)
	decision, err := p.Resolve([]Observation{obs(`"1"`), obs(`"2"`), obs(`"100"`)})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != Selected {
		t.Fatalf("variable results must stay selected, got %v", decision.Status)
	}
	if string(decision.Value) != `"2"` {
		t.Fatalf("expected lower median 2, got %s", decision.Value)
	}
	if len(decision.Support) != 1 || len(decision.Reject) != 2 {
		t.Fatalf("expected the median reporter in support and outliers as evidence, got %+v", decision)
	}
}

func TestMedianEvenCountPrefersLowerMiddle(t *testing.T) {
	p := Median(decimal)
	decision, err := p.Resolve([]Observation{obs(`"1"`), obs(`"2"`), obs(`"3"`), obs(`"4"`)})
	if err != nil {
		t.Fatal(err)
	}
	if string(decision.Value) != `"2"` {
		t.Fatalf("expected lower-middle 2, got %s", decision.Value)
	}
	if len(decision.Support) != 1 || len(decision.Reject) != 3 {
		t.Fatalf("expected one supporter, got %+v", decision)
	}
}

func TestMedianUnanimousFleetStaysSelected(t *testing.T) {
	p := Median(decimal)
	decision, err := p.Resolve([]Observation{obs(`"7"`), obs(`"7"`), obs(`"7"`)})
	if err != nil {
		t.Fatal(err)
	}
	// The red line: agreement on a moving value is coincidence, so the
	// ceiling is Selected no matter how many sources agree.
	if decision.Status != Selected {
		t.Fatalf("median selection must never claim corroborated, got %v", decision.Status)
	}
	if len(decision.Support) != 3 || len(decision.Reject) != 0 {
		t.Fatalf("unanimous fleet should sit fully in support, got %+v", decision)
	}
}

func TestMedianDuplicatesClusterAroundWinner(t *testing.T) {
	p := Median(decimal)
	decision, err := p.Resolve([]Observation{obs(`"5"`), obs(`"900"`), obs(`"5"`)})
	if err != nil {
		t.Fatal(err)
	}
	if string(decision.Value) != `"5"` || len(decision.Support) != 2 || len(decision.Reject) != 1 {
		t.Fatalf("expected duplicated low cluster to win, got %s %+v", decision.Value, decision)
	}
}

func TestMedianUsesFirstPoolOrderSpellingOnEqualValues(t *testing.T) {
	p := Median(func(v json.RawMessage) (string, error) {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return "", err
		}
		return map[string]string{"0x01": "1", "0x1": "1", "0x2": "2"}[s], nil
	})
	decision, err := p.Resolve([]Observation{obs(`"0x01"`), obs(`"0x1"`), obs(`"0x2"`)})
	if err != nil {
		t.Fatal(err)
	}
	if string(decision.Value) != `"0x01"` {
		t.Fatalf("expected first pool-order spelling to win, got %s", decision.Value)
	}
}

func TestMedianInvalidResponsesRideAsFaults(t *testing.T) {
	p := Median(decimal)
	decision, err := p.Resolve([]Observation{
		obs(`"5"`),
		{Source: src(), Err: errors.New("timeout")},
		obs(`"not-a-number"`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(decision.Value) != `"5"` {
		t.Fatalf("expected the only valid sample to win, got %s", decision.Value)
	}
	if len(decision.Faults) != 1 {
		t.Fatalf("expected the unparseable payload as a fault, got %+v", decision.Faults)
	}
}

func TestMedianAllInvalidIsNoValidResponse(t *testing.T) {
	p := Median(decimal)
	_, err := p.Resolve([]Observation{obs(`"junk"`), {Source: src(), Err: errors.New("down")}})
	if !errors.Is(err, ErrNoValidResponse) {
		t.Fatalf("expected no-valid-response class, got %v", err)
	}
}
