package rpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/jsonsafe"
)

func TestAggregateFieldsMatchesFrozenReference(t *testing.T) {
	for _, fixture := range aggregateBenchmarkCases() {
		t.Run(fixture.name, func(t *testing.T) {
			for _, quorum := range []int{1, 2, 3, 4} {
				assertAggregateMatchesReference(t, fixture.observations, quorum)
			}
		})
	}
	for _, fixture := range []struct {
		name string
		raw  []string
	}{
		{name: "root values", raw: []string{`null`, `{}`, `[]`, `true`, `42`, `"hello"`}},
		{name: "number spellings", raw: []string{`1`, `1.0`, `1e0`, `1`, `1.0`}},
		{name: "large numbers", raw: []string{`1e400`, `900719925474099312345`, `1e400`}},
		{name: "escaped keys", raw: []string{`{"a/b":{"~x":1},"":[],"\u0061":true}`, `{"a":true,"":[],"a\u002fb":{"\u007ex":1}}`}},
		{name: "string canonicalization", raw: []string{`{"x":"<>&\u2028\u2029","y":"\ud800"}`, "{\"y\":\"\ufffd\",\"x\":\"<>&\u2028\u2029\"}"}},
		{name: "invalid UTF8 replacement", raw: []string{"\"\xff\"", `"\ufffd"`}},
		{name: "trailing value", raw: []string{`{"x":1}`, `{"x":1} []`, `{"x":1}`}},
		{name: "late duplicate key", raw: []string{`{"x":1,"nested":{"y":2}}`, `{"x":1,"nested":{"y":2,"\u0079":3}}`}},
		{name: "interleaved malformed", raw: []string{`{"x":1,"x":2}`, `{"x":1}`, `{"x":1} []`, `{"x":1}`, `{"x":1,"x":2}`}},
		{name: "all malformed", raw: []string{`{"x":1,"x":2}`, `{"x":1} []`, `{"x":1,"x":2}`}},
		{name: "NBSP", raw: []string{`1` + "\u00a0", `1`, `1`}},
		{name: "deep array", raw: []string{strings.Repeat("[", jsonsafe.MaxNestingDepth+1) + "0" + strings.Repeat("]", jsonsafe.MaxNestingDepth+1), `[]`}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			observations := make([]Observation, len(fixture.raw))
			for i, raw := range fixture.raw {
				observations[i] = aggregateObservation(fmt.Sprintf("source-%d", len(fixture.raw)-i), raw)
			}
			for _, quorum := range []int{1, 2, 3} {
				assertAggregateMatchesReference(t, observations, quorum)
			}
		})
	}
}

func TestAggregateFieldsRootAndPointerContract(t *testing.T) {
	for _, fixture := range []struct {
		name string
		raw  string
		want []FieldObservation
	}{
		{name: "scalar", raw: `42`, want: []FieldObservation{{Path: "", Value: json.RawMessage(`42`)}}},
		{name: "null", raw: `null`, want: []FieldObservation{{Path: "", Value: json.RawMessage(`null`)}}},
		{name: "empty object", raw: `{}`, want: []FieldObservation{{Path: "", Value: json.RawMessage(`{}`)}}},
		{name: "empty array", raw: `[]`, want: []FieldObservation{{Path: "", Value: json.RawMessage(`[]`)}}},
		{name: "huge exponent", raw: `1e400`, want: []FieldObservation{{Path: "", Value: json.RawMessage(`1e400`)}}},
		{name: "large integer", raw: `900719925474099312345`, want: []FieldObservation{{Path: "", Value: json.RawMessage(`900719925474099312345`)}}},
		{name: "nested empty containers", raw: `[{},[],null]`, want: []FieldObservation{
			{Path: "/0", Value: json.RawMessage(`{}`)}, {Path: "/1", Value: json.RawMessage(`[]`)}, {Path: "/2", Value: json.RawMessage(`null`)},
		}},
		{name: "escaped keys", raw: `{"a/b":{"~x":1},"":[],"\u0061":true}`, want: []FieldObservation{
			{Path: "/", Value: json.RawMessage(`[]`)}, {Path: "/a", Value: json.RawMessage(`true`)}, {Path: "/a~1b/~0x", Value: json.RawMessage(`1`)},
		}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			observations := []Observation{aggregateObservation("b", fixture.raw), aggregateObservation("a", fixture.raw)}
			got := assertAggregateMatchesReference(t, observations, 2)
			for i := range fixture.want {
				fixture.want[i].Sources = []SourceRef{{ID: "a"}, {ID: "b"}}
			}
			want := Evaluation{Accepted: fixture.want}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("evaluation = %#v, want %#v", got, want)
			}
		})
	}
}

