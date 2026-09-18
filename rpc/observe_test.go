package rpc

import "testing"

func TestAggregateFieldsSeparatesCommonResidualAndConflict(t *testing.T) {
	observations := []Observation{
		{Source: SourceRef{ID: "a"}, Value: []byte(`{"result":{"number":"0x01","hash":"0xaaa","l1Fee":"0x10"}}`)},
		{Source: SourceRef{ID: "b"}, Value: []byte(`{"result":{"number":"0x01","hash":"0xaaa","l1Fee":"0x10"}}`)},
		{Source: SourceRef{ID: "c"}, Value: []byte(`{"result":{"number":"0x02","hash":"0xbbb","vendor":"x"}}`)},
	}

	result, err := AggregateFields(observations, 2)
	if err != nil {
		t.Fatalf("AggregateFields() error = %v", err)
	}

	if got := fieldValue(result.Common, "/result/number"); got != `"0x01"` {
		t.Fatalf("common number = %s, want %q", got, `"0x01"`)
	}
	if got := fieldValue(result.Common, "/result/hash"); got != `"0xaaa"` {
		t.Fatalf("common hash = %s, want %q", got, `"0xaaa"`)
	}
	if got := fieldValue(result.Common, "/result/l1Fee"); got != `"0x10"` {
		t.Fatalf("common extension = %s, want %q", got, `"0x10"`)
	}
	if len(result.Conflicts) != 0 {
		t.Fatalf("conflicts = %v, want none: only one value reaches quorum", result.Conflicts)
	}
	if got := fieldValue(result.Residuals, "/result/vendor"); got != `"x"` {
		t.Fatalf("residual vendor = %s, want %q", got, `"x"`)
	}
	if got := fieldValue(result.Residuals, "/result/number"); got != `"0x02"` {
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
	if len(result.Common) != 0 {
		t.Fatalf("common = %v, want none", result.Common)
	}
	if len(result.Conflicts) != 1 || result.Conflicts[0].Path != "/result/value" {
		t.Fatalf("conflicts = %v, want value conflict", result.Conflicts)
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
	if got := fieldValue(result.Common, "/result/n"); got != "900719925474099312345" {
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

func fieldValue(fields []FieldObservation, path string) string {
	for _, field := range fields {
		if field.Path == path {
			return string(field.Value)
		}
	}
	return ""
}
