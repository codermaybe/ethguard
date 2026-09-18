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
