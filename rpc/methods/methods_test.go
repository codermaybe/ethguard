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
