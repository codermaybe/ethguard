package rpc

import (
	"encoding/json"
	"errors"
	"testing"
)

// The isolation contract: published results, capture snapshots, and Result
// copies own their mutable material. Mutating a clone must never reach back
// into the original, at every nested level the graph actually has.
func TestDecisionCloneIsolatesNestedMaterial(t *testing.T) {
	original := Decision{
		Sources:          []SourceSummary{{Source: SourceRef{ID: "a"}, Exchange: Exchange{Progress: ExchangeCompleted}, Answer: AnswerSummary{Kind: AnswerResult, Interpretation: Interpretation{State: InterpretationValidated}}, Relation: VerdictSupported}},
		Adjudication:     Adjudication{Basis: 1, Required: 1, Reason: ReasonAccepted, Candidates: []CandidateGroup{{Sources: []SourceRef{{ID: "a"}}}}},
		Status:           Corroborated,
		Value:            json.RawMessage(`{"a":1}`),
		BlockRef:         "0xabc",
		Reference:        &ReferenceEvidence{Tag: "latest", Hash: "0xabc", Height: 9, Status: Corroborated, Support: []SourceRef{{Name: "s1"}}},
		Participants:     []SourceRef{{Name: "s1"}, {Name: "s2"}},
		Support:          []SourceRef{{Name: "s1"}},
		Reject:           []SourceRef{{Name: "s2"}},
		Faults:           []error{errors.New("boom")},
		SourceFaults:     []SourceFault{{Source: SourceRef{Name: "s2"}, Err: errors.New("down")}},
		CollectionFaults: []error{errors.New("collect")},
		Provenance:       json.RawMessage(`{"p":true}`),
		Revert:           &RevertContext{Code: 3, Message: "revert"},
		Extensions: &ExtensionSet{Fields: []ExtensionField{{
			Kind: AcceptedExtension, Path: "extra", Value: json.RawMessage(`"x"`), Sources: []SourceRef{{Name: "s1"}},
		}}},
		Diagnostics: &FieldDiagnostics{
			Residuals: []FieldObservation{{Path: "r", Value: json.RawMessage(`1`), Sources: []SourceRef{{Name: "s1"}}}},
			Conflicts: []ConflictField{{Path: "c", Values: []FieldObservation{
				{Path: "c", Value: json.RawMessage(`1`), Sources: []SourceRef{{Name: "s1"}}},
				{Path: "c", Value: json.RawMessage(`2`), Sources: []SourceRef{{Name: "s2"}}},
			}}},
		},
		Dimensions:    []DimensionEvidence{{Name: "revert-data", Support: []SourceRef{{Name: "s1"}}, Abstain: []SourceRef{{Name: "s3"}}}},
		Prepared:      &PreparedRequest{Method: "eth_getBalance", Params: json.RawMessage(`["0x1","latest"]`)},
		DeletedFields: []string{"temp"},
	}
	clone := original.Clone()

	clone.Sources[0].Answer.Interpretation.State = InterpretationInternalFailure
	clone.Adjudication.Candidates[0].Sources[0].ID = "changed"
	if original.Sources[0].Answer.Interpretation.State != InterpretationValidated || original.Adjudication.Candidates[0].Sources[0].ID != "a" {
		t.Fatal("source summary/candidate groups leaked through clone")
	}
	clone.Reference.Support[0].Name = "mutated"
	clone.Reference.Hash = "0xdead"
	clone.Participants[0].Name = "mutated"
	clone.Support[0].Name = "mutated"
	clone.Reject[0].Name = "mutated"
	clone.Value[2] = 'b' // {"a":1} -> {"b":1}; catches a shared backing array
	clone.Provenance[7] = 'f'
	clone.Revert.Message = "mutated"
	clone.Extensions.Fields[0].Value = json.RawMessage(`"mutated"`)
	clone.Extensions.Fields[0].Sources[0].Name = "mutated"
	clone.Diagnostics.Residuals[0].Value = json.RawMessage(`9`)
	clone.Diagnostics.Residuals[0].Sources[0].Name = "mutated"
	clone.Diagnostics.Conflicts[0].Values[0].Value = json.RawMessage(`9`)
	clone.Diagnostics.Conflicts[0].Values[0].Sources[0].Name = "mutated"
	clone.Dimensions[0].Support[0].Name = "mutated"
	clone.Dimensions[0].Abstain[0].Name = "mutated"
	clone.Prepared.Params[2] = 'X' // ["0x1",...] -> ["Xx1",...]
	clone.Prepared.Method = "mutated"
	clone.Faults[0] = errors.New("mutated")
	clone.SourceFaults[0].Err = errors.New("mutated")
	clone.CollectionFaults[0] = errors.New("mutated")
	clone.DeletedFields[0] = "mutated"

	if original.Reference.Hash != "0xabc" || original.Reference.Support[0].Name != "s1" {
		t.Fatalf("reference evidence leaked through the clone: %+v", original.Reference)
	}
	if original.Participants[0].Name != "s1" || original.Support[0].Name != "s1" || original.Reject[0].Name != "s2" {
		t.Fatalf("source lists leaked through the clone: %+v", original)
	}
	if string(original.Value) != `{"a":1}` {
		t.Fatalf("value bytes leaked through the clone: %s", original.Value)
	}
	if string(original.Provenance) != `{"p":true}` {
		t.Fatalf("provenance bytes leaked through the clone: %s", original.Provenance)
	}
	if original.Revert.Message != "revert" {
		t.Fatalf("revert context leaked through the clone: %+v", original.Revert)
	}
	if string(original.Extensions.Fields[0].Value) != `"x"` || original.Extensions.Fields[0].Sources[0].Name != "s1" {
		t.Fatalf("extensions leaked through the clone: %+v", original.Extensions)
	}
	if string(original.Diagnostics.Residuals[0].Value) != `1` || original.Diagnostics.Residuals[0].Sources[0].Name != "s1" {
		t.Fatalf("diagnostic residuals leaked through the clone: %+v", original.Diagnostics)
	}
	if string(original.Diagnostics.Conflicts[0].Values[0].Value) != `1` || original.Diagnostics.Conflicts[0].Values[0].Sources[0].Name != "s1" {
		t.Fatalf("diagnostic conflicts leaked through the clone: %+v", original.Diagnostics)
	}
	if original.Dimensions[0].Support[0].Name != "s1" || original.Dimensions[0].Abstain[0].Name != "s3" {
		t.Fatalf("dimensions leaked through the clone: %+v", original.Dimensions)
	}
	if string(original.Prepared.Params) != `["0x1","latest"]` || original.Prepared.Method != "eth_getBalance" {
		t.Fatalf("prepared request leaked through the clone: %+v", original.Prepared)
	}
	if original.Faults[0].Error() != "boom" || original.SourceFaults[0].Err.Error() != "down" || original.CollectionFaults[0].Error() != "collect" {
		t.Fatalf("fault slices leaked through the clone: %+v", original)
	}
	if original.DeletedFields[0] != "temp" {
		t.Fatalf("deleted fields leaked through the clone: %v", original.DeletedFields)
	}

	// errors.Is survives the copy: the error values are shared by contract.
	sentinel := errors.New("class")
	withSentinel := Decision{Faults: []error{sentinel}}
	if !errors.Is(withSentinel.Clone().Faults[0], sentinel) {
		t.Fatal("clone lost errors.Is on faults")
	}
}

