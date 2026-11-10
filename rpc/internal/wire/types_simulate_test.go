package wire_test

// SimulateV1 tagged-union discrimination: the official result merges failure
// and success into one oneOf selection over shared spellings. These payloads
// pin branch selection (status/value/error) and the canonical round trip:
// every fixture is spelled in the generated canonical field order, so
// parse->marshal must reproduce the input bytes exactly.

import (
	"encoding/json"
	"testing"

	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

func TestSimulateCallResultFailureBranch(t *testing.T) {
	canonical := `{"error":{"code":3,"message":"execution reverted"},"gasUsed":"0x0","returnData":"0x","status":"0x0"}`
	decoded, err := wire.NewSimulateCallResult(json.RawMessage(canonical))
	if err != nil {
		t.Fatalf("failure payload must parse: %v", err)
	}
	if decoded.ResultOfCallFailure == nil {
		t.Fatal("failure payload must select the failure branch")
	}
	if decoded.ResultOfCallSuccess != nil {
		t.Fatal("failure payload must not select the success branch")
	}
	if decoded.ResultOfCallFailure.Status != "0x0" {
		t.Fatalf("failure status = %q, want 0x0", decoded.ResultOfCallFailure.Status)
	}
	reencoded, err := wire.MarshalNoEscape(decoded)
	if err != nil {
		t.Fatalf("failure marshal: %v", err)
	}
	if string(reencoded) != canonical {
		t.Fatalf("failure round trip diverges:\n got  %s\n want %s", reencoded, canonical)
	}
}

func TestSimulateCallResultVMBranch(t *testing.T) {
	// The VM error branch has no required message: the bare const code
	// already discriminates it after the revert branch falls short.
	canonical := `{"code":-32015}`
	errorValue, err := wire.NewSimulateCallError(json.RawMessage(canonical))
	if err != nil {
		t.Fatalf("vm error payload must parse: %v", err)
	}
	if errorValue.SimulateErrorRevert != nil || errorValue.SimulateErrorVM == nil {
		t.Fatalf("vm error payload must select the VM branch, got %+v", errorValue)
	}

	call := `{"error":{"code":-32015},"gasUsed":"0x0","returnData":"0x","status":"0x0"}`
	decoded, err := wire.NewSimulateCallResult(json.RawMessage(call))
	if err != nil {
		t.Fatalf("vm call payload must parse: %v", err)
	}
	if decoded.ResultOfCallFailure == nil || decoded.ResultOfCallFailure.Error.SimulateErrorVM == nil {
		t.Fatalf("vm call payload must land on the vm error branch")
	}
	reencoded, err := wire.MarshalNoEscape(decoded)
	if err != nil {
		t.Fatalf("vm call marshal: %v", err)
	}
	if string(reencoded) != call {
		t.Fatalf("vm round trip diverges:\n got  %s\n want %s", reencoded, call)
	}
}

func TestSimulateCallResultSuccessBranch(t *testing.T) {
	canonical := `{"gasUsed":"0x1","logs":[{"transactionHash":"0x5bc704d4eb4ce7fe319705d2f888516961426a177f2799c9f934b5df7466dd33"}],"returnData":"0x","status":"0x1"}`
	decoded, err := wire.NewSimulateCallResult(json.RawMessage(canonical))
	if err != nil {
		t.Fatalf("success payload must parse: %v", err)
	}
	if decoded.ResultOfCallSuccess == nil {
		t.Fatal("success payload must select the success branch")
	}
	if decoded.ResultOfCallFailure != nil {
		t.Fatal("success payload must not select the failure branch")
	}
	if decoded.ResultOfCallSuccess.GasUsed != "0x1" {
		t.Fatalf("success gasUsed = %q, want 0x1", decoded.ResultOfCallSuccess.GasUsed)
	}
	reencoded, err := wire.MarshalNoEscape(decoded)
	if err != nil {
		t.Fatalf("success marshal: %v", err)
	}
	if string(reencoded) != canonical {
		t.Fatalf("success round trip diverges:\n got  %s\n want %s", reencoded, canonical)
	}
}

func TestSimulateCallResultRejectsBranchlessPayload(t *testing.T) {
	// Neither failure (needs error) nor success (needs status 0x1... the
	// official success schema fixes status by pattern spelling 0x1): a
	// status-only object matches no official branch.
	payload := `{"gasUsed":"0x1","returnData":"0x","status":"0x0"}`
	if _, err := wire.NewSimulateCallResult(json.RawMessage(payload)); err == nil {
		t.Fatal("branchless payload must reject")
	}
}

func TestSimulateCallErrorOfficialOptionalConstants(t *testing.T) {
	// The VM branch has no required members; the revert branch requires code
	// and message. Present members still must satisfy the selected branch.
	for _, payload := range []string{
		`{}`,
		`{"code":-32015}`,
		`{"message":"vm execution error"}`,
		`{"code":3,"message":"execution reverted"}`,
		`{"code":-32015,"message":"vm execution error: out of gas"}`,
	} {
		t.Run(payload, func(t *testing.T) {
			value, err := wire.NewSimulateCallError(json.RawMessage(payload))
			if err != nil {
				t.Fatalf("official optional-constant shape rejected: %v", err)
			}
			out, err := wire.MarshalNoEscape(value)
			if err != nil || string(out) != payload {
				t.Fatalf("optional constants changed: got %s (%v), want %s", out, err, payload)
			}
		})
	}
}

func TestSimulateCallErrorRejectsInvalidConstantsAndShapes(t *testing.T) {
	for _, payload := range []string{
		`{"code":7}`,
		`{"code":3}`,
		`{"code":3,"message":"wrong"}`,
		`{"code":-32015,"message":"wrong"}`,
		`{"code":3,"message":"vm execution error"}`,
		`{"code":-32015,"message":"execution reverted"}`,
		`null`,
		`[]`,
		`3`,
		`"execution reverted"`,
		`true`,
	} {
		t.Run(payload, func(t *testing.T) {
			if _, err := wire.NewSimulateCallError(json.RawMessage(payload)); err == nil {
				t.Fatalf("invalid simulate error admitted: %s", payload)
			}
		})
	}
}

func TestSimulateErrorVMOptionalPropertiesStillConstrainPresentValues(t *testing.T) {
	for _, raw := range []string{`{}`, `{"code":-32015}`} {
		value, err := wire.NewSimulateErrorVM(json.RawMessage(raw))
		if err != nil {
			t.Fatalf("valid optional VM properties rejected: %s: %v", raw, err)
		}
		out, err := wire.MarshalNoEscape(value)
		if err != nil || string(out) != raw {
			t.Fatalf("VM optional properties changed: got %s (%v), want %s", out, err, raw)
		}
	}
	for _, raw := range []string{`{"code":3}`, `{"code":-32015,"message":"not a VM error"}`, `null`, `[]`, `true`} {
		if _, err := wire.NewSimulateErrorVM(json.RawMessage(raw)); err == nil {
			t.Errorf("VM optional constraint ignored: %s", raw)
		}
	}
}
