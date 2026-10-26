package feehistory

import (
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

func TestRequestCountAcceptsIntegerTypes(t *testing.T) {
	for _, count := range []any{
		uint64(2), uint(2), uint32(2), uint16(2), uint8(2),
		int(2), int64(2), big.NewInt(2), json.Number("2"), "0x2",
	} {
		if got, err := requestQuantity(count); err != nil || got != 2 {
			t.Fatalf("%T: got %d, err %v", count, got, err)
		}
	}
	for _, bad := range []any{-1, int64(-1), json.Number("2.5"), "2", nil} {
		if _, err := requestQuantity(bad); err == nil {
			t.Fatalf("%T was accepted", bad)
		}
	}
	valid := []byte(`{"oldestBlock":"0x1","gasUsedRatio":[0.1,0.2],"baseFeePerGas":["0x1","0x2","0x3"]}`)
	if err := ValidateRange(rpc.Request{Params: []any{2, "latest", []any{}}}, valid); err != nil {
		t.Fatal(err)
	}
}

func TestErrorsAttributeToOldestBlockNotBaseLength(t *testing.T) {
	// An otherwise-valid payload whose only defect is oldestBlock must say
	// so: the merged condition it replaced blamed the baseFeePerGas length
	// rule for an oldestBlock it never parsed.
	onlyOldestBad := []byte(`{"oldestBlock":"123","gasUsedRatio":[0.5],"baseFeePerGas":["0x1","0x2"]}`)
	if _, err := Normalize(onlyOldestBad); err == nil || !strings.Contains(err.Error(), "oldestBlock") {
		t.Fatalf("Normalize: got %v, want oldestBlock attribution", err)
	}
	if _, err := Encode(onlyOldestBad); err == nil || !strings.Contains(err.Error(), "oldestBlock") {
		t.Fatalf("Encode: got %v, want oldestBlock attribution", err)
	}
	// A genuinely wrong baseFeePerGas length keeps its own message.
	badLength := []byte(`{"oldestBlock":"0x1","gasUsedRatio":[0.5],"baseFeePerGas":["0x1"]}`)
	if _, err := Normalize(badLength); err == nil || !strings.Contains(err.Error(), "baseFeePerGas") {
		t.Fatalf("Normalize: got %v, want baseFeePerGas length attribution", err)
	}
	if _, err := Encode(badLength); err == nil || !strings.Contains(err.Error(), "baseFeePerGas") {
		t.Fatalf("Encode: got %v, want baseFeePerGas length attribution", err)
	}
}
