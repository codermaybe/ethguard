package rpc

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestAggregateFieldsSeparatesAcceptedAndDiagnostics(t *testing.T) {
	observations := []Observation{
		{Attempted: true, Source: SourceRef{ID: "a"}, Response: Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerResult, Result: []byte(`{"result":{"number":"0x01","hash":"0xaaa","l1Fee":"0x10"}}`)}}},
		{Attempted: true, Source: SourceRef{ID: "b"}, Response: Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerResult, Result: []byte(`{"result":{"number":"0x01","hash":"0xaaa","l1Fee":"0x10"}}`)}}},
		{Attempted: true, Source: SourceRef{ID: "c"}, Response: Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerResult, Result: []byte(`{"result":{"number":"0x02","hash":"0xbbb","vendor":"x"}}`)}}},
	}

	result, err := AggregateFields(observations, 2)
	if err != nil {
		t.Fatalf("AggregateFields() error = %v", err)
	}

	if got := fieldValue(result.Accepted, "/result/number"); got != `"0x01"` {
		t.Fatalf("accepted number = %s, want %q", got, `"0x01"`)
	}
	if got := fieldValue(result.Accepted, "/result/hash"); got != `"0xaaa"` {
		t.Fatalf("accepted hash = %s, want %q", got, `"0xaaa"`)
	}
	if got := fieldValue(result.Accepted, "/result/l1Fee"); got != `"0x10"` {
		t.Fatalf("accepted extension = %s, want %q", got, `"0x10"`)
	}
	if len(result.Diagnostics.Conflicts) != 0 {
		t.Fatalf("conflicts = %v, want none: only one value reaches quorum", result.Diagnostics.Conflicts)
	}
	if got := fieldValue(result.Diagnostics.Residuals, "/result/vendor"); got != `"x"` {
		t.Fatalf("residual vendor = %s, want %q", got, `"x"`)
	}
	if got := fieldValue(result.Diagnostics.Residuals, "/result/number"); got != `"0x02"` {
		t.Fatalf("residual dissenting number = %s, want %q", got, `"0x02"`)
	}
}

func TestAggregateFieldsReportsMultipleQuorumValuesAsConflict(t *testing.T) {
	observations := []Observation{
		{Attempted: true, Source: SourceRef{ID: "a"}, Response: Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerResult, Result: []byte(`{"result":{"value":1}}`)}}},
		{Attempted: true, Source: SourceRef{ID: "b"}, Response: Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerResult, Result: []byte(`{"result":{"value":1}}`)}}},
		{Attempted: true, Source: SourceRef{ID: "c"}, Response: Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerResult, Result: []byte(`{"result":{"value":2}}`)}}},
		{Attempted: true, Source: SourceRef{ID: "d"}, Response: Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerResult, Result: []byte(`{"result":{"value":2}}`)}}},
	}

	result, err := AggregateFields(observations, 2)
	if err != nil {
		t.Fatalf("AggregateFields() error = %v", err)
	}
	if len(result.Accepted) != 0 {
		t.Fatalf("accepted = %v, want none", result.Accepted)
	}
	if len(result.Diagnostics.Conflicts) != 1 || result.Diagnostics.Conflicts[0].Path != "/result/value" {
		t.Fatalf("conflicts = %v, want value conflict", result.Diagnostics.Conflicts)
	}
}

func TestAggregateFieldsPreservesNumbersAndRejectsUnsafeJSON(t *testing.T) {
	observations := []Observation{
		{Attempted: true, Source: SourceRef{ID: "a"}, Response: Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerResult, Result: []byte(`{"result":{"n":900719925474099312345}}`)}}},
		{Attempted: true, Source: SourceRef{ID: "b"}, Response: Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerResult, Result: []byte(`{"result":{"n":900719925474099312345}}`)}}},
	}
	result, err := AggregateFields(observations, 2)
	if err != nil {
		t.Fatalf("AggregateFields() error = %v", err)
	}
	if got := fieldValue(result.Accepted, "/result/n"); got != "900719925474099312345" {
		t.Fatalf("number = %s, want exact input number", got)
	}

	_, err = AggregateFields([]Observation{{Attempted: true, Source: SourceRef{ID: "a"}, Response: Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerResult, Result: []byte(`{"result":{"x":1,"x":2}}`)}}}}, 1)
	if err == nil {
		t.Fatalf("duplicate key error = %v, want an error", err)
	}
}