func TestAggregateFieldsKeepsNumberSpellingsSeparate(t *testing.T) {
	observations := []Observation{aggregateObservation("c", `1e0`), aggregateObservation("a", `1`), aggregateObservation("b", `1.0`)}
	got := assertAggregateMatchesReference(t, observations, 2)
	want := Evaluation{Diagnostics: FieldDiagnostics{Residuals: []FieldObservation{
		{Path: "", Value: json.RawMessage(`1`), Sources: []SourceRef{{ID: "a"}}},
		{Path: "", Value: json.RawMessage(`1.0`), Sources: []SourceRef{{ID: "b"}}},
		{Path: "", Value: json.RawMessage(`1e0`), Sources: []SourceRef{{ID: "c"}}},
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("evaluation = %#v, want separate number spellings %#v", got, want)
	}
}

func TestAggregateFieldsMalformedResultNeverContributesPartialVotes(t *testing.T) {
	for _, fixture := range []struct {
		name  string
		raw   string
		class error
	}{
		{name: "late duplicate", raw: `{"x":1,"nested":{"y":2,"\u0079":3}}`, class: jsonsafe.ErrDuplicateKey},
		{name: "trailing value", raw: `{"x":1,"nested":{"y":2}} []`, class: jsonsafe.ErrTrailingData},
		{name: "NBSP", raw: `{"x":1,"nested":{"y":2}}` + "\u00a0", class: jsonsafe.ErrTrailingData},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			got := assertAggregateMatchesReference(t, []Observation{
				aggregateObservation("bad", fixture.raw),
				aggregateObservation("good", `{"x":1,"nested":{"y":2}}`),
			}, 2)
			if got.Accepted != nil || got.Diagnostics.Conflicts != nil || len(got.Diagnostics.Residuals) != 2 {
				t.Fatalf("partial vote from malformed source: %#v", got)
			}
			for _, field := range got.Diagnostics.Residuals {
				if !reflect.DeepEqual(field.Sources, []SourceRef{{ID: "good"}}) {
					t.Fatalf("field %q sources = %#v, want only good", field.Path, field.Sources)
				}
			}
			if len(got.Malformed) != 1 || got.Malformed[0].Source.ID != "bad" || !errors.Is(got.Malformed[0].Err, fixture.class) {
				t.Fatalf("malformed = %#v, want bad source with %v", got.Malformed, fixture.class)
			}
		})
	}
}

func TestAggregateFieldsPreservesErrorOrderAndPriority(t *testing.T) {
	failed := aggregateObservation("failed", `{"ignored":1}`)
	failed.Response.Exchange = Exchange{Progress: ExchangeStarted, Cause: errors.New("offline")}
	absent := aggregateObservation("absent", `null`)
	absent.Response.Answer.Result = nil
	for _, fixture := range []struct {
		name         string
		observations []Observation
		quorum       int
		wantError    string
	}{
		{name: "quorum before identity", observations: []Observation{aggregateObservation("", `{}`)}, quorum: 0, wantError: "rpc: observation quorum must be positive"},
		{name: "empty identity after malformed", observations: []Observation{aggregateObservation("bad", `{`), aggregateObservation("", `{}`)}, quorum: 2, wantError: "rpc: observation 1 has no source identity"},
		{name: "duplicate identity after malformed", observations: []Observation{aggregateObservation("same", `{`), aggregateObservation("same", `{}`)}, quorum: 2, wantError: `rpc: duplicate observation source "same" at 0 and 1`},
		{name: "failed response still has identity", observations: []Observation{failed, failed}, quorum: 2, wantError: `rpc: duplicate observation source "failed" at 0 and 1`},
		{name: "nil result still has identity", observations: []Observation{absent, absent}, quorum: 2, wantError: `rpc: duplicate observation source "absent" at 0 and 1`},
		{name: "all malformed reports first source", observations: []Observation{aggregateObservation("z", `{"x":1,"x":2}`), aggregateObservation("a", `[] []`)}, quorum: 2, wantError: `rpc: source "z": rpc: malformed response: jsonsafe: duplicate object key: "x"`},
		{name: "no observations", quorum: 1},
		{name: "failed and absent", observations: []Observation{failed, absent}, quorum: 1},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			assertAggregateMatchesReference(t, fixture.observations, fixture.quorum)
			got, err := AggregateFields(fixture.observations, fixture.quorum)
			if aggregationErrorText(err) != fixture.wantError {
				t.Fatalf("error = %v, want %q", err, fixture.wantError)
			}
			if !reflect.DeepEqual(got, Evaluation{}) {
				t.Fatalf("evaluation = %#v, want zero evaluation", got)
			}
		})
	}
	got := assertAggregateMatchesReference(t, []Observation{
		aggregateObservation("z", `{"x":1,"x":2}`), aggregateObservation("good", `1`),
		aggregateObservation("a", `[] []`), aggregateObservation("m", `{"x":1,"x":2}`),
	}, 2)
	var ids []string
	for _, malformed := range got.Malformed {
		ids = append(ids, malformed.Source.ID)
	}
	if !reflect.DeepEqual(ids, []string{"z", "a", "m"}) {
		t.Fatalf("malformed source order = %v, want input order [z a m]", ids)
	}
}