func TestDecisionCloneNilPointersStayNil(t *testing.T) {
	var original Decision
	clone := original.Clone()
	if clone.Reference != nil || clone.Extensions != nil || clone.Diagnostics != nil ||
		clone.Revert != nil || clone.Prepared != nil || clone.Dimensions != nil {
		t.Fatalf("clone materialized nil pointers: %+v", clone)
	}
	if len(clone.Participants) != 0 || len(clone.Value) != 0 {
		t.Fatalf("clone materialized empty slices: %+v", clone)
	}
}

func TestWriteDecisionCloneIsolatesRejections(t *testing.T) {
	original := WriteDecision{
		Adjudication: Adjudication{Candidates: []CandidateGroup{{Sources: []SourceRef{{ID: "a"}}}}},
		Status:       WriteUnconfirmed, Hash: "0xabc", SubmittedHash: "0xabc",
		Sources: []SourceAnswer{
			{SourceSummary: SourceSummary{Source: SourceRef{Name: "a"}, Relation: VerdictSupported}, Hash: "0xabc"},
			{SourceSummary: SourceSummary{Source: SourceRef{Name: "b"}, Relation: VerdictDissented}, Hash: "0xdead"},
			{SourceSummary: SourceSummary{Source: SourceRef{Name: "c"}}, Rejection: &RPCError{Code: -32000, Message: "nonce too low", Data: json.RawMessage(`"x"`), DataPresence: ErrorDataPresenceValue}},
			{SourceSummary: SourceSummary{Source: SourceRef{Name: "d"}, Relation: VerdictAbsent}},
		}, Faults: []error{errors.New("timeout")},
	}

	clone := original.Clone()
	clone.Sources[0].Answer.Cause = errors.New("changed")
	clone.Adjudication.Candidates[0].Sources[0].ID = "changed"
	if original.Sources[0].Answer.Cause != nil || original.Adjudication.Candidates[0].Sources[0].ID != "a" {
		t.Fatal("write summary/candidate groups leaked through clone")
	}
	clone.Sources[0].Source.Name = "mutated"
	clone.Sources[1].Hash = "mutated"
	clone.Sources[2].Rejection.Message = "mutated"
	clone.Sources[2].Rejection.Data[1] = 'y' // "x" -> "y", same length
	clone.Sources[3].Source.Name = "mutated"
	clone.Faults[0] = errors.New("mutated")

	if original.Sources[0].Source.Name != "a" || original.Sources[1].Hash != "0xdead" {
		t.Fatalf("answer lists leaked through the clone: %+v", original)
	}
	if original.Sources[2].Rejection.Message != "nonce too low" || string(original.Sources[2].Rejection.Data) != `"x"` {
		t.Fatalf("rejection bodies leaked through the clone: %+v", original.Sources[2].Rejection)
	}
	if original.Sources[3].Source.Name != "d" || original.Faults[0].Error() != "timeout" {
		t.Fatalf("silent list or faults leaked through the clone: %+v", original)
	}
}