func TestAggregateFieldsRejectsDuplicateSourcesAndInvalidQuorum(t *testing.T) {
	_, err := AggregateFields([]Observation{
		{Attempted: true, Source: SourceRef{ID: "a"}, Response: Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerResult, Result: []byte(`1`)}}},
		{Attempted: true, Source: SourceRef{ID: "a"}, Response: Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerResult, Result: []byte(`1`)}}},
	}, 1)
	if err == nil {
		t.Fatal("duplicate source accepted")
	}
	_, err = AggregateFields(nil, 0)
	if err == nil {
		t.Fatal("zero quorum accepted")
	}
}

func TestAggregateFieldsWithCorroborationUsesTheFixedBasis(t *testing.T) {
	basis := FreezeBasis([]SourceRef{
		{ID: "a"},
		{ID: "b"},
		{ID: "c"},
		{ID: "d"},
		{ID: "e"},
	}, Corroboration{Mode: CorroborationMajority, Floor: 2})
	observations := []Observation{
		{Attempted: true, Source: SourceRef{ID: "a"}, Response: Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerResult, Result: []byte(`{"result":{"value":1}}`)}}},
		{Attempted: true, Source: SourceRef{ID: "b"}, Response: Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerResult, Result: []byte(`{"result":{"value":1}}`)}}},
		{Attempted: true, Source: SourceRef{ID: "c"}, Response: Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerResult, Result: []byte(`{"result":{"value":1}}`)}}},
		{Attempted: true, Source: SourceRef{ID: "d"}, Response: Response{Exchange: Exchange{Progress: ExchangeStarted, Cause: errObserved()}}},
		{Attempted: true, Source: SourceRef{ID: "e"}, Response: Response{Exchange: Exchange{Progress: ExchangeStarted, Cause: errObserved()}}},
	}

	result, err := AggregateFieldsWithCorroboration(observations, basis)
	if err != nil {
		t.Fatalf("AggregateFieldsWithCorroboration() error = %v", err)
	}
	if got := fieldValue(result.Accepted, "/result/value"); got != "1" {
		t.Fatalf("common value = %s, want 1", got)
	}

	result, err = AggregateFieldsWithCorroboration([]Observation{observations[0]}, basis)
	if err != nil {
		t.Fatalf("AggregateFieldsWithCorroboration() with one response error = %v", err)
	}
	if got := fieldValue(result.Accepted, "/result/value"); got != "" {
		t.Fatalf("accepted value = %s, want no accepted value from 1 of 5", got)
	}
	if got := fieldValue(result.Diagnostics.Residuals, "/result/value"); got != "1" {
		t.Fatalf("residual value = %s, want 1", got)
	}

	fourMemberBasis := FreezeBasis(basis.Members[:4], basis.Corroboration)
	result, err = AggregateFieldsWithCorroboration([]Observation{
		observations[0],
		observations[1],
		observations[3],
	}, fourMemberBasis)
	if err != nil {
		t.Fatalf("AggregateFieldsWithCorroboration() with four members error = %v", err)
	}
	if got := fieldValue(result.Accepted, "/result/value"); got != "" {
		t.Fatalf("accepted value = %s, want no accepted value", got)
	}
	if got := fieldValue(result.Diagnostics.Residuals, "/result/value"); got != "1" {
		t.Fatalf("residual value = %s, want 1", got)
	}
}

func TestAcceptanceCalculatorUsesTheFixedBasis(t *testing.T) {
	calculator := newAcceptanceCalculator(Corroboration{Mode: CorroborationMajority, Floor: 2})
	for basis, want := range map[int]int{1: 2, 2: 2, 3: 2, 4: 3, 5: 3, 6: 4} {
		if got := calculator.required(basis); got != want {
			t.Fatalf("basis %d: required %d, want %d", basis, got, want)
		}
	}
	if calculator.accepts(5, 2) {
		t.Fatal("2 of 5 must not meet the fixed-basis threshold")
	}
	if !calculator.accepts(5, 3) {
		t.Fatal("3 of 5 must meet the fixed-basis threshold")
	}
}

