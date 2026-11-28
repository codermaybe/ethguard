package feehistory

import (
	"testing"
)

// Two invariants: the canonical form re-encodes to itself, and whatever the
// encoder accepts the normalizer accepts with the same comparison key — the
// two must never disagree about which payloads vote.
func FuzzEncodeIdempotentAndNormalizeAgrees(f *testing.F) {
	f.Add([]byte(`{"oldestBlock":"0x10","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[0.5],"baseFeePerBlobGas":["0x1","0x2"],"blobGasUsedRatio":[0.5]}`))
	f.Add([]byte(`{"oldestBlock":"123","gasUsedRatio":[],"baseFeePerGas":["0x1"]}`))
	f.Add([]byte(`{"oldestBlock":"0x1","gasUsedRatio":[0.5,0.5],"baseFeePerGas":["0x1","0x2","0x3"],"reward":[["0x1","0x2"],["0x3","0x4"]]}`))
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		out, err := Encode(data)
		if err != nil {
			return
		}
		want, err := Normalize(data)
		if err != nil {
			t.Fatalf("Encode accepted what Normalize rejects: %v", err)
		}
		got, err := Normalize(out)
		if err != nil {
			t.Fatalf("canonical output rejected by Normalize: %v", err)
		}
		if got != want {
			t.Fatalf("Normalize disagrees with Encode:\ninput   %q\ncanonical %q\nkeys %q vs %q", data, out, want, got)
		}
		again, err := Encode(out)
		if err != nil || string(again) != string(out) {
			t.Fatalf("encoding is not a fixpoint (%v)", err)
		}
	})
}
