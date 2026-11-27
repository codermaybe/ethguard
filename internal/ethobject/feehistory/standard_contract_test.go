package feehistory

import (
	"encoding/json"
	"testing"
)

func TestRequiredArraysAndRewardRowsRetainSchemaPresence(t *testing.T) {
	// These historical inputs were collapsed to empty slices by struct decode.
	// They are an explicit acceptance tightening, not a spelling compatibility.
	for _, raw := range []string{
		`{"oldestBlock":"0x1","baseFeePerGas":["0x1"]}`,
		`{"oldestBlock":"0x1","baseFeePerGas":["0x1"],"gasUsedRatio":null}`,
		`{"oldestBlock":"0x1","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[0.5],"reward":[null]}`,
	} {
		if _, err := Normalize(json.RawMessage(raw)); err == nil {
			t.Fatalf("normalizer accepted missing/null schema value: %s", raw)
		}
		if _, err := Encode(json.RawMessage(raw)); err == nil {
			t.Fatalf("encoder accepted missing/null schema value: %s", raw)
		}
	}
	for _, raw := range []string{
		`{"oldestBlock":"0x1","baseFeePerGas":["0x1"],"gasUsedRatio":[]}`,
		`{"oldestBlock":"0X1","baseFeePerGas":["0X1","0x2"],"gasUsedRatio":[0.5],"reward":null,"baseFeePerBlobGas":null,"blobGasUsedRatio":null}`,
		`{"oldestBlock":"0x1","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[0.5],"reward":[[]]}`,
	} {
		if _, err := Encode(json.RawMessage(raw)); err != nil {
			t.Fatalf("valid/finite-compatible value rejected: %s %v", raw, err)
		}
	}
}