func TestAggregateFieldsTwoMatchingSourcesKeepFiveMemberDenominator(t *testing.T) {
	basis := FreezeBasis([]SourceRef{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "e"}}, Corroboration{Mode: CorroborationMajority, Floor: 2})
	observations := []Observation{aggregateObservation("b", `{"x":1,"empty":[]}`), aggregateObservation("a", `{"x":1,"empty":[]}`)}
	got, err := AggregateFieldsWithCorroboration(observations, basis)
	if err != nil {
		t.Fatal(err)
	}
	want := Evaluation{Diagnostics: FieldDiagnostics{Residuals: []FieldObservation{
		{Path: "/empty", Value: json.RawMessage(`[]`), Sources: []SourceRef{{ID: "a"}, {ID: "b"}}},
		{Path: "/x", Value: json.RawMessage(`1`), Sources: []SourceRef{{ID: "a"}, {ID: "b"}}},
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("evaluation = %#v, want two-source residuals %#v", got, want)
	}
	assertAggregateMatchesReference(t, observations, 3)
}

func TestAggregateFieldsIdenticalResultsSkipFailedAndAbsentResponses(t *testing.T) {
	failed := Observation{Source: SourceRef{ID: "failed"}, Response: Response{Exchange: Exchange{Progress: ExchangeStarted, Cause: errors.New("offline")}}}
	absent := aggregateObservation("absent", `null`)
	absent.Response.Answer.Result = nil
	empty := aggregateObservation("empty", "")
	observations := []Observation{
		failed, aggregateObservation("b", `{"x":1,"empty":[]}`), absent, empty,
		aggregateObservation("a", `{"x":1,"empty":[]}`),
	}
	got := assertAggregateMatchesReference(t, observations, 2)
	want := Evaluation{Accepted: []FieldObservation{
		{Path: "/empty", Value: json.RawMessage(`[]`), Sources: []SourceRef{{ID: "a"}, {ID: "b"}}},
		{Path: "/x", Value: json.RawMessage(`1`), Sources: []SourceRef{{ID: "a"}, {ID: "b"}}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("evaluation = %#v, want only eligible sources %#v", got, want)
	}
	for _, raw := range []string{`{"x":1,"x":2}`, " "} {
		bad := []Observation{failed, aggregateObservation("z", raw), absent, aggregateObservation("a", raw)}
		assertAggregateMatchesReference(t, bad, 2)
		got, err := AggregateFields(bad, 2)
		if !errors.Is(err, ErrMalformedResponse) || !strings.HasPrefix(err.Error(), `rpc: source "z":`) || !reflect.DeepEqual(got, Evaluation{}) {
			t.Fatalf("all malformed result = %#v, error %v; want first eligible source z", got, err)
		}
	}
}

func TestAggregateFieldsChecksUntrimmedResultSize(t *testing.T) {
	// A short valid number followed by excess JSON whitespace remains oversized.
	// Both sources deliberately share the input bytes; neither may contribute a
	// vote or have its length limit bypassed by exact-result reuse.
	oversized := bytes.Repeat([]byte{' '}, int(jsonsafe.MaxBodyBytes)+1)
	oversized[0] = '0'
	first := aggregateObservation("z", `0`)
	second := aggregateObservation("a", `0`)
	first.Response.Answer.Result = oversized
	second.Response.Answer.Result = oversized
	observations := []Observation{first, aggregateObservation("good", `0`), second}
	got := assertAggregateMatchesReference(t, observations, 2)
	if got.Accepted != nil || len(got.Diagnostics.Residuals) != 1 || !reflect.DeepEqual(got.Diagnostics.Residuals[0].Sources, []SourceRef{{ID: "good"}}) {
		t.Fatalf("oversized source contributed a vote: %#v", got)
	}
	if len(got.Malformed) != 2 || got.Malformed[0].Source.ID != "z" || got.Malformed[1].Source.ID != "a" {
		t.Fatalf("malformed sources = %#v, want z then a", got.Malformed)
	}
	for _, malformed := range got.Malformed {
		if !errors.Is(malformed.Err, jsonsafe.ErrTooLarge) {
			t.Fatalf("malformed source %q error = %v, want ErrTooLarge", malformed.Source.ID, malformed.Err)
		}
	}
	assertAggregateMatchesReference(t, []Observation{first, second}, 2)
}

func TestAggregateFieldsDeliversIndependentValuesAndSources(t *testing.T) {
	for _, fixture := range []struct {
		name        string
		third       string
		sharedInput bool
	}{
		{name: "dissent", third: `{"left":"same","right":"same","vote":2}`},
		{name: "identical", third: `{"left":"same","right":"same","vote":1}`},
		{name: "identical shared input", third: `{"left":"same","right":"same","vote":1}`, sharedInput: true},
	} {
		for _, quorum := range []int{1, 2, 4} {
			t.Run(fmt.Sprintf("%s/quorum-%d", fixture.name, quorum), func(t *testing.T) {
				observations := []Observation{
					aggregateObservation("c", `{"left":"same","right":"same","vote":1}`),
					aggregateObservation("a", `{"left":"same","right":"same","vote":1}`),
					aggregateObservation("b", fixture.third),
				}
				if fixture.sharedInput {
					observations[1].Response.Answer.Result = observations[0].Response.Answer.Result
					observations[2].Response.Answer.Result = observations[0].Response.Answer.Result
				}
				got := assertAggregateMatchesReference(t, observations, quorum)
				again, err := AggregateFields(observations, quorum)
				if err != nil {
					t.Fatal(err)
				}
				before, err := json.Marshal(got)
				if err != nil {
					t.Fatal(err)
				}
				for i := range observations {
					observations[i].Response.Answer.Result[0] = '!'
					observations[i].Source.ID = "changed-input"
				}
				after, err := json.Marshal(got)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatalf("input mutation changed delivered evaluation: %s, error %v", after, err)
				}
				fields := aggregationDeliveredFields(&got)
				otherFields := aggregationDeliveredFields(&again)
				for i, field := range fields {
					value := append(json.RawMessage(nil), field.Value...)
					sources := append([]SourceRef(nil), field.Sources...)
					field.Value[0] ^= 1
					field.Sources[0].ID = "changed-output"
					for j, other := range fields {
						if i != j && !reflect.DeepEqual(*other, *otherFields[j]) {
							t.Fatalf("mutating field %d changed distinct field %d", i, j)
						}
					}
					if !bytes.Equal(otherFields[i].Value, value) || !reflect.DeepEqual(otherFields[i].Sources, sources) {
						t.Fatalf("mutating field %d changed another invocation", i)
					}
					copy(field.Value, value)
					copy(field.Sources, sources)
				}
			})
		}
	}
}

func FuzzAggregateFieldsMatchesFrozenReference(f *testing.F) {
	for _, seed := range [][3]string{
		{`1`, `1`, `1`}, {`1`, `1.0`, `1e0`}, {`[]`, `{}`, `null`},
		{`{"a/b":1,"~x":[]}`, `{"~x":[],"a\u002fb":1}`, `{"a/b":2}`},
		{`{"x":1,"x":2}`, `{"x":1}`, `{"x":1} []`}, {`1e400`, `1e400`, `2e400`},
	} {
		f.Add([]byte(seed[0]), []byte(seed[1]), []byte(seed[2]), uint8(2))
	}
	f.Fuzz(func(t *testing.T, first, second, third []byte, quorum uint8) {
		if len(first)+len(second)+len(third) > 64<<10 {
			t.Skip()
		}
		observations := []Observation{aggregateObservation("c", string(first)), aggregateObservation("a", string(second)), aggregateObservation("b", string(third))}
		assertAggregateMatchesReference(t, observations, int(quorum%4)+1)
	})
}

func assertAggregateMatchesReference(t *testing.T, observations []Observation, quorum int) Evaluation {
	t.Helper()
	want, wantErr := aggregateFieldsReference(observations, quorum)
	got, gotErr := AggregateFields(observations, quorum)
	if aggregationErrorText(gotErr) != aggregationErrorText(wantErr) || errors.Is(gotErr, ErrMalformedResponse) != errors.Is(wantErr, ErrMalformedResponse) {
		t.Fatalf("error = %v, reference = %v", gotErr, wantErr)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("evaluation = %#v, reference = %#v", got, want)
	}
	return got
}

func aggregationErrorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func aggregationDeliveredFields(evaluation *Evaluation) []*FieldObservation {
	var fields []*FieldObservation
	for i := range evaluation.Accepted {
		fields = append(fields, &evaluation.Accepted[i])
	}
	for i := range evaluation.Diagnostics.Residuals {
		fields = append(fields, &evaluation.Diagnostics.Residuals[i])
	}
	for i := range evaluation.Diagnostics.Conflicts {
		for j := range evaluation.Diagnostics.Conflicts[i].Values {
			fields = append(fields, &evaluation.Diagnostics.Conflicts[i].Values[j])
		}
	}
	return fields
}
