package transaction

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/internal/jsonsafe"
	"github.com/codermaybe/ethguard/rpc"
)

func TestPrepareCanonicalizesTypedSignatureBytes(t *testing.T) {
	for _, kind := range []string{"0x1", "0x2", "0x3", "0x4"} {
		t.Run(kind, func(t *testing.T) {
			fields := officialTransactionFields(t, kind)
			for _, spelling := range []struct{ input, want string }{
				{`"0x00"`, `"0x0"`},
				{`"0X00"`, `"0x0"`},
				{`"0x0f"`, `"0xf"`},
				{`"0X0F"`, `"0xf"`},
			} {
				t.Run(spelling.input, func(t *testing.T) {
					// Prepare owns schema representation, before signature integrity.
					fields["v"], fields["yParity"] = json.RawMessage(spelling.input), json.RawMessage(spelling.input)
					raw := marshalRepresentation(t, fields)
					before := bytes.Clone(raw)
					candidate, err := Prepare(raw)
					if err != nil {
						t.Fatal(err)
					}
					var canonical map[string]json.RawMessage
					if err := json.Unmarshal(candidate.Canonical(), &canonical); err != nil {
						t.Fatal(err)
					}
					for _, field := range []string{"v", "yParity"} {
						if string(canonical[field]) != spelling.want {
							t.Fatalf("%s = %s, want %s", field, canonical[field], spelling.want)
						}
					}
					if !bytes.Equal(raw, before) {
						t.Fatal("source evidence mutated")
					}
				})
			}
			for _, field := range []string{"v", "yParity"} {
				t.Run(field+"/invalid-width", func(t *testing.T) {
					fields["v"], fields["yParity"] = json.RawMessage(`"0x0"`), json.RawMessage(`"0x0"`)
					fields[field] = json.RawMessage(`"0x000"`)
					candidate, err := Prepare(marshalRepresentation(t, fields))
					if !errors.Is(err, rpc.ErrMalformedResponse) || errors.Is(err, rpc.ErrUnsupportedTypedObject) || len(candidate.Canonical()) != 0 {
						t.Fatalf("invalid signature byte was repaired: %s, %v", candidate.Canonical(), err)
					}
				})
			}
		})
	}
}