func TestReferenceEvidenceCloneIsolatesFaultSlices(t *testing.T) {
	original := ReferenceEvidence{
		Sources:      []SourceSummary{{Source: SourceRef{ID: "a"}}},
		Adjudication: Adjudication{Candidates: []CandidateGroup{{Sources: []SourceRef{{ID: "a"}}}}},
		Tag:          "latest", Status: Corroborated,
		Participants:     []SourceRef{{Name: "a"}},
		Support:          []SourceRef{{Name: "a"}},
		Reject:           []SourceRef{{Name: "b"}},
		Faults:           []error{errors.New("f1")},
		SourceFaults:     []SourceFault{{Source: SourceRef{Name: "b"}, Err: errors.New("f2")}},
		CollectionFaults: []error{errors.New("f3")},
	}
	clone := original.Clone()
	clone.Sources[0].Source.ID = "changed"
	clone.Adjudication.Candidates[0].Sources[0].ID = "changed"
	if original.Sources[0].Source.ID != "a" || original.Adjudication.Candidates[0].Sources[0].ID != "a" {
		t.Fatal("reference summary/candidate groups leaked through clone")
	}
	clone.Participants[0].Name = "mutated"
	clone.Support[0].Name = "mutated"
	clone.Reject[0].Name = "mutated"
	clone.SourceFaults[0].Source.Name = "mutated"

	if original.Participants[0].Name != "a" || original.Support[0].Name != "a" || original.Reject[0].Name != "b" {
		t.Fatalf("reference lists leaked through the clone: %+v", original)
	}
	if original.SourceFaults[0].Source.Name != "b" {
		t.Fatalf("source faults leaked through the clone: %+v", original)
	}
}
