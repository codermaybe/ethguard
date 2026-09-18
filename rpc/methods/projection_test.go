package methods

import (
	"encoding/json"
	"testing"

	"ethguard/rpc"
)

func TestProjectStandardSeparatesCoreExtensionsAndMissing(t *testing.T) {
	observation := rpc.CommonObservation{
		Common: []rpc.FieldObservation{
			{Path: "/transactionHash", Value: json.RawMessage(`"0x01"`)},
			{Path: "/logs", Value: json.RawMessage(`[]`)},
			{Path: "/l1Fee", Value: json.RawMessage(`"0x10"`)},
		},
		Conflicts: []rpc.ConflictField{{Path: "/status"}},
	}

	result := ProjectStandard(observation, []StandardField{
		{Path: "/transactionHash", Required: true},
		{Path: "/blockHash", Required: true},
		{Path: "/logs", Required: true},
	})

	if len(result.Core) != 2 || result.Core[0].Path != "/logs" || result.Core[1].Path != "/transactionHash" {
		t.Fatalf("core = %#v, want sorted standard fields", result.Core)
	}
	if len(result.Extensions) != 1 || result.Extensions[0].Path != "/l1Fee" {
		t.Fatalf("extensions = %#v, want l1Fee", result.Extensions)
	}
	if len(result.Missing) != 1 || result.Missing[0] != "/blockHash" {
		t.Fatalf("missing = %#v, want blockHash", result.Missing)
	}
	if len(result.Conflicts) != 1 || result.Conflicts[0].Path != "/status" {
		t.Fatalf("conflicts = %#v, want status", result.Conflicts)
	}
}

func TestProjectionExtensionsBindFingerprintAndEvidenceKind(t *testing.T) {
	observation := rpc.CommonObservation{
		Common:    []rpc.FieldObservation{{Path: "/l1Fee", Value: json.RawMessage(`"0x10"`), Sources: []rpc.SourceRef{{ID: "a"}, {ID: "b"}}}},
		Residuals: []rpc.FieldObservation{{Path: "/revertReason", Value: json.RawMessage(`"oops"`), Sources: []rpc.SourceRef{{ID: "a"}}}},
		Conflicts: []rpc.ConflictField{{Path: "/debug", Values: []rpc.FieldObservation{{Path: "/debug", Value: json.RawMessage(`1`), Sources: []rpc.SourceRef{{ID: "a"}}}, {Path: "/debug", Value: json.RawMessage(`2`), Sources: []rpc.SourceRef{{ID: "b"}}}}}},
	}
	p := ProjectStandard(observation, nil)
	set, err := p.Carrier([]byte(`{"hash":"0x01"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Fields) != 4 {
		t.Fatalf("fields = %d, want 4", len(set.Fields))
	}
	if set.Fields[0].Kind != rpc.CorroboratedExtension || set.Fields[1].Kind != rpc.ProviderResidual {
		t.Fatalf("kinds = %#v", set.Fields)
	}
	if set.Fields[0].CoreObjectFingerprint == "" || set.Fields[0].CoreObjectFingerprint != set.Fields[1].CoreObjectFingerprint {
		t.Fatalf("fingerprints not shared: %#v", set.Fields)
	}
}

func TestProjectStandardMatchesArrayPathPatterns(t *testing.T) {
	result := ProjectStandard(rpc.CommonObservation{Common: []rpc.FieldObservation{
		{Path: "/0/address", Value: json.RawMessage(`"0x01"`)},
		{Path: "/1/topics", Value: json.RawMessage(`[]`)},
		{Path: "/0/vendor", Value: json.RawMessage(`true`)},
	}}, LogsStandardFields())
	if len(result.Core) != 2 {
		t.Fatalf("core = %#v, want two wildcard matches", result.Core)
	}
	if len(result.Extensions) != 1 || result.Extensions[0].Path != "/0/vendor" {
		t.Fatalf("extensions = %#v, want vendor field", result.Extensions)
	}
	if len(result.Missing) != 0 {
		t.Fatalf("missing = %#v, wildcard required fields must not be globally required", result.Missing)
	}
}