func TestPrepareCanonicalizesSetCodeAuthorizationParity(t *testing.T) {
	fields := officialTransactionFields(t, "0x4")
	var authorizations []map[string]json.RawMessage
	if err := json.Unmarshal(fields["authorizationList"], &authorizations); err != nil {
		t.Fatal(err)
	}
	if len(authorizations) == 0 {
		t.Fatal("set-code fixture has no authorizations")
	}
	for _, spelling := range []struct{ input, want string }{
		{`"0X00"`, `"0x0"`},
		{`"0X01"`, `"0x1"`},
		{`"0X0F"`, `"0xf"`},
		{`"0x000"`, ""},
	} {
		t.Run(spelling.input, func(t *testing.T) {
			for _, authorization := range authorizations {
				authorization["yParity"] = json.RawMessage(spelling.input)
			}
			fields["authorizationList"] = marshalRepresentation(t, authorizations)
			candidate, err := Prepare(marshalRepresentation(t, fields))
			if spelling.want == "" {
				if !errors.Is(err, rpc.ErrMalformedResponse) || errors.Is(err, rpc.ErrUnsupportedTypedObject) || len(candidate.Canonical()) != 0 {
					t.Fatalf("invalid authorization byte was repaired: %s, %v", candidate.Canonical(), err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var canonical struct {
				Authorizations []struct {
					YParity json.RawMessage `json:"yParity"`
				} `json:"authorizationList"`
			}
			if err := json.Unmarshal(candidate.Canonical(), &canonical); err != nil {
				t.Fatal(err)
			}
			if len(canonical.Authorizations) != len(authorizations) {
				t.Fatal("authorization count changed")
			}
			for _, authorization := range canonical.Authorizations {
				if string(authorization.YParity) != spelling.want {
					t.Fatalf("authorization parity = %s, want %s", authorization.YParity, spelling.want)
				}
			}
		})
	}
}

func TestPrepareLeavesCrossBranchSignatureBytesAsOpenValues(t *testing.T) {
	for _, kind := range []string{"0x0", "0x1", "0x2", "0x3"} {
		t.Run(kind, func(t *testing.T) {
			fields := officialTransactionFields(t, kind)
			fields["authorizationList"] = json.RawMessage(`[{"yParity":"0X00","nonce":9007199254740993}]`)
			fields["maxFeePerBlobGas"] = json.RawMessage(`{"text":"keep <>&","hex":"0XAB","n":9007199254740993}`)
			if kind == "0x0" {
				fields["yParity"] = json.RawMessage(`"0X00"`)
			}
			// The blob branch owns maxFeePerBlobGas and would validate its type.
			if kind == "0x3" {
				fields["maxFeePerBlobGas"] = json.RawMessage(`"0x1"`)
			}
			candidate, err := Prepare(marshalRepresentation(t, fields))
			if err != nil {
				t.Fatal(err)
			}
			var canonical map[string]json.RawMessage
			if err := json.Unmarshal(candidate.Canonical(), &canonical); err != nil {
				t.Fatal(err)
			}
			if got, want := string(canonical["authorizationList"]), `[{"nonce":9007199254740993,"yParity":"0x00"}]`; got != want {
				t.Fatalf("cross-branch authorization changed: %s, want %s", got, want)
			}
			if kind == "0x0" && string(canonical["yParity"]) != `"0x00"` {
				t.Fatalf("legacy extra yParity was shortened: %s", canonical["yParity"])
			}
			if kind != "0x3" {
				const want = `{"hex":"0xab","n":9007199254740993,"text":"keep \u003c\u003e\u0026"}`
				if string(canonical["maxFeePerBlobGas"]) != want {
					t.Fatalf("open value lost spelling or number fidelity: %s, want %s", canonical["maxFeePerBlobGas"], want)
				}
			}
		})
	}
}

func TestPrepareRejectsProjectionExpansionBeforeSchema(t *testing.T) {
	fields := recordedMainnetTransaction(t)
	// The raw string fits the safety bound; json.Marshal in the comparison
	// mask escapes each '<' to six bytes before generated schema parsing.
	fields["maxFeePerBlobGas"] = json.RawMessage(`"` + strings.Repeat("<", int(jsonsafe.MaxBodyBytes/6)+1) + `"`)
	for _, kind := range []string{"0x2", "0x7f"} {
		t.Run(kind, func(t *testing.T) {
			fields["type"] = json.RawMessage(`"` + kind + `"`)
			raw := marshalRepresentation(t, fields)
			if err := jsonsafe.Validate(raw); err != nil {
				t.Fatalf("fixture must pass the initial safety gate: %v", err)
			}
			candidate, err := Prepare(raw)
			if !errors.Is(err, jsonsafe.ErrTooLarge) || err.Error() != "jsonsafe: body too large" || len(candidate.Canonical()) != 0 {
				t.Fatalf("expanded comparison key reached schema parsing: bytes=%d, canonical=%d, err=%v", len(raw), len(candidate.Canonical()), err)
			}
			if errors.Is(err, rpc.ErrMalformedResponse) || errors.Is(err, rpc.ErrUnsupportedTypedObject) || errors.Is(err, rpc.ErrInvalidRequest) {
				t.Fatalf("expanded comparison key acquired a later error classification: %v", err)
			}
		})
	}
}

func officialTransactionFields(t *testing.T, kind string) map[string]json.RawMessage {
	for _, raw := range officialFixtures(t) {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		if string(fields["type"]) == `"`+kind+`"` {
			return fields
		}
	}
	t.Fatalf("no official transaction fixture for type %s", kind)
	return nil
}

func marshalRepresentation(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := ethcodec.MarshalNoEscape(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
