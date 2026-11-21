package rpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"testing"
)

// quantity mirrors what real local policies hand to resolution: a
// normalizer that rejects payloads outside its value domain.
func quantity(v json.RawMessage) (string, error) {
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return "", err
	}
	if _, ok := new(big.Int).SetString(s, 10); !ok {
		return "", fmt.Errorf("not an integer quantity")
	}
	return s, nil
}

func TestLocalSelectsFirstValidInPoolOrder(t *testing.T) {
	p := Local(quantity)
	first := src()
	decision, err := p.Resolve([]Observation{
		{Attempted: true, Source: first, Response: Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerResult, Result: json.RawMessage(`"3"`)}}},
		obs(`"9"`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision.Status != Selected {
		t.Fatalf("local results must stay selected, got %v", decision.Decision.Status)
	}
	if string(decision.Decision.Value) != `"3"` {
		t.Fatalf("expected the first source's answer to stand, got %s", decision.Decision.Value)
	}
	if len(decision.Decision.Support) != 1 || decision.Decision.Support[0] != first || len(decision.Decision.Reject) != 0 {
		t.Fatalf("only the consulted source belongs in evidence, got %+v", decision)
	}
}

func TestLocalSkipsBrokenSourcesToNextValid(t *testing.T) {
	p := Local(quantity)
	second := src()
	decision, err := p.Resolve([]Observation{
		obs(`"junk"`),
		{Attempted: true, Source: src(), Response: Response{Exchange: Exchange{Progress: ExchangeStarted, Cause: errors.New("timeout")}}},
		{Attempted: true, Source: second, Response: Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerResult, Result: json.RawMessage(`"4"`)}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(decision.Decision.Value) != `"4"` || decision.Decision.Support[0] != second {
		t.Fatalf("expected the next healthy source to answer, got %s %+v", decision.Decision.Value, decision)
	}
	if len(decision.Decision.Faults) != 2 {
		t.Fatalf("expected the unparseable payload as a fault, got %+v", decision.Decision.Faults)
	}
}

func TestLocalAllInvalidIsNoValidResponse(t *testing.T) {
	p := Local(quantity)
	_, err := p.Resolve([]Observation{obs(`"junk"`), {Attempted: true, Source: src(), Response: Response{Exchange: Exchange{Progress: ExchangeStarted, Cause: errors.New("down")}}}})
	if !errors.Is(err, ErrNoValidResponse) {
		t.Fatalf("expected no-valid-response class, got %v", err)
	}
}

func TestLocalCanonicalFailureDoesNotSelectTheNextSource(t *testing.T) {
	normalize := func(v json.RawMessage) (string, error) {
		var text string
		if err := json.Unmarshal(v, &text); err != nil {
			return "", err
		}
		return text, nil
	}
	encode := func(v json.RawMessage) (json.RawMessage, error) {
		var text string
		if err := json.Unmarshal(v, &text); err != nil {
			return nil, err
		}
		if text == "bad" {
			return nil, errors.New("cannot encode")
		}
		return json.Marshal(text)
	}
	second := src()
	decision, err := Local(normalize, WithLocalCanonical(encode)).Resolve([]Observation{
		{Attempted: true, Source: src(), Response: Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerResult, Result: json.RawMessage(`"bad"`)}}},
		{Attempted: true, Source: second, Response: Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerResult, Result: json.RawMessage(`"ok"`)}}},
	})
	if err == nil || decision.Decision.Status != Unknown {
		t.Fatalf("err = %v, decision = %+v", err, decision)
	}
	if string(decision.Decision.Value) == `"ok"` || len(decision.Decision.Support) != 0 || decision.Decision.Representative != (SourceRef{}) {
		t.Fatalf("selected another source: %+v", decision)
	}
	if len(decision.Decision.Faults) == 0 {
		t.Fatal("expected an encode fault")
	}
}
