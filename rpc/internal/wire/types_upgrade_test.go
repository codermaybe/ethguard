package wire_test

import (
	"encoding/json"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/internal/spec/conformance"
)

func TestUpgradedScalarBoundsAndCompatibilityNormalization(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{`"0x0"`, "0x0"}, {`"0x00"`, "0x0"}, {`"0xff"`, "0xff"}, {`"0x4A"`, "0x4a"}, {`"0X4A"`, "0x4a"},
	} {
		value, err := ethapi.NewHexByte(json.RawMessage(tc.raw))
		if err != nil || string(value) != tc.want {
			t.Errorf("hex byte %s = %s (%v), want %s", tc.raw, value, err, tc.want)
		}
	}
	for _, raw := range []string{`"0x"`, `"0x100"`, `"0xgg"`, `null`, `1`} {
		if _, err := ethapi.NewHexByte(json.RawMessage(raw)); err == nil {
			t.Errorf("invalid byte accepted: %s", raw)
		}
	}
	for _, tc := range []struct{ raw, want string }{{`"0"`, "0"}, {`"1"`, "1"}, {`"000"`, "0"}, {`"900719925474099312345678901234567890"`, "900719925474099312345678901234567890"}} {
		value, err := ethapi.NewVersionResult(json.RawMessage(tc.raw))
		if err != nil || string(value) != tc.want {
			t.Errorf("version %s = %s (%v)", tc.raw, value, err)
		}
	}
	for _, raw := range []string{`"-1"`, `"0x1"`, `""`, `null`, `0`} {
		if _, err := ethapi.NewVersionResult(json.RawMessage(raw)); err == nil {
			t.Errorf("invalid version accepted: %s", raw)
		}
	}
	// Raw official constraints remain stricter than canonical input spelling.
	byteSchema := map[string]any{"type": "string", "pattern": "^0x[0-9a-f]{1,2}$"}
	for _, raw := range []string{`"0x4A"`, `"0X4A"`, `"0x"`} {
		if err := conformance.ValidateResult(byteSchema, json.RawMessage(raw)); err == nil {
			t.Errorf("raw byte schema accepted: %s", raw)
		}
	}
}

func TestUpgradedSlotNumberGeneratedBoundary(t *testing.T) {
	for _, method := range []string{"eth_getBlockByNumber", "eth_getBlockByHash"} {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(firstObjectResult(t, method), &fields); err != nil {
			t.Fatal(err)
		}
		for _, raw := range []string{`"0x0"`, `"0xffffffffffffffff"`, `null`, `"0x10000000000000000"`, `"0x01"`, `true`} {
			fields["slotNumber"] = json.RawMessage(raw)
			input, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			value, err := generatedResultParsers()[method](input)
			valid := raw == `"0x0"` || raw == `"0xffffffffffffffff"`
			if (err == nil) != valid {
				t.Errorf("%s slot %s: %v", method, raw, err)
				continue
			}
			if err == nil {
				out, err := ethcodec.MarshalNoEscape(value)
				if err != nil {
					t.Fatal(err)
				}
				var output map[string]json.RawMessage
				if err := json.Unmarshal(out, &output); err != nil {
					t.Fatal(err)
				}
				if len(output["slotNumber"]) == 0 {
					t.Fatal("official slot number was dropped")
				}
			}
		}
	}
}

func TestUpgradedCallMsgBlobSidecarsPreservePresenceAndValidateItems(t *testing.T) {
	for _, raw := range []string{`{}`, `{"commitments":[],"proofs":[]}`, `{"commitments":["0x0001"],"proofs":["0x0002"]}`} {
		value, err := ethapi.NewCallMsg(json.RawMessage(raw))
		if err != nil {
			t.Fatal(err)
		}
		out, err := ethcodec.MarshalNoEscape(value)
		if err != nil || string(out) != raw {
			t.Fatalf("call sidecars changed: %s -> %s (%v)", raw, out, err)
		}
	}
	for _, raw := range []string{`{"commitments":null}`, `{"proofs":null}`, `{"commitments":"0x00"}`, `{"proofs":[true]}`, `{"commitments":["invalid"]}`} {
		if _, err := ethapi.NewCallMsg(json.RawMessage(raw)); err == nil {
			t.Errorf("invalid call sidecars accepted: %s", raw)
		}
	}
}
