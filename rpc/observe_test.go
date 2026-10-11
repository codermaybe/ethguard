package rpc

import (
	"errors"
	"testing"
)

func TestAggregateFieldsSeparatesAcceptedAndDiagnostics(t *testing.T) {
	observations := []Observation{
		{Source: SourceRef{ID: "a"}, Value: []byte(`{"result":{"number":"0x01","hash":"0xaaa","l1Fee":"0x10"}}`)},
		{Source: SourceRef{ID: "b"}, Value: []byte(`{"result":{"number":"0x01","hash":"0xaaa","l1Fee":"0x10"}}`)},
		{Source: SourceRef{ID: "c"}, Value: []byte(`{"result":{"number":"0x02","hash":"0xbbb","vendor":"x"}}`)},
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
		{Source: SourceRef{ID: "a"}, Value: []byte(`{"result":{"value":1}}`)},
		{Source: SourceRef{ID: "b"}, Value: []byte(`{"result":{"value":1}}`)},
		{Source: SourceRef{ID: "c"}, Value: []byte(`{"result":{"value":2}}`)},
		{Source: SourceRef{ID: "d"}, Value: []byte(`{"result":{"value":2}}`)},
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
		{Source: SourceRef{ID: "a"}, Value: []byte(`{"result":{"n":900719925474099312345}}`)},
		{Source: SourceRef{ID: "b"}, Value: []byte(`{"result":{"n":900719925474099312345}}`)},
	}
	result, err := AggregateFields(observations, 2)
	if err != nil {
		t.Fatalf("AggregateFields() error = %v", err)
	}
	if got := fieldValue(result.Accepted, "/result/n"); got != "900719925474099312345" {
		t.Fatalf("number = %s, want exact input number", got)
	}

	_, err = AggregateFields([]Observation{{Source: SourceRef{ID: "a"}, Value: []byte(`{"result":{"x":1,"x":2}}`)}}, 1)
	if err == nil {
		t.Fatalf("duplicate key error = %v, want an error", err)
	}
}

func TestAggregateFieldsRejectsDuplicateSourcesAndInvalidQuorum(t *testing.T) {
	_, err := AggregateFields([]Observation{
		{Source: SourceRef{ID: "a"}, Value: []byte(`1`)},
		{Source: SourceRef{ID: "a"}, Value: []byte(`1`)},
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
	observations := []Observation{
		{Source: SourceRef{ID: "a"}, Value: []byte(`{"result":{"value":1}}`)},
		{Source: SourceRef{ID: "b"}, Value: []byte(`{"result":{"value":1}}`)},
		{Source: SourceRef{ID: "c"}, Value: []byte(`{"result":{"value":1}}`)},
		{Source: SourceRef{ID: "d"}, Err: errObserved()},
		{Source: SourceRef{ID: "e"}, Err: errObserved()},
	}

	result, err := AggregateFieldsWithCorroboration(observations, Corroboration{Mode: CorroborationMajority, Floor: 2})
	if err != nil {
		t.Fatalf("AggregateFieldsWithCorroboration() error = %v", err)
	}
	if got := fieldValue(result.Accepted, "/result/value"); got != "1" {
		t.Fatalf("common value = %s, want 1", got)
	}

	result, err = AggregateFieldsWithCorroboration([]Observation{
		observations[0],
		observations[1],
		observations[3],
		observations[4],
	}, Corroboration{Mode: CorroborationMajority, Floor: 2})
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

func errObserved() error { return errors.New("unavailable") }

func fieldValue(fields []FieldObservation, path string) string {
	for _, field := range fields {
		if field.Path == path {
			return string(field.Value)
		}
	}
	return ""
}