func TestAggregateFieldsExactRawBucketsKeepInterleavedSourceVotes(t *testing.T) {
	observations := []Observation{
		aggregateObservation("a", `{"result":{"value":"same"}}`),
		aggregateObservation("b", `{"result":{"value":"other"}}`),
		aggregateObservation("c", `{"result":{"value":"same"}}`),
		aggregateObservation("d", `{"result":{"value":"other"}}`),
		aggregateObservation("e", `{"result":{"value":"same"}}`),
	}

	result, err := AggregateFields(observations, 3)
	if err != nil {
		t.Fatalf("AggregateFields() error = %v", err)
	}
	accepted := fieldObservationAt(result.Accepted, "/result/value")
	if accepted == nil || string(accepted.Value) != `"same"` {
		t.Fatalf("accepted value = %+v, want the three-source same bucket", accepted)
	}
	if got := sourceIDs(accepted.Sources); !equalStrings(got, []string{"a", "c", "e"}) {
		t.Fatalf("accepted sources = %v, want [a c e]", got)
	}
	residual := fieldObservationAt(result.Diagnostics.Residuals, "/result/value")
	if residual == nil || string(residual.Value) != `"other"` {
		t.Fatalf("residual value = %+v, want the dissenting bucket", residual)
	}
	if got := sourceIDs(residual.Sources); !equalStrings(got, []string{"b", "d"}) {
		t.Fatalf("residual sources = %v, want [b d]", got)
	}
}

func TestAggregateFieldsDifferentEnvelopesStillVoteForSameLeaf(t *testing.T) {
	observations := []Observation{
		aggregateObservation("a", `{"jsonrpc":"2.0","id":1,"result":{"value":7}}`),
		aggregateObservation("b", "{\n  \"jsonrpc\": \"2.0\", \"id\": 2, \"result\": {\"value\": 7}\n}"),
	}

	result, err := AggregateFields(observations, 2)
	if err != nil {
		t.Fatalf("AggregateFields() error = %v", err)
	}
	value := fieldObservationAt(result.Accepted, "/result/value")
	if value == nil || string(value.Value) != "7" {
		t.Fatalf("accepted value = %+v, want 7", value)
	}
	if got := sourceIDs(value.Sources); !equalStrings(got, []string{"a", "b"}) {
		t.Fatalf("leaf sources = %v, want [a b]", got)
	}
	if len(result.Diagnostics.Conflicts) != 0 {
		t.Fatalf("conflicts = %+v, want none", result.Diagnostics.Conflicts)
	}
}

func TestAggregateFieldsMalformedSourcesRemainInterleavedDiagnostics(t *testing.T) {
	observations := []Observation{
		aggregateObservation("a", `{"result":{"value":1}}`),
		aggregateObservation("b", `{"result":{"value":NaN}}`),
		aggregateObservation("c", `{"result":{"value":1}}`),
		aggregateObservation("d", `{"result":{"value":NaN}}`),
	}

	result, err := AggregateFields(observations, 2)
	if err != nil {
		t.Fatalf("one valid bucket must survive malformed interleaving: %v", err)
	}
	value := fieldObservationAt(result.Accepted, "/result/value")
	if value == nil || string(value.Value) != "1" {
		t.Fatalf("accepted value = %+v, want 1", value)
	}
	if got := sourceIDs(value.Sources); !equalStrings(got, []string{"a", "c"}) {
		t.Fatalf("accepted sources = %v, want [a c]", got)
	}
	if len(result.Malformed) != 2 || result.Malformed[0].Source.ID != "b" || result.Malformed[1].Source.ID != "d" {
		t.Fatalf("malformed sources = %+v, want b then d", result.Malformed)
	}
}

func TestAggregateFieldsPreservesRootLeavesAndEmptyContainers(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "root scalar", raw: `42`, want: "42"},
		{name: "root null", raw: `null`, want: "null"},
		{name: "root object", raw: `{}`, want: `{}`},
		{name: "root array", raw: `[]`, want: `[]`},
		{name: "nested leaves", raw: `{"result":null,"emptyObject":{},"emptyArray":[]}`, want: "nested"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := AggregateFields([]Observation{
				aggregateObservation("a", test.raw),
				aggregateObservation("b", test.raw),
			}, 2)
			if err != nil {
				t.Fatalf("AggregateFields() error = %v", err)
			}
			if test.want == "nested" {
				for path, want := range map[string]string{
					"/result": "null", "/emptyObject": "{}", "/emptyArray": "[]",
				} {
					if got := fieldValue(result.Accepted, path); got != want {
						t.Errorf("accepted %s = %s, want %s", path, got, want)
					}
				}
				return
			}
			if got := fieldValue(result.Accepted, ""); got != test.want {
				t.Fatalf("root accepted value = %s, want %s", got, test.want)
			}
		})
	}
}

