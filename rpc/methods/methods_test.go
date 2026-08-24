package methods

import (
	"encoding/json"
	"testing"
)

func TestChainIDPolicyNormalizesQuantities(t *testing.T) {
	p := ChainIDPolicy{}
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
	p := BlockByHashPolicy{}
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
