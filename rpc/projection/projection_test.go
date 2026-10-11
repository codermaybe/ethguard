package projection

import (
	"encoding/json"
	"testing"

	"ethguard/rpc"
)

func TestProjectStandardSeparatesCoreExtensionsAndMissing(t *testing.T) {
	evaluation := rpc.Evaluation{
		Accepted: []rpc.FieldObservation{
			{Path: "/transactionHash", Value: json.RawMessage(`"0x01"`)},
			{Path: "/logs", Value: json.RawMessage(`[]`)},
			{Path: "/l1Fee", Value: json.RawMessage(`"0x10"`)},
		},
		Diagnostics: rpc.FieldDiagnostics{Conflicts: []rpc.ConflictField{{Path: "/status"}}},
	}

	result := ProjectStandard(evaluation, []StandardField{
		{Path: "/transactionHash", Required: true},
		{Path: "/blockHash", Required: true},
		{Path: "/logs", Required: true},
	})

	if len(result.Core) != 2 || result.Core[0].Path != "/logs" || result.Core[1].Path != "/transactionHash" {
		t.Fatalf("core = %#v, want sorted standard fields", result.Core)
	}
	if len(result.AcceptedExtensions) != 1 || result.AcceptedExtensions[0].Path != "/l1Fee" {
		t.Fatalf("extensions = %#v, want l1Fee", result.AcceptedExtensions)
	}
	if len(result.Missing) != 1 || result.Missing[0] != "/blockHash" {
		t.Fatalf("missing = %#v, want blockHash", result.Missing)
	}
	if len(evaluation.Diagnostics.Conflicts) != 1 || evaluation.Diagnostics.Conflicts[0].Path != "/status" {
		t.Fatalf("conflicts = %#v, want status", evaluation.Diagnostics.Conflicts)
	}
}

func TestProjectionExtensionsBindFingerprintAndEvidenceKind(t *testing.T) {
	evaluation := rpc.Evaluation{
		Accepted: []rpc.FieldObservation{{Path: "/l1Fee", Value: json.RawMessage(`"0x10"`), Sources: []rpc.SourceRef{{ID: "a"}, {ID: "b"}}}},
		Diagnostics: rpc.FieldDiagnostics{
			Residuals: []rpc.FieldObservation{{Path: "/revertReason", Value: json.RawMessage(`"oops"`), Sources: []rpc.SourceRef{{ID: "a"}}}},
			Conflicts: []rpc.ConflictField{{Path: "/debug", Values: []rpc.FieldObservation{{Path: "/debug", Value: json.RawMessage(`1`), Sources: []rpc.SourceRef{{ID: "a"}}}, {Path: "/debug", Value: json.RawMessage(`2`), Sources: []rpc.SourceRef{{ID: "b"}}}}}},
		},
	}
	p := ProjectStandard(evaluation, nil)
	set, err := p.Carrier([]byte(`{"hash":"0x01"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Fields) != 1 {
		t.Fatalf("fields = %d, want 1 accepted extension", len(set.Fields))
	}
	if set.Fields[0].Kind != rpc.AcceptedExtension {
		t.Fatalf("kinds = %#v", set.Fields)
	}
	if set.Fields[0].CoreObjectFingerprint == "" {
		t.Fatalf("fingerprint missing: %#v", set.Fields)
	}
}

func TestSchemaProjectorKeepsReceiptContainersCore(t *testing.T) {
	fields, err := ExecutionAPIStandardFields("eth_getTransactionReceipt")
	if err != nil {
		t.Fatal(err)
	}
	result := ProjectStandard(rpc.Evaluation{Accepted: []rpc.FieldObservation{
		{Path: "/logs", Value: json.RawMessage(`[]`)},
		{Path: "/l1Fee", Value: json.RawMessage(`"0x10"`)},
	}}, fields)
	if len(result.Core) != 1 || result.Core[0].Path != "/logs" {
		t.Fatalf("core = %#v, want receipt logs", result.Core)
	}
	if len(result.AcceptedExtensions) != 1 || result.AcceptedExtensions[0].Path != "/l1Fee" {
		t.Fatalf("extensions = %#v, want l1Fee", result.AcceptedExtensions)
	}
}

func TestProjectStandardMatchesArrayPathPatterns(t *testing.T) {
	fields, err := ExecutionAPIStandardFields("eth_getLogs")
	if err != nil {
		t.Fatal(err)
	}
	result := ProjectStandard(rpc.Evaluation{Accepted: []rpc.FieldObservation{
		{Path: "/0/address", Value: json.RawMessage(`"0x01"`)},
		{Path: "/1/topics/0", Value: json.RawMessage(`"0x02"`)},
		{Path: "/0/vendor", Value: json.RawMessage(`true`)},
	}}, fields)
	if len(result.Core) != 2 {
		t.Fatalf("core = %#v, want two wildcard matches", result.Core)
	}
	if len(result.AcceptedExtensions) != 1 || result.AcceptedExtensions[0].Path != "/0/vendor" {
		t.Fatalf("extensions = %#v, want vendor field", result.AcceptedExtensions)
	}
	if len(result.Missing) != 0 {
		t.Fatalf("missing = %#v, wildcard required fields must not be globally required", result.Missing)
	}
}
