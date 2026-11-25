package hexcompat

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
)

func TestSimulateResultTouchesOnlyDeclaredHexPaths(t *testing.T) {
	raw := json.RawMessage(`[{"size":"0XA","calls":[{"gasUsed":"0XA","returnData":"0XAB00","error":{"code":-32015.0,"message":"0XAB","data":"0xAB"},"vendor":{"gasUsed":"0XAB"}}],"vendor":{"size":"0XAB","n":9007199254740993}}]`)
	before := bytes.Clone(raw)
	got, err := SimulateResult(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"calls":[{"error":{"code":-32015.0,"message":"0XAB","data":"0xAB"},"gasUsed":"0xa","returnData":"0xab00","vendor":{"gasUsed":"0XAB"}}],"size":"0xa","vendor":{"size":"0XAB","n":9007199254740993}}]`
	if string(got) != want {
		t.Fatalf("unexpected finite projection:\n got %s\nwant %s", got, want)
	}
	if !bytes.Equal(raw, before) {
		t.Fatal("source evidence mutated")
	}
}

func TestBlockBodySelectsFieldsAndRetainsValidationFailures(t *testing.T) {
	address := "0X" + strings.Repeat("AB", 20)
	for _, tc := range []struct {
		name, withdrawal string
		valid            bool
	}{
		{"extension", `{"index":"0XA","validatorIndex":"0XB","address":"` + address + `","amount":"0XC","vendor":"0XAB"}`, true},
		{"missing", `{"index":"0XA","validatorIndex":"0XB","address":"` + address + `"}`, false},
		{"null", `{"index":"0XA","validatorIndex":"0XB","address":null,"amount":"0XC"}`, false},
		{"wrong-shape", `{"index":"0XA","validatorIndex":"0XB","address":{},"amount":"0XC"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := json.RawMessage(`{"number":"0X8","size":"0XA","withdrawals":[` + tc.withdrawal + `],"vendor":{"gas":"0XAB"}}`)
			got, err := BlockBody(raw)
			if err != nil {
				t.Fatal(err)
			}
			var body struct {
				Size        string            `json:"size"`
				Withdrawals []json.RawMessage `json:"withdrawals"`
			}
			if err := json.Unmarshal(got, &body); err != nil {
				t.Fatal(err)
			}
			if body.Size != "0xa" || bytes.Contains(got, []byte(`"vendor"`)) || bytes.Contains(got, []byte(`"number"`)) {
				t.Fatalf("body selection changed: %s", got)
			}
			_, err = ethapi.NewWithdrawal(body.Withdrawals[0])
			if (err == nil) != tc.valid {
				t.Fatalf("projection hid required/type failure: %s: %v", got, err)
			}
		})
	}
}

func TestTransactionUsesSelectedBranchForHexByte(t *testing.T) {
	for _, tc := range []struct {
		kind, value, want string
	}{
		{"0X0", "0X01", "0x01"}, // legacy v is QUANTITY; never repair its width
		{"0X1", "0X00", "0x0"},
		{"0X2", "0X01", "0x1"},
		{"0X3", "0X00", "0x0"},
		{"0X4", "0X01", "0x1"},
		{"0X2", "0X000", "0x000"}, // invalid HexByte width remains invalid
	} {
		t.Run(tc.kind+"/"+tc.value, func(t *testing.T) {
			got, err := Transaction(json.RawMessage(`{"type":"` + tc.kind + `","v":"` + tc.value + `","input":"0XAB00","gas":"0X01","vendor":{"v":"0X00"}}`))
			if err != nil {
				t.Fatal(err)
			}
			want := `{"gas":"0x01","input":"0xab00","type":"` + strings.ToLower(tc.kind) + `","v":"` + tc.want + `","vendor":{"v":"0X00"}}`
			if string(got) != want {
				t.Fatalf("unexpected branch compatibility:\n got %s\nwant %s", got, want)
			}
			if _, err := ethapi.NewQuantity(json.RawMessage(`"0x01"`)); err == nil {
				t.Fatal("leading-zero quantity became accepted")
			}
		})
	}
	for _, raw := range []string{`{"v":"0X00"}`, `{"type":"0X9","v":"0X00"}`, `{"type":2,"v":"0X00"}`} {
		got, err := Transaction(json.RawMessage(raw))
		if err != nil || string(got) != raw {
			t.Fatalf("unknown/malformed discriminator was rewritten: %s: %v", got, err)
		}
	}
}

func TestCompatibilityRejectsUnsafeJSONBeforeProjection(t *testing.T) {
	for name, prepare := range map[string]func(json.RawMessage) (json.RawMessage, error){"block": BlockBody, "transaction": Transaction, "simulate": SimulateResult} {
		t.Run(name, func(t *testing.T) {
			for _, raw := range []string{`{"vendor":1,"vendor":2}`, `{"vendor":{"gas":1,"gas":2}}`, `{"vendor":`} {
				if _, err := prepare(json.RawMessage(raw)); err == nil {
					t.Fatalf("unsafe JSON reached compatibility projection: %s", raw)
				}
			}
		})
	}
}
