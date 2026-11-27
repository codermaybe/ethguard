package transaction

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/typed"
)

func TestTypedSignatureByteCompatibilityReachesPayloadBridge(t *testing.T) {
	fields := recordedMainnetTransaction(t)
	baseline, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	want, err := Encode(baseline)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`"0x00"`, `"0X00"`} {
		t.Run(value, func(t *testing.T) {
			fields["v"], fields["yParity"] = json.RawMessage(value), json.RawMessage(value)
			raw, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			before := bytes.Clone(raw)
			got, err := Encode(raw)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("equivalent signature byte changed canonical value: %v", err)
			}
			if err := Validate(raw); err != nil {
				t.Fatalf("legal signature byte rejected by private payload bridge: %v", err)
			}
			if _, _, err := typed.DecodeTransaction(raw); err != nil {
				t.Fatalf("public raw decoder rejected legal signature byte: %v", err)
			}
			if !bytes.Equal(raw, before) {
				t.Fatal("source evidence mutated")
			}
		})
	}
	fields["v"], fields["yParity"] = json.RawMessage(`"0x000"`), json.RawMessage(`"0x000"`)
	invalid, _ := json.Marshal(fields)
	if _, err := Encode(invalid); err == nil {
		t.Fatal("invalid HexByte width was silently repaired")
	}
	if _, _, err := typed.DecodeTransaction(invalid); err == nil {
		t.Fatal("public decoder silently repaired invalid HexByte width")
	}
}

func TestLegacySignatureQuantityKeepsItsWidthContract(t *testing.T) {
	for _, raw := range officialFixtures(t) {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		if string(fields["type"]) != `"0x0"` {
			continue
		}
		if _, err := ethapi.NewTransaction(raw); err != nil {
			t.Fatalf("official legacy vector invalid: %v", err)
		}
		fields["v"] = json.RawMessage(`"0x01"`)
		invalid, _ := json.Marshal(fields)
		if _, err := Encode(invalid); err == nil {
			t.Fatal("legacy QUANTITY v was treated as HexByte and repaired")
		}
		if _, _, err := typed.DecodeTransaction(invalid); err == nil {
			t.Fatal("public decoder repaired legacy QUANTITY v")
		}
		return
	}
	t.Fatal("no official legacy vector exercised")
}

func TestPublicTransactionInBlockRetainsSenderCaseCompatibility(t *testing.T) {
	fields := recordedMainnetTransaction(t)
	var blockHash ethapi.Hash
	if err := json.Unmarshal(fields["blockHash"], &blockHash); err != nil {
		t.Fatal(err)
	}
	fields["from"] = json.RawMessage(strings.ToUpper(string(fields["from"])))
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := typed.DecodeTransactionInBlock(raw, blockHash, 0)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.From != "0x2252f216f4a494a87025123425181ca1bb754fb8" {
		t.Fatalf("declared sender changed: %s", decoded.From)
	}
}
