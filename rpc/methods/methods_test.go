package methods

import (
	"encoding/json"
	"testing"
)

func TestChainIDPolicyNormalizesQuantities(t *testing.T) {
	p := ChainIDPolicy()
	first, err := p.Normalize(json.RawMessage(`"0x01"`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.Normalize(json.RawMessage(`"0x1"`))
	if err != nil {
		t.Fatal(err)
	}
	if first != "1" || second != "1" || first != second {
		t.Fatalf("unexpected normalized IDs: %q, %q", first, second)
	}
}

func TestBlockByHashPolicyIgnoresObjectOrderAndHexCase(t *testing.T) {
	p := BlockByHashPolicy()
	first, err := p.Normalize(json.RawMessage(`{"hash":"0xAB","number":"0x1","transactions":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.Normalize(json.RawMessage(`{"transactions":[],"number":"0x1","hash":"0xab"}`))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("expected canonical block values to match: %s != %s", first, second)
	}
}

func TestBlockByNumberPolicyComparesHeightsNumerically(t *testing.T) {
	p := BlockByNumberPolicy()
	first, err := p.Normalize(json.RawMessage(`{"number":"0x010f","hash":"0xAB"}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.Normalize(json.RawMessage(`{"number":"0x10f","hash":"0xab"}`))
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first != "271|0xab" {
		t.Fatalf("expected numerically equal block heads, got %q vs %q", first, second)
	}
}

func TestTransactionReceiptPolicyIgnoresObjectOrderAndHexCase(t *testing.T) {
	p := TransactionReceiptPolicy()
	first, err := p.Normalize(json.RawMessage(`{"transactionHash":"0xAB","status":"0x1","gasUsed":"0x10","logs":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.Normalize(json.RawMessage(`{"logs":[],"gasUsed":"0x10","status":"0x1","transactionHash":"0xab"}`))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("expected canonical receipt values to match: %s != %s", first, second)
	}
}

func TestNormalizeHexDataFoldsCaseAndAllowsEmpty(t *testing.T) {
	first, err := normalizeHexData(json.RawMessage(`"0x6080FE"`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := normalizeHexData(json.RawMessage(`"0x6080fe"`))
	if err != nil {
		t.Fatal(err)
	}
	empty, err := normalizeHexData(json.RawMessage(`"0x"`))
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first != "0x6080fe" || empty != "0x" {
		t.Fatalf("unexpected canonical data: %q, %q, %q", first, second, empty)
	}
	if _, err := normalizeHexData(json.RawMessage(`"6080"`)); err == nil {
		t.Fatal("expected non-0x data to be rejected")
	}
}

func TestNormalizeDecimalStringComparesNumerically(t *testing.T) {
	first, err := normalizeDecimalString(json.RawMessage(`"01"`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := normalizeDecimalString(json.RawMessage(`"1"`))
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first != "1" {
		t.Fatalf("expected numeric equality across spellings, got %q vs %q", first, second)
	}
	if _, err := normalizeDecimalString(json.RawMessage(`"0x1"`)); err == nil {
		t.Fatal("expected hex spelling to be rejected")
	}
}

func TestNormalizeCallOutcomeClustersResultsAndReverts(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    string
		wantErr bool
	}{
		{"return data compares case-insensitively", `"0xDEADbeef"`, "ok|0xdeadbeef", false},
		{"empty result is valid data", `"0x"`, "ok|0x", false},
		{"revert wording collapses to one key", `{"code":-32000,"message":"execution reverted: ERC20: insufficient allowance"}`, "reverted", false},
		{"bare revert collapses to the same key", `{"code":-32000,"message":"execution reverted"}`, "reverted", false},
		{"legacy parity revert code", `{"code":3,"message":"Reverted"}`, "reverted", false},
		{"geth client timeout is a fault, not an observation", `{"code":-32002,"message":"request timed out"}`, "", true},
		{"internal error is a fault", `{"code":-32603,"message":"panic"}`, "", true},
		{"unrecognized payload is a fault", `{"neither":"result nor error"}`, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, err := normalizeCallOutcome(json.RawMessage(tc.payload))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected fault, got key %q", key)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if key != tc.want {
				t.Fatalf("expected key %q, got %q", tc.want, key)
			}
		})
	}
}