func TestAggregateFieldsRejectsNBSPAsJSONWhitespace(t *testing.T) {
	bad := `{"result":{"value":1}}` + string([]byte{0xc2, 0xa0})
	result, err := AggregateFields([]Observation{
		aggregateObservation("a", `{"result":{"value":1}}`),
		aggregateObservation("b", bad),
		aggregateObservation("c", `{"result":{"value":1}}`),
	}, 2)
	if err != nil {
		t.Fatalf("valid sources must outvote NBSP-malformed source: %v", err)
	}
	if fieldValue(result.Accepted, "/result/value") != "1" {
		t.Fatalf("accepted value = %s, want 1", fieldValue(result.Accepted, "/result/value"))
	}
	if len(result.Malformed) != 1 || result.Malformed[0].Source.ID != "b" {
		t.Fatalf("malformed = %+v, want source b", result.Malformed)
	}
}

func aggregateObservation(id, raw string) Observation {
	return Observation{
		Attempted: true,
		Source:    SourceRef{ID: id},
		Response: Response{
			Exchange: Exchange{Progress: ExchangeCompleted},
			Answer:   Answer{Kind: AnswerResult, Result: json.RawMessage(raw)},
		},
	}
}

func fieldObservationAt(fields []FieldObservation, path string) *FieldObservation {
	for i := range fields {
		if fields[i].Path == path {
			return &fields[i]
		}
	}
	return nil
}

func sourceIDs(sources []SourceRef) []string {
	ids := make([]string, len(sources))
	for i, source := range sources {
		ids[i] = source.ID
	}
	return ids
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func errObserved() error { return errors.New("unavailable") }

func fieldValue(fields []FieldObservation, path string) string {
	for _, field := range fields {
		if field.Path == path {
			return string(field.Value)
		}
	}
	return ""
}

// A single source whose value fails generic JSON validation abstains from
// every field vote instead of killing the whole field evaluation — the same
// fault-and-continue model the strict and median resolvers apply — while the
// quorum denominator stays the fixed basis size. When every value-bearing
// source is malformed there is no vote left and the ErrMalformedResponse
// class is preserved.
func TestAggregateFieldsMalformedSourceAbstains(t *testing.T) {
	good := Observation{Attempted: true, Source: SourceRef{Name: "a", ID: "a"}, Response: Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerResult, Result: json.RawMessage(`{"blockNumber":"0x1"}`)}}}
	alsoGood := Observation{Attempted: true, Source: SourceRef{Name: "b", ID: "b"}, Response: Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerResult, Result: json.RawMessage(`{"blockNumber":"0x1"}`)}}}
	bad := Observation{Attempted: true, Source: SourceRef{Name: "c", ID: "c"}, Response: Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerResult, Result: json.RawMessage(`{"blockNumber":NaN}`)}}}

	observation, err := AggregateFields([]Observation{good, alsoGood, bad}, 2)
	if err != nil {
		t.Fatalf("one malformed source must not fail the evaluation: %v", err)
	}
	if len(observation.Accepted) != 1 || len(observation.Accepted[0].Sources) != 2 {
		t.Fatalf("accepted = %+v, want one field backed by the two healthy sources", observation.Accepted)
	}
	if len(observation.Malformed) != 1 || observation.Malformed[0].Source.ID != "c" || observation.Malformed[0].Err == nil {
		t.Fatalf("malformed = %+v, want source c attributed", observation.Malformed)
	}

	alsoBad := Observation{Attempted: true, Source: SourceRef{Name: "d", ID: "d"}, Response: Response{Exchange: Exchange{Progress: ExchangeCompleted}, Answer: Answer{Kind: AnswerResult, Result: json.RawMessage(`{"blockNumber":NaN}`)}}}
	if _, err := AggregateFields([]Observation{bad, alsoBad}, 2); !errors.Is(err, ErrMalformedResponse) {
		t.Fatalf("err = %v, want ErrMalformedResponse when every value-bearing source is malformed", err)
	}
}
